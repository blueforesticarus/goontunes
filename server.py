"""
Goontunes Minimal MP3 Streaming Server

- Serves player.html at '/'
- Streams MP3s from 'tracks/' folder at '/stream' (with Range support)
- Playback controls: /next, /prev, /pause, /play
- WebSocket (SocketIO) for playback state sync
- Auto-advance to next track
- All logic in this file, with endpoint/state documentation
"""

import os
import mimetypes
import time
import threading
from flask import Flask, send_file, request, Response, jsonify, send_from_directory
from flask_socketio import SocketIO, emit
from werkzeug.middleware.proxy_fix import ProxyFix

TRACK_FOLDER = os.path.join(os.path.dirname(__file__), 'tracks')
SUPPORTED_EXTS = ('.mp3', '.mpeg', '.ogg', '.webm', '.wav')
BROADCAST_INTERVAL = 0.5  # seconds

app = Flask(__name__)
app.wsgi_app = ProxyFix(app.wsgi_app)
sio = SocketIO(app, cors_allowed_origins="*", async_mode='threading')

from analyse import (
    ensure_beats,
    beat_cache_path,
    read_cached_beats,
    pending_jobs,
    pending_lock,
)

# --- Playback State ---
# {
#   'track_idx': int,   # index in track_files
#   'paused': bool,
#   'position': float,  # seconds
#   'last_update': float, # server time of last state change
# }
state = {
    'track_idx': 0,
    'paused': False,
    'position': 0.0,
    'last_update': time.time(),
}

# --- Track List ---
def get_track_files():
    if not os.path.isdir(TRACK_FOLDER):
        return []
    files = [f for f in os.listdir(TRACK_FOLDER) if f.lower().endswith(SUPPORTED_EXTS)]
    files.sort()
    return files
track_files = get_track_files()


def mime_for(fname):
    """Return a best-effort MIME type for an audio file name."""
    ext = os.path.splitext(fname)[1].lower()
    mapping = {
        '.mp3': 'audio/mpeg',
        '.mpeg': 'audio/mpeg',
        '.ogg': 'audio/ogg',
        '.webm': 'audio/webm',
        '.wav': 'audio/wav',
    }
    if ext in mapping:
        return mapping[ext]
    t, _ = mimetypes.guess_type(fname)
    return t or 'application/octet-stream'

# --- Serve player.html ---
@app.route('/')
def serve_player():
    return send_file('player.html')

# --- Serve static files (if needed) ---
@app.route('/<path:filename>')
def serve_static(filename):
    if filename.endswith('.html'):
        return send_file(filename)
    return send_from_directory('.', filename)

# --- MP3 Streaming with Range support ---
@app.route('/stream')
def stream_mp3():
    """Streams the current MP3 file with HTTP Range support."""
    if not track_files:
        return ("No tracks available", 404)
    fname = track_files[state['track_idx']]
    path = os.path.join(TRACK_FOLDER, fname)
    range_header = request.headers.get('Range', None)
    file_size = os.path.getsize(path)
    if not range_header:
        return send_file(path, mimetype=mime_for(fname))
    byte1, byte2 = 0, None
    m = None
    import re
    m = re.search(r'bytes=(\d+)-(\d*)', range_header)
    if m:
        g = m.groups()
        byte1 = int(g[0])
        if g[1]:
            byte2 = int(g[1])
    length = file_size - byte1 if byte2 is None else byte2 - byte1 + 1
    with open(path, 'rb') as f:
        f.seek(byte1)
        data = f.read(length)
    rv = Response(data, 206, mimetype=mime_for(fname), direct_passthrough=True)
    rv.headers.add('Content-Range', f'bytes {byte1}-{byte1+length-1}/{file_size}')
    rv.headers.add('Accept-Ranges', 'bytes')
    rv.headers.add('Content-Length', str(length))
    return rv

# --- Playback Controls ---
@app.route('/next', methods=['POST'])
def next_track():
    if not track_files:
        return ("No tracks", 404)
    state['track_idx'] = (state['track_idx'] + 1) % len(track_files)
    state['position'] = 0.0
    state['last_update'] = time.time()
    state['paused'] = False
    sio.emit('state', get_state())
    # start beat extraction in background for new track
    try:
        ensure_beats(os.path.join(TRACK_FOLDER, track_files[state['track_idx']]))
    except Exception:
        pass
    return ('', 204)

