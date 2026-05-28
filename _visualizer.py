#!/usr/bin/env python3
"""
Guitar Hero Spotify Visualizer — Web Playback Edition
=====================================================
Streams audio via the Spotify Web Playback SDK running in a local browser tab.
The browser does real-time beat detection with the Web Audio API and sends
beat/energy events over a WebSocket to the Python pygame renderer.

Architecture:
  • HTTP server  (localhost:8765) — serves player.html + /token endpoint
  • WebSocket server (localhost:8766) — receives beat/energy/state events
  • pygame — Guitar Hero-style note highway driven by those events

Requirements (nix develop provides these):
    pygame  spotipy  websockets

Spotify Premium is required for the Web Playback SDK.

Run:
    python visualizer.py
"""

import argparse
import asyncio
import colorsys
import json
import math
import os
import queue
import random
import sys
import threading
import time
import webbrowser
from dataclasses import dataclass
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import List, Optional, Tuple

import pygame
import spotipy
import websockets
from spotipy.oauth2 import SpotifyOAuth

import env  # local env.py with client_id, client_secret, redirect_uri

_HERE = os.path.dirname(os.path.abspath(__file__))

# ─────────────────────────── constants ───────────────────────────────────────

WIDTH, HEIGHT = 1280, 720
FPS = 60

LANES = 5
LANE_COLORS_BASE = [
    (0,   200, 255),   # cyan
    (255,  80,  80),   # red
    (80,  255,  80),   # green
    (255, 200,   0),   # yellow
    (180,  80, 255),   # purple
]
NOTE_W = 100
NOTE_H = 28
NOTE_SPEED = 420         # pixels / second
HIGHWAY_TOP_Y = 80       # where notes are spawned (far end)
HIT_LINE_Y = HEIGHT - 140

PERSPECTIVE_STRENGTH = 0.55   # how much the highway narrows at the top
CAMERA_TRANSITION_SPEED = 3.0  # lerp speed for camera parameters

BG_BASE = (8, 6, 18)

# ─────────────────────────── data structures ─────────────────────────────────

@dataclass
class Note:
    lane: int
    spawn_y: float        # starting y (top)
    y: float              # current y
    loudness: float       # 0..1 mapped from segment loudness
    hit: bool = False
    hit_time: float = 0.0
    alpha: int = 255

@dataclass
class Flash:
    x: float
    y: float
    radius: float
    color: Tuple[int, int, int]
    life: float = 1.0     # 1 → 0

@dataclass
class CameraState:
    tilt: float = 0.0        # extra vertical tilt (degrees) – affects perspective origin
    roll: float = 0.0        # lane-spread multiplier
    fov_pulse: float = 0.0   # extra zoom (0..1)
    hue_shift: float = 0.0   # palette hue rotation (0..1)
    bg_brightness: float = 0.0

# ─────────────────────────── Spotify helpers ─────────────────────────────────

_CACHE_PATH = os.path.join(_HERE, ".spotify_token")
_SCOPE = (
    "streaming "
    "user-read-email "
    "user-read-private "
    "user-read-currently-playing "
    "user-read-playback-state "
    "user-modify-playback-state"
)


def build_spotify() -> spotipy.Spotify:
    auth = SpotifyOAuth(
        client_id=env.client_id,
        client_secret=env.client_secret,
        redirect_uri=env.redirect_uri,
        scope=_SCOPE,
        cache_path=_CACHE_PATH,
        open_browser=True,
    )

    # Try cached / refreshed token first; if none, run interactive OAuth.
    token_info = auth.get_cached_token()
    if token_info and auth.is_token_expired(token_info):
        token_info = auth.refresh_access_token(token_info["refresh_token"])
    if not token_info:
        print(f"\nNo cached Spotify token found.")
        print(f"A browser window will open — log in and authorise the app.")
        print(f"Then paste the full redirect URL here when prompted.\n")
        code = auth.get_auth_response()
        auth.get_access_token(code)

    return spotipy.Spotify(auth_manager=auth)


def get_track_and_progress(sp: spotipy.Spotify, track_id: Optional[str]):
    if track_id:
        track = sp.track(track_id)
        progress_ms = 0
    else:
        playback = sp.current_playback()
        if not playback or not playback.get("item"):
            print("No track currently playing. Pass --track-id to specify one.")
            sys.exit(1)
        track = playback["item"]
        progress_ms = playback.get("progress_ms", 0)
    return track, progress_ms


