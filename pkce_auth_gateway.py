#!/usr/bin/env python3
"""
M365 Copilot browser PKCE auth gateway (NO device code).

Uses the Office web Copilot first-party client from m365-copilot-proxy:
  client_id: c0ab8ce9-e9a0-42e7-b064-33d422df41f1
  redirect:  https://login.microsoftonline.com/common/oauth2/nativeclient
  scopes:    substrate.office.com/sydney M365Chat.Read + sydney.readwrite

Flow:
  1) Gateway prints/serves a login URL
  2) User signs in with a real browser
  3) Browser lands on nativeclient URL containing ?code=...
     (often briefly, then bounces to /common/wrongplace)
  4) User pastes the callback URL into the gateway page / CLI
     OR posts it to /callback?url=...
  5) Gateway exchanges code for tokens and saves them

Also supports:
  - CLI: python pkce_auth_gateway.py --serve
  - CLI: python pkce_auth_gateway.py --exchange 'https://login.microsoftonline.com/common/oauth2/nativeclient?code=...'
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import secrets
import sys
import threading
import time
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any, Optional

# Office web Copilot client (m365-copilot-proxy)
CLIENT_ID = "c0ab8ce9-e9a0-42e7-b064-33d422df41f1"
AUTHORITY = "https://login.microsoftonline.com/common"
REDIRECT_URI = "https://login.microsoftonline.com/common/oauth2/nativeclient"
TOKEN_URL = f"{AUTHORITY}/oauth2/v2.0/token"
AUTHORIZE_URL = f"{AUTHORITY}/oauth2/v2.0/authorize"
SCOPES = [
    "https://substrate.office.com/sydney/M365Chat.Read",
    "https://substrate.office.com/sydney/sydney.readwrite",
    "offline_access",
    "openid",
    "profile",
]

DEFAULT_STATE_DIR = Path(os.environ.get("M365_STATE_DIR", "/tmp/m365-pkce"))
TOKEN_FILE = Path(os.path.expanduser(os.environ.get("M365_TOKEN_FILE", "~/.config/m365-native/accounts.json")))
DEFAULT_HOST = os.environ.get("M365_AUTH_HOST", "0.0.0.0")
DEFAULT_PORT = int(os.environ.get("M365_AUTH_PORT", "8765"))


def b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def decode_jwt_payload(token: str) -> dict[str, Any]:
    part = token.split(".")[1]
    pad = "=" * ((4 - len(part) % 4) % 4)
    return json.loads(base64.urlsafe_b64decode(part + pad))


def pkce_pair() -> tuple[str, str]:
    verifier = b64url(secrets.token_bytes(64))
    challenge = b64url(hashlib.sha256(verifier.encode("ascii")).digest())
    return verifier, challenge


class AuthSession:
    def __init__(self) -> None:
        self.lock = threading.Lock()
        self.reset()

    def reset(self) -> None:
        verifier, challenge = pkce_pair()
        self.verifier = verifier
        self.challenge = challenge
        self.state = secrets.token_urlsafe(24)
        self.created_at = time.time()
        self.auth_url = self._build_auth_url()
        self.result: Optional[dict[str, Any]] = None
        self.error: Optional[str] = None
        self.done = threading.Event()

    def _build_auth_url(self) -> str:
        q = {
            "client_id": CLIENT_ID,
            "response_type": "code",
            "redirect_uri": REDIRECT_URI,
            "response_mode": "query",
            "scope": " ".join(SCOPES),
            "state": self.state,
            "code_challenge": self.challenge,
            "code_challenge_method": "S256",
            "prompt": "select_account",
        }
        return f"{AUTHORIZE_URL}?{urllib.parse.urlencode(q)}"

    def status(self) -> dict[str, Any]:
        with self.lock:
            return {
                "ready": True,
                "client_id": CLIENT_ID,
                "redirect_uri": REDIRECT_URI,
                "scopes": SCOPES,
                "state": self.state,
                "auth_url": self.auth_url,
                "created_at": self.created_at,
                "completed": self.result is not None,
                "error": self.error,
                "result_summary": None
                if not self.result
                else {
                    "email": self.result.get("email"),
                    "appid": self.result.get("appid"),
                    "aud": self.result.get("aud"),
                    "expires_at": self.result.get("expiresAt"),
                    "token_file": str(TOKEN_FILE),
                },
            }


SESSION = AuthSession()


def extract_code_from_callback(callback: str) -> tuple[str, Optional[str]]:
    """Accept either a full URL or a bare code."""
    callback = callback.strip().strip('"').strip("'")
    if not callback:
        raise ValueError("empty callback")

    # bare code
    if "://" not in callback and "code=" not in callback and "&" not in callback:
        return callback, None

    # maybe user pasted only query string
    if callback.startswith("?"):
        callback = REDIRECT_URI + callback
    elif callback.startswith("code="):
        callback = REDIRECT_URI + "?" + callback

    # sometimes users paste wrongplace URL; try to recover if code still present
    parsed = urllib.parse.urlparse(callback)
    qs = urllib.parse.parse_qs(parsed.query)
    if "code" not in qs and "#" in callback:
        # fragment form
        frag = urllib.parse.parse_qs(parsed.fragment)
        qs = frag

    if "code" not in qs:
        raise ValueError(
            "callback URL has no code= parameter. "
            "Copy the URL that contains oauth2/nativeclient?code=... "
            "(it may flash before /common/wrongplace)."
        )
    code = qs["code"][0]
    state = qs.get("state", [None])[0]
    return code, state


def exchange_code(code: str, verifier: str) -> dict[str, Any]:
    data = urllib.parse.urlencode(
        {
            "client_id": CLIENT_ID,
            "grant_type": "authorization_code",
            "code": code,
            "redirect_uri": REDIRECT_URI,
            "code_verifier": verifier,
            "scope": " ".join(SCOPES),
        }
    ).encode("utf-8")
    req = urllib.request.Request(
        TOKEN_URL,
        data=data,
        headers={
            # Native/public client PKCE exchange — do NOT send Origin,
            # or AAD returns AADSTS9002326 (SPA-only cross-origin redemption).
            "Content-Type": "application/x-www-form-urlencoded",
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
            "Accept": "application/json",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as e:
        err_body = e.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"token exchange HTTP {e.code}: {err_body}") from e

    if "access_token" not in body:
        raise RuntimeError(f"token response missing access_token: {body}")
    return body


def persist_tokens(token_response: dict[str, Any], token_file: Path = TOKEN_FILE) -> dict[str, Any]:
    access = token_response["access_token"]
    claims = decode_jwt_payload(access)
    expires_in = int(token_response.get("expires_in", 3600))
    expires_at = datetime.fromtimestamp(time.time() + expires_in, tz=timezone.utc).isoformat().replace("+00:00", "Z")

    account = {
        "id": claims.get("oid") or claims.get("sub"),
        "email": claims.get("preferred_username") or claims.get("upn") or claims.get("unique_name") or claims.get("email"),
        "displayName": claims.get("name"),
        "status": "online",
        "accessToken": access,
        "refreshToken": token_response.get("refresh_token"),
        "idToken": token_response.get("id_token"),
        "expiresAt": expires_at,
        "updatedAt": now_iso(),
        "clientId": CLIENT_ID,
        "scopes": SCOPES,
        "aud": claims.get("aud"),
        "appid": claims.get("appid") or claims.get("azp"),
        "tid": claims.get("tid"),
        "oid": claims.get("oid"),
        "rawTokenResponseKeys": sorted(token_response.keys()),
    }

    payload = {
        "source": "pkce-browser-gateway",
        "clientId": CLIENT_ID,
        "redirectUri": REDIRECT_URI,
        "updatedAt": now_iso(),
        "accounts": [account],
    }

    token_file.parent.mkdir(parents=True, exist_ok=True)
    token_file.write_text(json.dumps(payload, indent=2), encoding="utf-8")
    # also mirror to the path our earlier scripts used, if different
    mirror = Path("/tmp/m365-native-tokens.json")
    if token_file.resolve() != mirror.resolve():
        try:
            mirror.write_text(json.dumps(payload, indent=2), encoding="utf-8")
        except Exception:
            pass

    # keep session state for debugging
    DEFAULT_STATE_DIR.mkdir(parents=True, exist_ok=True)
    (DEFAULT_STATE_DIR / "last-result.json").write_text(
        json.dumps(
            {
                "email": account["email"],
                "appid": account["appid"],
                "aud": account["aud"],
                "oid": account["oid"],
                "tid": account["tid"],
                "expiresAt": account["expiresAt"],
                "tokenFile": str(token_file),
            },
            indent=2,
        ),
        encoding="utf-8",
    )
    return account


def complete_with_callback(callback: str, expect_state: Optional[str] = None) -> dict[str, Any]:
    code, state = extract_code_from_callback(callback)
    with SESSION.lock:
        verifier = SESSION.verifier
        current_state = SESSION.state
    if expect_state is None:
        expect_state = current_state
    if state and expect_state and state != expect_state:
        # soft warning only; some pastes may lose state
        print(f"[warn] state mismatch: got={state} expected={expect_state}", file=sys.stderr)

    token_response = exchange_code(code, verifier)
    account = persist_tokens(token_response)
    with SESSION.lock:
        SESSION.result = account
        SESSION.error = None
        SESSION.done.set()
    return account


HTML_PAGE = """<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>M365 Copilot PKCE Auth Gateway</title>
  <style>
    :root { color-scheme: dark; font-family: ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif; }
    body { margin: 0; background: #0b1220; color: #e8eefc; }
    .wrap { max-width: 920px; margin: 32px auto; padding: 0 16px; }
    .card { background: #121a2b; border: 1px solid #243251; border-radius: 16px; padding: 20px; margin-bottom: 16px; box-shadow: 0 10px 40px rgba(0,0,0,.25); }
    h1 { font-size: 22px; margin: 0 0 8px; }
    h2 { font-size: 16px; margin: 0 0 12px; color: #9fb4d9; }
    p, li { line-height: 1.55; color: #c7d4ee; }
    code, textarea, input { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
    a.btn, button {
      display: inline-block; background: #3b82f6; color: white; border: 0; border-radius: 10px;
      padding: 10px 14px; text-decoration: none; cursor: pointer; font-weight: 600;
    }
    button.secondary { background: #334155; }
    button.danger { background: #b91c1c; }
    textarea {
      width: 100%; min-height: 110px; box-sizing: border-box; border-radius: 12px;
      border: 1px solid #334155; background: #0b1220; color: #e8eefc; padding: 12px; resize: vertical;
    }
    .row { display: flex; gap: 10px; flex-wrap: wrap; margin-top: 12px; }
    .ok { color: #4ade80; }
    .err { color: #f87171; }
    .muted { color: #8aa0c6; font-size: 13px; }
    .kv { display: grid; grid-template-columns: 140px 1fr; gap: 6px 10px; }
    .kv div:nth-child(odd) { color: #8aa0c6; }
    pre {
      white-space: pre-wrap; word-break: break-all; background: #0b1220; border: 1px solid #243251;
      border-radius: 12px; padding: 12px; max-height: 220px; overflow: auto;
    }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="card">
      <h1>M365 Copilot 浏览器授权网关</h1>
      <p>非设备码。使用 Office Copilot 官方 client：<code>c0ab8ce9-e9a0-42e7-b064-33d422df41f1</code></p>
      <p class="muted">登录后浏览器会跳到 <code>oauth2/nativeclient?code=...</code>。请立刻复制完整地址粘贴到下方（有时会再跳到 wrongplace）。</p>
      <div class="row">
        <a class="btn" id="loginBtn" href="#" target="_blank" rel="noreferrer">1. 打开 Microsoft 登录</a>
        <button class="secondary" onclick="refreshStatus()">刷新状态</button>
        <button class="danger" onclick="resetSession()">重置会话</button>
      </div>
    </div>

    <div class="card">
      <h2>2. 粘贴回调地址 / 授权码</h2>
      <textarea id="callback" placeholder="https://login.microsoftonline.com/common/oauth2/nativeclient?code=...&state=...&#10;或者只贴 code= 后面的授权码"></textarea>
      <div class="row">
        <button onclick="submitCallback()">提交并换 token</button>
      </div>
      <p id="msg" class="muted"></p>
    </div>

    <div class="card">
      <h2>当前状态</h2>
      <div id="status" class="muted">loading...</div>
    </div>

    <div class="card">
      <h2>说明</h2>
      <ol>
        <li>点“打开 Microsoft 登录”，用有 Copilot 权限的账号登录。</li>
        <li>登录完成后，地址栏出现 <code>.../oauth2/nativeclient?code=...</code> 时立刻复制整段 URL。</li>
        <li>粘贴到上面提交。网关会用 PKCE verifier 换 access/refresh token 并落盘。</li>
        <li>也可用 API：<code>POST /api/exchange</code>，body: <code>{"callback":"..."}</code></li>
      </ol>
    </div>
  </div>
  <script>
    async function refreshStatus() {
      const res = await fetch('/api/status');
      const data = await res.json();
      document.getElementById('loginBtn').href = data.auth_url;
      const box = document.getElementById('status');
      if (data.completed) {
        const s = data.result_summary;
        box.innerHTML = `<div class="ok">✅ 已完成</div>
          <div class="kv" style="margin-top:10px">
            <div>email</div><div>${s.email || ''}</div>
            <div>appid</div><div>${s.appid || ''}</div>
            <div>aud</div><div>${s.aud || ''}</div>
            <div>expires</div><div>${s.expires_at || ''}</div>
            <div>token file</div><div><code>${s.token_file || ''}</code></div>
          </div>`;
      } else if (data.error) {
        box.innerHTML = `<div class="err">❌ ${data.error}</div><pre>${data.auth_url}</pre>`;
      } else {
        box.innerHTML = `<div>等待回调...</div>
          <div class="kv" style="margin-top:10px">
            <div>client</div><div><code>${data.client_id}</code></div>
            <div>redirect</div><div><code>${data.redirect_uri}</code></div>
            <div>state</div><div><code>${data.state}</code></div>
          </div>
          <p class="muted">登录 URL：</p>
          <pre>${data.auth_url}</pre>`;
      }
    }
    async function submitCallback() {
      const callback = document.getElementById('callback').value.trim();
      const msg = document.getElementById('msg');
      msg.textContent = '提交中...';
      msg.className = 'muted';
      try {
        const res = await fetch('/api/exchange', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({callback})
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || JSON.stringify(data));
        msg.textContent = '成功：' + (data.email || '') + ' / appid=' + (data.appid || '');
        msg.className = 'ok';
        await refreshStatus();
      } catch (e) {
        msg.textContent = '失败：' + e.message;
        msg.className = 'err';
      }
    }
    async function resetSession() {
      await fetch('/api/reset', {method: 'POST'});
      document.getElementById('callback').value = '';
      document.getElementById('msg').textContent = '已重置';
      await refreshStatus();
    }
    refreshStatus();
    setInterval(refreshStatus, 3000);
  </script>
</body>
</html>
"""


class Handler(BaseHTTPRequestHandler):
    server_version = "M365PkceAuthGateway/1.0"

    def log_message(self, fmt: str, *args: Any) -> None:
        sys.stderr.write("[http] " + (fmt % args) + "\n")

    def _send(self, code: int, body: bytes, content_type: str) -> None:
        self.send_response(code)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def _json(self, code: int, obj: Any) -> None:
        raw = json.dumps(obj, ensure_ascii=False, indent=2).encode("utf-8")
        self._send(code, raw, "application/json; charset=utf-8")

    def do_GET(self) -> None:  # noqa: N802
        parsed = urllib.parse.urlparse(self.path)
        path = parsed.path
        qs = urllib.parse.parse_qs(parsed.query)

        if path in ("/", "/index.html"):
            self._send(200, HTML_PAGE.encode("utf-8"), "text/html; charset=utf-8")
            return

        if path == "/api/status":
            self._json(200, SESSION.status())
            return

        if path == "/api/auth-url":
            self._json(200, {"auth_url": SESSION.status()["auth_url"], "state": SESSION.status()["state"]})
            return

        # direct callback capture if someone points a custom redirect here later
        if path in ("/callback", "/auth/callback"):
            # support ?code=... or ?url=full-callback
            if "url" in qs:
                callback = qs["url"][0]
            else:
                callback = REDIRECT_URI + "?" + parsed.query
            try:
                account = complete_with_callback(callback)
                html = f"""<!doctype html><meta charset=utf-8>
                <title>OK</title><body style='font-family:sans-serif;background:#0b1220;color:#e8eefc;padding:24px'>
                <h1>✅ Auth complete</h1>
                <p>{account.get('email')}</p>
                <p>appid={account.get('appid')}</p>
                <p>aud={account.get('aud')}</p>
                <p>saved: {TOKEN_FILE}</p>
                </body>"""
                self._send(200, html.encode("utf-8"), "text/html; charset=utf-8")
            except Exception as e:
                self._send(400, f"callback failed: {e}".encode("utf-8"), "text/plain; charset=utf-8")
            return

        self._json(404, {"error": "not found", "paths": ["/", "/api/status", "/api/auth-url", "/api/exchange", "/callback"]})

    def do_POST(self) -> None:  # noqa: N802
        parsed = urllib.parse.urlparse(self.path)
        path = parsed.path
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b"{}"
        try:
            body = json.loads(raw.decode("utf-8") or "{}")
        except Exception:
            body = {}

        if path == "/api/reset":
            with SESSION.lock:
                SESSION.reset()
            self._json(200, {"ok": True, **SESSION.status()})
            return

        if path == "/api/exchange":
            callback = body.get("callback") or body.get("url") or body.get("code")
            if not callback:
                self._json(400, {"error": "missing callback/url/code"})
                return
            try:
                account = complete_with_callback(str(callback))
                self._json(
                    200,
                    {
                        "ok": True,
                        "email": account.get("email"),
                        "appid": account.get("appid"),
                        "aud": account.get("aud"),
                        "oid": account.get("oid"),
                        "tid": account.get("tid"),
                        "expiresAt": account.get("expiresAt"),
                        "tokenFile": str(TOKEN_FILE),
                    },
                )
            except Exception as e:
                with SESSION.lock:
                    SESSION.error = str(e)
                self._json(400, {"error": str(e)})
            return

        self._json(404, {"error": "not found"})


def print_banner(host: str, port: int) -> None:
    st = SESSION.status()
    print("=" * 72)
    print("M365 Copilot PKCE Auth Gateway  (NO device code)")
    print("=" * 72)
    print(f"client_id : {CLIENT_ID}")
    print(f"redirect  : {REDIRECT_URI}")
    print(f"scopes    : {' '.join(SCOPES)}")
    print(f"token file: {TOKEN_FILE}")
    print()
    print(f"Gateway UI: http://127.0.0.1:{port}/")
    if host not in ("127.0.0.1", "localhost"):
        print(f"          : http://{host}:{port}/  (if reachable)")
    print()
    print("Login URL:")
    print(st["auth_url"])
    print()
    print("After browser login, paste callback URL containing code= into the UI,")
    print("or run:")
    print(f"  python {Path(__file__).name} --exchange 'https://login.microsoftonline.com/common/oauth2/nativeclient?code=...'")
    print("=" * 72)


def serve(host: str, port: int) -> None:
    DEFAULT_STATE_DIR.mkdir(parents=True, exist_ok=True)
    (DEFAULT_STATE_DIR / "auth-url.txt").write_text(SESSION.status()["auth_url"], encoding="utf-8")
    httpd = ThreadingHTTPServer((host, port), Handler)
    print_banner(host, port)
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\nbye")
    finally:
        httpd.server_close()


def main() -> int:
    global TOKEN_FILE

    parser = argparse.ArgumentParser(description="M365 Copilot browser PKCE auth gateway")
    parser.add_argument("--serve", action="store_true", help="start local auth gateway")
    parser.add_argument("--host", default=DEFAULT_HOST)
    parser.add_argument("--port", type=int, default=DEFAULT_PORT)
    parser.add_argument("--exchange", metavar="CALLBACK", help="exchange a pasted callback URL / code and exit")
    parser.add_argument("--print-url", action="store_true", help="print auth URL and exit")
    parser.add_argument(
        "--token-file",
        default=str(Path(os.path.expanduser(os.environ.get("M365_TOKEN_FILE", "~/.config/m365-native/accounts.json")))),
    )
    args = parser.parse_args()
    TOKEN_FILE = Path(args.token_file)

    if args.print_url:
        print(SESSION.status()["auth_url"])
        return 0

    if args.exchange:
        try:
            account = complete_with_callback(args.exchange)
        except Exception as e:
            print(f"ERROR: {e}", file=sys.stderr)
            return 1
        print(json.dumps({
            "ok": True,
            "email": account.get("email"),
            "appid": account.get("appid"),
            "aud": account.get("aud"),
            "expiresAt": account.get("expiresAt"),
            "tokenFile": str(TOKEN_FILE),
        }, ensure_ascii=False, indent=2))
        return 0

    # default: serve
    serve(args.host, args.port)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
