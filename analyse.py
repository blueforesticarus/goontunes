import json
import hashlib
import threading
from pathlib import Path

# cache directory for beat JSON files
CACHE_DIR = Path(__file__).parent / 'cache' / 'beats'

# keys of currently running jobs (cache keys)
pending_jobs: set = set()
pending_lock = threading.Lock()


def beat_cache_path(audio_path: str) -> Path:
    """Return a stable cache file path for the given audio file.

    The cache key is based on absolute path, mtime and filesize so it
    invalidates automatically when the file changes.
    """
    p = Path(audio_path)
    stat = p.stat()
    key_src = f"{str(p.resolve())}:{int(stat.st_mtime)}:{stat.st_size}"
    h = hashlib.sha256(key_src.encode('utf-8')).hexdigest()
    return CACHE_DIR / f"{h}.json"


def extract_beats(audio_path: str) -> list[float]:
    # Lazy import madmom to avoid hard dependency at module import time
    from madmom.features.beats import RNNBeatProcessor, DBNBeatTrackingProcessor

    activations = RNNBeatProcessor()(audio_path)
    beats = DBNBeatTrackingProcessor(fps=100)(activations)
    return beats.tolist()


def ensure_beats(audio_path: str):
    """Ensure a beat cache exists for audio_path.

    If a cache file exists already this returns immediately. Otherwise
    it starts a background daemon thread to compute and save the beats.
    Multiple concurrent calls for the same file will not spawn duplicate
    jobs.
    """
    cache_file = beat_cache_path(audio_path)
    if cache_file.exists():
        return

    # Use the cache key (filename of cache file) to dedupe jobs
    job_key = str(cache_file)
    with pending_lock:
        if job_key in pending_jobs:
            return
        pending_jobs.add(job_key)

    def worker():
        try:
            try:
                beats = extract_beats(audio_path)
            except Exception:
                beats = []

            cache_file.parent.mkdir(parents=True, exist_ok=True)
            cache_file.write_text(json.dumps({
                "version": 1,
                "beats": beats,
            }))
        finally:
            with pending_lock:
                pending_jobs.discard(job_key)

    t = threading.Thread(target=worker, daemon=True)
    t.start()


def read_cached_beats(audio_path: str):
    cache_file = beat_cache_path(audio_path)
    if not cache_file.exists():
        return None
    try:
        data = json.loads(cache_file.read_text())
        return data.get('beats', [])
    except Exception:
        return None
