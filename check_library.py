#!/usr/bin/env python3
"""
Check all Spotify tracks in the .cache/library file to see if they still exist.
Uses the Authorization Code flow so the check runs in the context of your
Spotify account (respects your region's availability).

Credentials are read from env.py (copy env.py.template -> env.py and fill in).
The redirect_uri in env.py must match what's registered in your Spotify app
(default: http://127.0.0.1:12996).
"""

import hashlib
import json
import os
import secrets
import sys
import time
import urllib.parse
import webbrowser
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from typing import Optional

import requests

CACHE_PATH = Path(__file__).parent / ".cache" / "library"
TOKEN_CACHE = Path(__file__).parent / ".cache" / "spotify_token.json"
BATCH_SIZE = 50  # Spotify allows up to 50 track IDs per request
SCOPES = ""  # no special scopes needed – we only read public track data


# ---------------------------------------------------------------------------
# OAuth helpers
# ---------------------------------------------------------------------------

def _parse_port(redirect_uri: str) -> int:
    parsed = urllib.parse.urlparse(redirect_uri)
    return parsed.port or 80


class _CallbackHandler(BaseHTTPRequestHandler):
    """Minimal HTTP handler that captures the ?code= callback."""

    code: Optional[str] = None

    def do_GET(self) -> None:  # noqa: N802
        params = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        _CallbackHandler.code = params.get("code", [None])[0]
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"<h2>Authenticated! You can close this tab.</h2>")

    def log_message(self, *_):  # silence access log
        pass


def _authorize(client_id: str, client_secret: str, redirect_uri: str) -> dict:
    """Run the Authorization Code + PKCE flow and return the token response."""
    # PKCE
    verifier = secrets.token_urlsafe(64)
    digest = hashlib.sha256(verifier.encode()).digest()
    import base64
    challenge = base64.urlsafe_b64encode(digest).rstrip(b"=").decode()

    state = secrets.token_hex(8)
    auth_url = (
        "https://accounts.spotify.com/authorize?"
        + urllib.parse.urlencode(
            {
                "client_id": client_id,
                "response_type": "code",
                "redirect_uri": redirect_uri,
                "scope": SCOPES,
                "state": state,
                "code_challenge_method": "S256",
                "code_challenge": challenge,
            }
        )
    )

    port = _parse_port(redirect_uri)
    print(f"Opening browser for Spotify login … (listening on port {port})")
    webbrowser.open(auth_url)

    server = HTTPServer(("127.0.0.1", port), _CallbackHandler)
    _CallbackHandler.code = None
    while _CallbackHandler.code is None:
        server.handle_request()
    server.server_close()

    code = _CallbackHandler.code
    resp = requests.post(
        "https://accounts.spotify.com/api/token",
        data={
            "grant_type": "authorization_code",
            "code": code,
            "redirect_uri": redirect_uri,
            "client_id": client_id,
            "code_verifier": verifier,
        },
        timeout=10,
    )
    resp.raise_for_status()
    token = resp.json()
    token["expires_at"] = time.time() + token["expires_in"] - 30
    return token


def _refresh(token: dict, client_id: str) -> dict:
    resp = requests.post(
        "https://accounts.spotify.com/api/token",
        data={
            "grant_type": "refresh_token",
            "refresh_token": token["refresh_token"],
            "client_id": client_id,
        },
        timeout=10,
    )
    resp.raise_for_status()
    new = resp.json()
    new.setdefault("refresh_token", token["refresh_token"])
    new["expires_at"] = time.time() + new["expires_in"] - 30
    return new


def get_access_token(client_id: str, client_secret: str, redirect_uri: str) -> str:
    """Return a valid access token, reusing / refreshing a cached one if possible."""
    token: Optional[dict] = None

    if TOKEN_CACHE.exists():
        try:
            token = json.loads(TOKEN_CACHE.read_text())
        except Exception:
            token = None

    if token and time.time() < token.get("expires_at", 0):
        return token["access_token"]

    if token and token.get("refresh_token"):
        print("Refreshing Spotify access token …")
        try:
            token = _refresh(token, client_id)
        except Exception as e:
            print(f"Refresh failed ({e}), re-authorizing …")
            token = None

    if token is None:
        token = _authorize(client_id, client_secret, redirect_uri)

    TOKEN_CACHE.parent.mkdir(parents=True, exist_ok=True)
    TOKEN_CACHE.write_text(json.dumps(token))
    return token["access_token"]


