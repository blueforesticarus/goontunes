#!/usr/bin/env python3
"""
Fetch all liked tracks from your Spotify account, find the ones that are
unavailable (deleted / geo-restricted), and write an HTML report sorted by
date added.

Requires env.py (or SPOTIFY_* env vars) with client_id, client_secret,
and redirect_uri = http://127.0.0.1:12996.
"""

import base64
import hashlib
import importlib.util
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

_here = Path(__file__).parent
TOKEN_CACHE = _here / ".cache" / "spotify_token.json"
OUTPUT_HTML = _here / "unavailable_liked.html"

SCOPES = "user-library-read"


# ---------------------------------------------------------------------------
# OAuth / token helpers (self-contained)
# ---------------------------------------------------------------------------

def _parse_port(redirect_uri: str) -> int:
    return urllib.parse.urlparse(redirect_uri).port or 80


class _CallbackHandler(BaseHTTPRequestHandler):
    code: Optional[str] = None

    def do_GET(self) -> None:  # noqa: N802
        params = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        _CallbackHandler.code = params.get("code", [None])[0]
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"<h2>Authenticated! You can close this tab.</h2>")

    def log_message(self, *_):
        pass


def _authorize(client_id: str, redirect_uri: str) -> dict:
    verifier = secrets.token_urlsafe(64)
    digest = hashlib.sha256(verifier.encode()).digest()
    challenge = base64.urlsafe_b64encode(digest).rstrip(b"=").decode()

    auth_url = (
        "https://accounts.spotify.com/authorize?"
        + urllib.parse.urlencode({
            "client_id": client_id,
            "response_type": "code",
            "redirect_uri": redirect_uri,
            "scope": SCOPES,
            "state": secrets.token_hex(8),
            "code_challenge_method": "S256",
            "code_challenge": challenge,
        })
    )

    port = _parse_port(redirect_uri)
    print(f"Opening browser for Spotify login … (listening on port {port})")
    webbrowser.open(auth_url)

    server = HTTPServer(("127.0.0.1", port), _CallbackHandler)
    _CallbackHandler.code = None
    while _CallbackHandler.code is None:
        server.handle_request()
    server.server_close()

    resp = requests.post(
        "https://accounts.spotify.com/api/token",
        data={
            "grant_type": "authorization_code",
            "code": _CallbackHandler.code,
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


def get_access_token(client_id: str, redirect_uri: str) -> str:
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
        token = _authorize(client_id, redirect_uri)

    TOKEN_CACHE.parent.mkdir(parents=True, exist_ok=True)
    TOKEN_CACHE.write_text(json.dumps(token))
    return token["access_token"]


# ---------------------------------------------------------------------------
# Spotify helpers
# ---------------------------------------------------------------------------

def fetch_liked_tracks(token: str) -> list[dict]:
    """Page through /v1/me/tracks and return every saved track object."""
    headers = {"Authorization": f"Bearer {token}"}
    tracks: list[dict] = []
    url = "https://api.spotify.com/v1/me/tracks"
    params: dict = {"limit": 50, "market": "from_token"}

    while url:
        resp = requests.get(url, headers=headers, params=params, timeout=15)
        if resp.status_code == 401:
            raise RuntimeError("Token expired while fetching liked tracks")
        resp.raise_for_status()
        page = resp.json()
        tracks.extend(page["items"])
        url = page.get("next")
        params = {}  # next URL already contains query params
        time.sleep(0.05)

    return tracks


def is_unavailable(track: dict) -> bool:
    """Return True if the track object signals it cannot be played."""
    if track is None:
        return True
    # Null-ed out by API
    if not track.get("id"):
        return True
    # Explicit restriction object
    if track.get("restrictions"):
        return True
    # is_playable is set when market=from_token is passed
    if "is_playable" in track and not track["is_playable"]:
        return True
    return False


# ---------------------------------------------------------------------------
# HTML report
# ---------------------------------------------------------------------------

_HTML_TEMPLATE = """\
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Unavailable liked tracks</title>
<style>
  body {{ font-family: system-ui, sans-serif; background: #121212; color: #e0e0e0; padding: 2rem; }}
  h1   {{ color: #1db954; }}
  p.meta {{ color: #aaa; margin-top: -.5rem; }}
  table {{ border-collapse: collapse; width: 100%; margin-top: 1.5rem; }}
  th    {{ background: #1db954; color: #000; text-align: left; padding: .5rem .75rem; position: sticky; top: 0; }}
  td    {{ padding: .45rem .75rem; border-bottom: 1px solid #2a2a2a; vertical-align: top; }}
  tr:hover td {{ background: #1a1a1a; }}
  a    {{ color: #1db954; text-decoration: none; }}
  a:hover {{ text-decoration: underline; }}
  .reason {{ font-size: .8em; color: #ff6b6b; }}
  .art    {{ width: 48px; height: 48px; object-fit: cover; border-radius: 4px; display: block; }}
  .album-cell {{ display: flex; align-items: center; gap: .6rem; }}
</style>
</head>
<body>
<h1>Unavailable liked tracks</h1>
<p class="meta">Generated {date} &mdash; {count} unavailable out of {total} liked tracks</p>
<table>
  <thead>
    <tr>
      <th>#</th>
      <th>Date added</th>
      <th>Track</th>
      <th>Artist(s)</th>
      <th>Album</th>
      <th>Art</th>
      <th>Reason</th>
    </tr>
  </thead>
  <tbody>
{rows}
  </tbody>
</table>
</body>
</html>
"""

_ROW_TEMPLATE = """\
    <tr>
      <td>{n}</td>
      <td>{added}</td>
      <td><a href="{track_url}" target="_blank">{track_name}</a></td>
      <td>{artists}</td>
      <td><a href="{album_url}" target="_blank" class="album-cell"><img class="art" src="{album_art}" alt="" loading="lazy">{album_name}</a></td>
      <td class="reason">{reason}</td>
    </tr>"""


def build_html(items: list[dict], total: int) -> str:
    """
    items: list of saved-track objects (already filtered to unavailable),
           sorted by added_at ascending.
    """
    from datetime import datetime, timezone

    rows = []
    for n, item in enumerate(items, 1):
        added_raw: str = item.get("added_at", "")
        try:
            added = datetime.fromisoformat(added_raw.replace("Z", "+00:00")).strftime("%Y-%m-%d")
        except Exception:
            added = added_raw

        track = item.get("track") or {}
        track_name = track.get("name", "?")
        track_url = (track.get("external_urls") or {}).get("spotify", "#")

        artists = ", ".join(
            f'<a href="{a.get("external_urls", {}).get("spotify", "#")}" target="_blank">{a.get("name", "?")}</a>'
            for a in track.get("artists", [])
        )

        album = track.get("album") or {}
        album_name = album.get("name", "?")
        album_url = (album.get("external_urls") or {}).get("spotify", "#")
        # Pick smallest image >= 48px (last in list is usually 64px)
        images = album.get("images") or []
        album_art = next(
            (img["url"] for img in reversed(images) if img.get("width", 0) >= 48),
            images[0]["url"] if images else "",
        )

        restrictions = track.get("restrictions") or {}
        reason = restrictions.get("reason", "unavailable")

        rows.append(_ROW_TEMPLATE.format(
            n=n,
            added=added,
            track_name=track_name,
            track_url=track_url,
            artists=artists,
            album_art=album_art,
            album_name=album_name,
            album_url=album_url,
            reason=reason,
        ))

    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    return _HTML_TEMPLATE.format(
        date=now,
        count=len(items),
        total=total,
        rows="\n".join(rows),
    )


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def load_credentials() -> tuple[str, str, str]:
    client_id = os.environ.get("SPOTIFY_CLIENT_ID")
    client_secret = os.environ.get("SPOTIFY_CLIENT_SECRET")
    redirect_uri = os.environ.get("SPOTIFY_REDIRECT_URI", "http://127.0.0.1:12996")

    env_file = Path.cwd() / "env.py"
    if not (client_id and client_secret) and env_file.exists():
        spec = importlib.util.spec_from_file_location("env", env_file)
        env = importlib.util.module_from_spec(spec)  # type: ignore
        spec.loader.exec_module(env)  # type: ignore
        client_id = client_id or getattr(env, "client_id", None)
        client_secret = client_secret or getattr(env, "client_secret", None)
        redirect_uri = redirect_uri or getattr(env, "redirect_uri", "http://127.0.0.1:12996")

    if not client_id or not client_secret:
        sys.exit(
            "Spotify credentials not found.\n"
            "Set SPOTIFY_CLIENT_ID / SPOTIFY_CLIENT_SECRET env vars or create env.py."
        )
    return client_id, client_secret, redirect_uri  # type: ignore


def main() -> None:
    client_id, _client_secret, redirect_uri = load_credentials()

    print("Authenticating …")
    token = get_access_token(client_id, redirect_uri)

    print("Fetching liked tracks (this may take a while for large libraries) …")
    saved = fetch_liked_tracks(token)
    total = len(saved)
    print(f"  {total} liked tracks fetched.")

    unavailable = [item for item in saved if is_unavailable(item.get("track"))]
    # Sort by date added (oldest first)
    unavailable.sort(key=lambda x: x.get("added_at", ""))

    print(f"  {len(unavailable)} unavailable.")

    html = build_html(unavailable, total)
    OUTPUT_HTML.write_text(html, encoding="utf-8")
    print(f"\nReport written to: {OUTPUT_HTML}")


if __name__ == "__main__":
    main()