@app.route('/prev', methods=['POST'])
def prev_track():
    if not track_files:
        return ("No tracks", 404)
    state['track_idx'] = (state['track_idx'] - 1) % len(track_files)
    state['position'] = 0.0
    state['last_update'] = time.time()
    state['paused'] = False
    sio.emit('state', get_state())
    try:
        ensure_beats(os.path.join(TRACK_FOLDER, track_files[state['track_idx']]))
    except Exception:
        pass
    return ('', 204)

@app.route('/pause', methods=['POST'])
def pause_track():
    if not track_files:
        return ("No tracks", 404)
    if not state['paused']:
        state['position'] = get_position()
        state['paused'] = True
        state['last_update'] = time.time()
        sio.emit('state', get_state())
    return ('', 204)

@app.route('/play', methods=['POST'])
def play_track():
    if not track_files:
        return ("No tracks", 404)
    if state['paused']:
        state['position'] = get_position()
        state['paused'] = False
        state['last_update'] = time.time()
        sio.emit('state', get_state())
        try:
            ensure_beats(os.path.join(TRACK_FOLDER, track_files[state['track_idx']]))
        except Exception:
            pass
    return ('', 204)

# --- Playback State Helpers ---
def get_position():
    if state['paused']:
        return state['position']
    dt = time.time() - state['last_update']
    return state['position'] + dt

def get_state():
    if not track_files:
        return {
            'track_idx': 0,
            'track': None,
            'paused': state['paused'],
            'position': get_position(),
            'duration': 0.0,
        }
    return {
        'track_idx': state['track_idx'],
        'track': track_files[state['track_idx']],
        'paused': state['paused'],
        'position': get_position(),
        'duration': get_track_duration(),
    }

def get_track_duration():
    # Optionally use mutagen for real duration, else fake (300s)
    try:
        from mutagen.mp3 import MP3
        if not track_files:
            return 0.0
        fname = os.path.join(TRACK_FOLDER, track_files[state['track_idx']])
        return float(MP3(fname).info.length)
    except Exception:
        return 300.0

# --- WebSocket State Broadcast ---
@sio.on('connect')
def ws_connect():
    emit('state', get_state())
    # proactively start beat extraction for the currently selected track
    if track_files:
        try:
            ensure_beats(os.path.join(TRACK_FOLDER, track_files[state['track_idx']]))
        except Exception:
            pass


@app.route('/api/beats')
def api_beats():
    """Return beat grid for a track by id (filename).

    Query params: ?id=<track_filename>
    Responses:
      - missing: track unknown
      - processing: analysis running or started
      - ready: beats available
    """
    track_id = request.args.get('id')
    if not track_id:
        return jsonify({'status': 'missing'})
    # ensure requested file exists in track list
    if track_id not in track_files:
        return jsonify({'status': 'missing'})

    audio_path = os.path.join(TRACK_FOLDER, track_id)
    cache_file = beat_cache_path(audio_path)
    if cache_file.exists():
        beats = read_cached_beats(audio_path) or []
        return jsonify({'status': 'ready', 'beats': beats})

    # if a job is currently pending for this cache key -> processing
    job_key = str(cache_file)
    with pending_lock:
        if job_key in pending_jobs:
            return jsonify({'status': 'processing'})

    # start processing now and report processing
    try:
        ensure_beats(audio_path)
    except Exception:
        pass
    return jsonify({'status': 'processing'})

# --- Auto-advance Thread ---
def auto_advance():
    while True:
        if not state['paused']:
            if get_position() >= get_track_duration() - 0.5:
                state['track_idx'] = (state['track_idx'] + 1) % len(track_files)
                state['position'] = 0.0
                state['last_update'] = time.time()
                sio.emit('state', get_state())
                try:
                    ensure_beats(os.path.join(TRACK_FOLDER, track_files[state['track_idx']]))
                except Exception:
                    pass
        sio.emit('state', get_state())
        time.sleep(BROADCAST_INTERVAL)

threading.Thread(target=auto_advance, daemon=True).start()

if __name__ == '__main__':
    print(f"Serving {len(track_files)} tracks from {TRACK_FOLDER}")
    sio.run(app, host='0.0.0.0', port=5000)