def check_tracks(track_ids: list[str], token: str) -> dict[str, Optional[dict]]:
    """
    Check a list of track IDs. Returns a dict mapping id -> track object (or None if deleted/unavailable).
    """
    results: dict[str, Optional[dict]] = {}
    headers = {"Authorization": f"Bearer {token}"}

    for i in range(0, len(track_ids), BATCH_SIZE):
        batch = track_ids[i : i + BATCH_SIZE]
        resp = requests.get(
            "https://api.spotify.com/v1/tracks",
            params={"ids": ",".join(batch)},
            headers=headers,
            timeout=15,
        )
        if resp.status_code == 401:
            raise RuntimeError("Access token expired mid-run")
        resp.raise_for_status()
        for track_id, track in zip(batch, resp.json()["tracks"]):
            results[track_id] = track  # None if Spotify returns null for that slot
        # Respect rate limits – Spotify recommends staying under ~10 req/s
        time.sleep(0.1)

    return results


def main() -> None:
    # --- load credentials ---
    # Support env vars or env.py in the current working directory
    client_id = os.environ.get("SPOTIFY_CLIENT_ID")
    client_secret = os.environ.get("SPOTIFY_CLIENT_SECRET")
    redirect_uri = os.environ.get("SPOTIFY_REDIRECT_URI", "http://127.0.0.1:12996")

    env_file = Path.cwd() / "env.py"
    if not (client_id and client_secret) and env_file.exists():
        import importlib.util
        spec = importlib.util.spec_from_file_location("env", env_file)
        env = importlib.util.module_from_spec(spec)  # type: ignore
        spec.loader.exec_module(env)  # type: ignore
        client_id = client_id or getattr(env, "client_id", None)
        client_secret = client_secret or getattr(env, "client_secret", None)
        redirect_uri = redirect_uri or getattr(env, "redirect_uri", "http://127.0.0.1:12996")

    if not client_id or not client_secret:
        sys.exit(
            "Spotify credentials not found.\n"
            "Either set SPOTIFY_CLIENT_ID / SPOTIFY_CLIENT_SECRET env vars,\n"
            "or create env.py in the current directory (copy from env.py.template)."
        )

    # --- load cache ---
    print(f"Loading cache from {CACHE_PATH} …")
    with CACHE_PATH.open() as f:
        data = json.load(f)

    tracks = data.get("Tracks", [])
    print(f"Total tracks in cache: {len(tracks)}")

    # Extract Spotify track IDs (skip entries without one)
    id_to_cache: dict[str, dict] = {}
    for t in tracks:
        info = t.get("SpotifyInfo", {})
        if info.get("Initialized") and info.get("V"):
            spotify_id: Optional[str] = info["V"].get("id")
            if spotify_id:
                id_to_cache[spotify_id] = t

    print(f"Tracks with Spotify IDs: {len(id_to_cache)}")
    if not id_to_cache:
        print("Nothing to check.")
        return

    # --- authenticate ---
    token = get_access_token(client_id, client_secret, redirect_uri)

    # --- check in batches ---
    track_ids = list(id_to_cache.keys())
    print(f"Checking {len(track_ids)} tracks in batches of {BATCH_SIZE} …")
    results = check_tracks(track_ids, token)

    # --- report ---
    missing: list[str] = []
    unavailable: list[str] = []  # present but no available markets
    ok: int = 0

    for tid, track_obj in results.items():
        cached_name = id_to_cache[tid].get("SpotifyInfo", {}).get("V", {}).get("name", "?")
        cached_artists = ", ".join(
            a.get("name", "?")
            for a in id_to_cache[tid].get("SpotifyInfo", {}).get("V", {}).get("artists", [])
        )

        if track_obj is None:
            missing.append(f"  {tid}  {cached_artists} – {cached_name}")
        elif not track_obj.get("is_playable", True) and track_obj.get("restrictions"):
            unavailable.append(
                f"  {tid}  {cached_artists} – {cached_name}  "
                f"[reason: {track_obj['restrictions'].get('reason', '?')}]"
            )
        else:
            ok += 1

    print()
    print(f"✓  Still available:  {ok}")
    print(f"✗  Deleted/missing:  {len(missing)}")
    print(f"⚠  Restricted:       {len(unavailable)}")

    if missing:
        print("\n=== MISSING (null from API) ===")
        print("\n".join(missing))

    if unavailable:
        print("\n=== RESTRICTED ===")
        print("\n".join(unavailable))


if __name__ == "__main__":
    main()