# ─────────────────────────── lane helper ─────────────────────────────────────

def _spread_lane(used_lanes: List[int]) -> int:
    """Pick a lane avoiding recent repeats."""
    lane = random.randint(0, LANES - 1)
    if used_lanes:
        attempts = 0
        while lane in used_lanes[-min(2, len(used_lanes)):] and attempts < 10:
            lane = random.randint(0, LANES - 1)
            attempts += 1
    return lane


# ─────────────────────────── HTTP server ─────────────────────────────────────

PORT_HTTP = 8765
PORT_WS   = 8766

_sp_ref: Optional[spotipy.Spotify] = None   # set after auth


class _PlayerHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == '/':
            data = open(os.path.join(_HERE, 'player.html'), 'rb').read()
            self._respond(200, 'text/html', data)
        elif self.path == '/token':
            token = _get_access_token()
            if token:
                body = json.dumps({'access_token': token}).encode()
                self._respond(200, 'application/json', body)
            else:
                self._respond(503, 'text/plain', b'token unavailable')
        else:
            self._respond(404, 'text/plain', b'not found')

    def _respond(self, code, ctype, body):
        self.send_response(code)
        self.send_header('Content-Type', ctype)
        self.send_header('Access-Control-Allow-Origin', '*')
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):  # silence access logs
        pass


def _get_access_token() -> Optional[str]:
    if _sp_ref is None:
        return None
    auth = _sp_ref.auth_manager
    tok = auth.get_cached_token()
    if tok and auth.is_token_expired(tok):
        tok = auth.refresh_access_token(tok['refresh_token'])
    return tok['access_token'] if tok else None


def _run_http():
    srv = HTTPServer(('localhost', PORT_HTTP), _PlayerHandler)
    srv.serve_forever()


# ─────────────────────────── WebSocket server ─────────────────────────────────

_event_queue: queue.Queue = queue.Queue()


async def _ws_handler(websocket):
    print("Browser connected via WebSocket")
    try:
        async for msg in websocket:
            try:
                _event_queue.put_nowait(json.loads(msg))
            except Exception:
                pass
    except websockets.exceptions.ConnectionClosed:
        pass


async def _ws_main():
    async with websockets.serve(_ws_handler, 'localhost', PORT_WS):
        await asyncio.Future()  # run forever


def _run_ws():
    asyncio.run(_ws_main())

# ─────────────────────────── drawing helpers ─────────────────────────────────

def lerp(a: float, b: float, t: float) -> float:
    return a + (b - a) * t


def lerp_color(c1, c2, t):
    return tuple(int(lerp(c1[i], c2[i], t)) for i in range(3))


def hsv_color(h: float, s: float, v: float) -> Tuple[int, int, int]:
    r, g, b = colorsys.hsv_to_rgb(h % 1.0, s, v)
    return int(r * 255), int(g * 255), int(b * 255)


def lane_x_perspective(lane: int, y: float, cam: CameraState) -> float:
    """Return screen x for a note at (lane, y) with perspective."""
    total_w = LANES * NOTE_W * (1.0 + 0.15 * cam.roll)
    lane_w = total_w / LANES
    # Normalise y: 0 at hit line, 1 at spawn
    t = (y - HIT_LINE_Y) / (HIGHWAY_TOP_Y - HIT_LINE_Y)
    t = max(0.0, min(1.0, t))
    horizon_x = WIDTH / 2
    spread = 1.0 - t * PERSPECTIVE_STRENGTH
    base_x = horizon_x - total_w / 2 + lane * lane_w + lane_w / 2
    return horizon_x + (base_x - horizon_x) * spread


def note_width_at(y: float) -> int:
    t = (y - HIT_LINE_Y) / (HIGHWAY_TOP_Y - HIT_LINE_Y)
    t = max(0.0, min(1.0, t))
    scale = 1.0 - t * PERSPECTIVE_STRENGTH
    return max(8, int(NOTE_W * scale))


def draw_highway(surf: pygame.Surface, cam: CameraState, hue_shift: float):
    """Draw the perspective lane stripes."""
    for lane in range(LANES):
        base_col = LANE_COLORS_BASE[lane]
        h, s, v = colorsys.rgb_to_hsv(*[c / 255 for c in base_col])
        col = hsv_color(h + hue_shift, s * 0.4, v * 0.18)
        col_bright = hsv_color(h + hue_shift, s * 0.6, v * 0.35)

        # Draw a trapezoid for the lane
        top_x = lane_x_perspective(lane, HIGHWAY_TOP_Y, cam)
        bot_x = lane_x_perspective(lane, HIT_LINE_Y, cam)
        half_top = note_width_at(HIGHWAY_TOP_Y) // 2
        half_bot = note_width_at(HIT_LINE_Y) // 2

        pts = [
            (top_x - half_top, HIGHWAY_TOP_Y),
            (top_x + half_top, HIGHWAY_TOP_Y),
            (bot_x + half_bot, HIT_LINE_Y),
            (bot_x - half_bot, HIT_LINE_Y),
        ]
        pygame.draw.polygon(surf, col, pts)
        pygame.draw.polygon(surf, col_bright, pts, 2)


def draw_hit_line(surf: pygame.Surface, cam: CameraState, tatum_flash: float, hue_shift: float):
    """Draw the glowing hit bar at the bottom."""
    alpha = int(180 + 75 * tatum_flash)
    for lane in range(LANES):
        h, s, v = colorsys.rgb_to_hsv(*[c / 255 for c in LANE_COLORS_BASE[lane]])
        col = hsv_color(h + hue_shift, s, v * (0.6 + 0.4 * tatum_flash))
        cx = lane_x_perspective(lane, HIT_LINE_Y, cam)
        hw = note_width_at(HIT_LINE_Y) // 2 + 4
        rect = pygame.Rect(cx - hw, HIT_LINE_Y - NOTE_H // 2, hw * 2, NOTE_H)
        # glow layers
        for glow in range(4, 0, -1):
            gr = pygame.Rect(rect.x - glow * 4, rect.y - glow * 2,
                             rect.width + glow * 8, rect.height + glow * 4)
            glow_surf = pygame.Surface((gr.width, gr.height), pygame.SRCALPHA)
            ga = max(0, min(255, int(40 * tatum_flash * glow)))
            glow_surf.fill((*col, ga))
            surf.blit(glow_surf, gr.topleft)
        pygame.draw.rect(surf, col, rect, border_radius=6)


def draw_note(surf: pygame.Surface, note: Note, cam: CameraState, hue_shift: float):
    cx = lane_x_perspective(note.lane, note.y, cam)
    nw = note_width_at(note.y)
    nh = max(6, int(NOTE_H * (nw / NOTE_W)))
    rect = pygame.Rect(cx - nw // 2, int(note.y) - nh // 2, nw, nh)

    h, s, v = colorsys.rgb_to_hsv(*[c / 255 for c in LANE_COLORS_BASE[note.lane]])
    col = hsv_color(h + hue_shift, s, v * (0.6 + 0.4 * note.loudness))
    white_mix = note.loudness * 0.5
    col = lerp_color(col, (255, 255, 255), white_mix)

    # glow
    for glow in range(3, 0, -1):
        gr = pygame.Rect(rect.x - glow * 3, rect.y - glow * 2,
                         rect.width + glow * 6, rect.height + glow * 4)
        gs = pygame.Surface((gr.width, gr.height), pygame.SRCALPHA)
        ga = max(0, int(50 * note.loudness / glow))
        gs.fill((*col, ga))
        surf.blit(gs, gr.topleft)

    pygame.draw.rect(surf, col, rect, border_radius=5)
    # specular stripe
    stripe = pygame.Rect(rect.x + 4, rect.y + 3, max(2, nw // 3), max(2, nh // 3))
    pygame.draw.rect(surf, (255, 255, 255, 120), stripe, border_radius=2)


def draw_flash(surf: pygame.Surface, flash: Flash):
    if flash.life <= 0:
        return
    r = int(flash.radius * (1.0 - flash.life * 0.3))
    a = int(255 * flash.life)
    s = pygame.Surface((r * 2, r * 2), pygame.SRCALPHA)
    pygame.draw.circle(s, (*flash.color, a), (r, r), r)
    surf.blit(s, (int(flash.x) - r, int(flash.y) - r))


def draw_hud(surf: pygame.Surface, font_big, font_small,
             track_name: str, artist: str, bpm: float,
             energy: float, valence: float, section_name: str,
             cam: CameraState):
    # Background panel
    panel = pygame.Surface((420, 110), pygame.SRCALPHA)
    panel.fill((0, 0, 0, 140))
    surf.blit(panel, (20, 10))

    col_title = hsv_color(cam.hue_shift, 0.6, 1.0)
    col_sub = (200, 200, 220)

    surf.blit(font_big.render(track_name[:36], True, col_title), (30, 16))
    surf.blit(font_small.render(artist[:48], True, col_sub), (30, 52))
    meta = f"♩ {bpm:.0f} BPM   ⚡ Energy {energy:.2f}   ☀ Valence {valence:.2f}"
    surf.blit(font_small.render(meta, True, col_sub), (30, 76))
    if section_name:
        surf.blit(font_small.render(f"§ {section_name}", True, (160, 160, 200)), (30, 98))


def draw_starfield(surf: pygame.Surface, stars: list, scroll: float, cam: CameraState):
    for (sx, sy, sz, sc) in stars:
        # parallax with camera roll
        px = (sx + scroll * 0.3 * (sz + 0.3)) % WIDTH
        py = sy
        brightness = min(255, int(120 + 135 * sz + 80 * cam.fov_pulse))
        col = (brightness, brightness, min(255, brightness + 40))
        pygame.draw.circle(surf, col, (int(px), int(py)), max(1, int(sz * 2.5)))

# ─────────────────────────── camera transitions ──────────────────────────────

# Each "section" gets a camera preset; we cycle / pick by index mod len
CAMERA_PRESETS = [
    CameraState(tilt=0.0,  roll=0.0,  fov_pulse=0.0, hue_shift=0.00, bg_brightness=0.0),
    CameraState(tilt=5.0,  roll=0.3,  fov_pulse=0.1, hue_shift=0.08, bg_brightness=0.05),
    CameraState(tilt=-4.0, roll=-0.2, fov_pulse=0.0, hue_shift=0.20, bg_brightness=0.08),
    CameraState(tilt=3.0,  roll=0.5,  fov_pulse=0.2, hue_shift=0.50, bg_brightness=0.04),
    CameraState(tilt=-6.0, roll=0.0,  fov_pulse=0.3, hue_shift=0.65, bg_brightness=0.10),
    CameraState(tilt=2.0,  roll=-0.4, fov_pulse=0.0, hue_shift=0.85, bg_brightness=0.02),
]


def lerp_camera(src: CameraState, dst: CameraState, t: float) -> CameraState:
    return CameraState(
        tilt=lerp(src.tilt, dst.tilt, t),
        roll=lerp(src.roll, dst.roll, t),
        fov_pulse=lerp(src.fov_pulse, dst.fov_pulse, t),
        hue_shift=lerp(src.hue_shift, dst.hue_shift, t),
        bg_brightness=lerp(src.bg_brightness, dst.bg_brightness, t),
    )

# ─────────────────────────── main ────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(description="Guitar Hero Spotify Visualizer")
    args = parser.parse_args()

    # ── Auth ───────────────────────────────────────────────────────────────
    print("Connecting to Spotify…")
    global _sp_ref
    _sp_ref = build_spotify()
    sp = _sp_ref

    # Get current track for initial HUD (before browser takes over playback)
    track_name = "Waiting for playback…"
    artist     = ""
    duration_ms = 0
    bpm_estimate = 120.0
    current_energy = 0.3

    pb = sp.current_playback()
    if pb and pb.get("item"):
        t = pb["item"]
        track_name  = t["name"]
        artist      = ", ".join(a["name"] for a in t["artists"])
        duration_ms = t["duration_ms"]

    # ── Start background servers ───────────────────────────────────────────
    threading.Thread(target=_run_http, daemon=True).start()
    threading.Thread(target=_run_ws,   daemon=True).start()
    time.sleep(0.3)   # let servers bind

    player_url = f"http://localhost:{PORT_HTTP}/"
    print(f"Opening player at {player_url}")
    print("A browser tab will open — log in to Spotify if prompted.")
    print("Press Esc / Q in the pygame window to quit.\n")
    webbrowser.open(player_url)

    # ── pygame setup ───────────────────────────────────────────────────────
    pygame.init()
    screen = pygame.display.set_mode((WIDTH, HEIGHT))
    pygame.display.set_caption("♪  Goontunes Visualizer")
    clock = pygame.time.Clock()

    font_big   = pygame.font.SysFont("sans", 26, bold=True)
    font_small = pygame.font.SysFont("sans", 18)

    random.seed(42)
    stars = [(random.uniform(0, WIDTH), random.uniform(0, HEIGHT),
              random.uniform(0.1, 1.0), random.uniform(0, 1)) for _ in range(200)]
    star_scroll = 0.0

    notes_on_screen: List[Note] = []
    flashes:         List[Flash] = []
    used_lanes:      List[int]  = []

    # Beat timing state
    beat_times:       List[float] = []   # wall-clock times of recent beats
    next_note_spawn   = time.perf_counter()  # next predicted beat (for pre-spawn)

    # Camera
    current_cam        = CameraState()
    target_cam         = CameraState()
    section_timer      = 0.0
    section_idx        = 0
    section_label      = ""

    tatum_flash = 0.0
    bar_pulse   = 0.0

    # Playback progress tracking
    position_ms  = 0
    position_ref = time.perf_counter()
    paused       = False

    def transfer_playback(device_id: str):
        try:
            sp.transfer_playback(device_id, force_play=True)
            print(f"Transferred playback to browser (device {device_id})")
        except Exception as e:
            print(f"transfer_playback failed: {e}")

    running = True
    while running:
        dt = clock.tick(FPS) / 1000.0
        now_wall = time.perf_counter()

        # ── pygame events ─────────────────────────────────────────────────
        for ev in pygame.event.get():
            if ev.type == pygame.QUIT:
                running = False
            elif ev.type == pygame.KEYDOWN and ev.key in (pygame.K_ESCAPE, pygame.K_q):
                running = False

        # ── WebSocket events ──────────────────────────────────────────────
        while not _event_queue.empty():
            msg = _event_queue.get_nowait()
            mtype = msg.get("type")

            if mtype == "ready":
                threading.Thread(target=transfer_playback,
                                 args=(msg["device_id"],), daemon=True).start()

            elif mtype == "state":
                track_name  = msg.get("track_name", track_name) or track_name
                artist      = msg.get("artist",     artist)     or artist
                duration_ms = msg.get("duration",   duration_ms) or duration_ms
                position_ms = msg.get("position",   0)
                position_ref = now_wall
                paused       = msg.get("paused", False)
                pygame.display.set_caption(f"♪  {track_name}")

            elif mtype == "beat":
                bass = msg.get("bass", 0.5)
                current_energy = msg.get("energy", current_energy)

                # Update BPM estimate from inter-beat intervals
                beat_times.append(now_wall)
                if len(beat_times) > 10:
                    beat_times.pop(0)
                if len(beat_times) >= 4:
                    intervals = [beat_times[i+1] - beat_times[i]
                                 for i in range(len(beat_times) - 1)]
                    avg = sum(intervals) / len(intervals)
                    new_bpm = 60.0 / avg
                    bpm_estimate = max(40.0, min(220.0, new_bpm))

                # Re-align predicted spawn timer to actual beat
                beat_interval = 60.0 / bpm_estimate
                next_note_spawn = now_wall + beat_interval

                # Immediate hit flash on all lanes (one random lane highlighted)
                hit_lane = _spread_lane(used_lanes)
                cx = lane_x_perspective(hit_lane, HIT_LINE_Y, current_cam)
                h, s, v = colorsys.rgb_to_hsv(*[c / 255 for c in LANE_COLORS_BASE[hit_lane]])
                col = hsv_color(h + current_cam.hue_shift, s, v)
                flashes.append(Flash(x=cx, y=HIT_LINE_Y,
                                     radius=NOTE_W * (1.5 + bass),
                                     color=col, life=1.0))
                tatum_flash = min(1.0, tatum_flash + bass)
                bar_pulse   = min(1.0, bar_pulse   + bass * 0.5)

            elif mtype == "energy":
                current_energy = max(msg.get("rms", 0), msg.get("bass", 0))

        # ── Pre-spawn notes based on BPM prediction ───────────────────────
        beat_interval = 60.0 / max(bpm_estimate, 40.0)
        travel_time   = (HIT_LINE_Y - HIGHWAY_TOP_Y) / NOTE_SPEED
        while not paused and now_wall >= next_note_spawn - travel_time:
            lane = _spread_lane(used_lanes)
            used_lanes.append(lane)
            if len(used_lanes) > 6:
                used_lanes.pop(0)
            notes_on_screen.append(Note(
                lane=lane,
                spawn_y=float(HIGHWAY_TOP_Y),
                y=float(HIGHWAY_TOP_Y),
                loudness=min(1.0, current_energy * 2.5),
            ))
            next_note_spawn += beat_interval

        # ── Camera: section transitions every 30 s ────────────────────────
        section_timer += dt
        if section_timer >= 30.0:
            section_timer = 0.0
            section_idx = (section_idx + 1) % len(CAMERA_PRESETS)
            target_cam   = CAMERA_PRESETS[section_idx]
            section_label = f"Section {section_idx + 1}"

        cam_t = min(1.0, CAMERA_TRANSITION_SPEED * dt)
        current_cam = lerp_camera(current_cam, target_cam, cam_t)

        tatum_flash = max(0.0, tatum_flash - dt * 6.0)
        bar_pulse   = max(0.0, bar_pulse   - dt * 4.0)
        current_cam.fov_pulse = lerp(current_cam.fov_pulse,
                                     target_cam.fov_pulse + bar_pulse * 0.15,
                                     0.3)

        # ── Move & hit notes ──────────────────────────────────────────────
        for note in notes_on_screen:
            note.y += NOTE_SPEED * dt
            if not note.hit and note.y >= HIT_LINE_Y - NOTE_H:
                note.hit = True
                cx = lane_x_perspective(note.lane, HIT_LINE_Y, current_cam)
                h, s, v = colorsys.rgb_to_hsv(*[c / 255 for c in LANE_COLORS_BASE[note.lane]])
                col = hsv_color(h + current_cam.hue_shift, s, v)
                flashes.append(Flash(x=cx, y=HIT_LINE_Y,
                                     radius=NOTE_W * 1.2, color=col, life=0.7))

        for note in notes_on_screen:
            if note.hit:
                note.alpha = max(0, note.alpha - int(255 * dt * 5))

        notes_on_screen = [n for n in notes_on_screen
                           if n.y < HEIGHT + 40 and n.alpha > 0]

        for f in flashes:
            f.life -= dt * 3.5
        flashes = [f for f in flashes if f.life > 0]

        # ── Draw ──────────────────────────────────────────────────────────
        bg_bright = int(BG_BASE[2] + current_cam.bg_brightness * 40 + bar_pulse * 20)
        bg = (BG_BASE[0], BG_BASE[1], min(255, bg_bright))
        screen.fill(bg)

        star_scroll += 30 * dt
        draw_starfield(screen, stars, star_scroll, current_cam)

        for yy in range(HIGHWAY_TOP_Y, HIGHWAY_TOP_Y + 60):
            a = int(80 * (1.0 - (yy - HIGHWAY_TOP_Y) / 60))
            pygame.draw.line(screen, (bg[0], bg[1], bg[2] + a), (0, yy), (WIDTH, yy))

        draw_highway(screen, current_cam, current_cam.hue_shift)
        draw_hit_line(screen, current_cam, tatum_flash, current_cam.hue_shift)

        for note in sorted(notes_on_screen, key=lambda n: n.y):
            draw_note(screen, note, current_cam, current_cam.hue_shift)

        for flash in flashes:
            draw_flash(screen, flash)

        # Progress
        if duration_ms > 0:
            elapsed_ms = position_ms + (now_wall - position_ref) * 1000
            prog = max(0.0, min(1.0, elapsed_ms / duration_ms))
        else:
            prog = 0.0

        draw_hud(screen, font_big, font_small,
                 track_name, artist, bpm_estimate, current_energy, 0.0,
                 section_label, current_cam)

        bar_rect = pygame.Rect(0, HEIGHT - 5, int(WIDTH * prog), 5)
        pygame.draw.rect(screen, hsv_color(current_cam.hue_shift + 0.5, 0.9, 1.0), bar_rect)

        pygame.display.flip()

    pygame.quit()
    print("Done.")


if __name__ == "__main__":
    main()
