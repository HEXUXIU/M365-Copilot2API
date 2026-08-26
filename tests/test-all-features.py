#!/usr/bin/env python3
"""M365-Copilot2API Full Feature Test"""

import sys, json, time, os, requests

BASE = "http://127.0.0.1:4141"
ADMIN_PW = "LNBuuAsbrS47XUM"
API_KEY = os.environ.get("M365_TEST_API_KEY", "")

passed = failed = skipped = 0
results = []
session_cookie = None


def color(text, c):
    codes = {"green": 32, "red": 31, "yellow": 33, "cyan": 36, "gray": 90, "white": 97, "magenta": 35}
    return f"\033[{codes.get(c, 0)}m{text}\033[0m"


def _record(status, name, detail=""):
    global passed, failed, skipped
    if status == "PASS":
        passed += 1
        print(color(f"  PASS {name}", "green"))
    elif status == "FAIL":
        failed += 1
        print(color(f"  FAIL {name}" + (f" - {detail}" if detail else ""), "red"))
    else:
        skipped += 1
        print(color(f"  SKIP {name}" + (f" - {detail}" if detail else ""), "yellow"))
    results.append({"status": status, "name": name, "detail": detail})


def ok(name, cond, detail=""):
    _record("PASS" if cond else "FAIL", name, detail)


def skip(name, reason=""):
    _record("SKIP", name, reason)


def section(name):
    print(f"\n{'=' * 60}")
    print(f"  {name}")
    print(f"{'=' * 60}")


def admin_headers():
    h = {"Content-Type": "application/json"}
    if session_cookie:
        h["Cookie"] = session_cookie
    return h


def api_headers():
    return {"Authorization": f"Bearer {API_KEY}", "Content-Type": "application/json"}


def test_endpoint(name, method="GET", url="", headers=None, body=None, expected=None, timeout=15, validate=None):
    if expected is None:
        expected = [200]
    try:
        r = requests.request(method, url, headers=headers, json=body if isinstance(body, dict) else None,
                             data=body if isinstance(body, str) else None, timeout=timeout, allow_redirects=False)
        if r.status_code not in expected:
            ok(name, False, f"expected {expected}, got {r.status_code}")
            return r
        if validate:
            try:
                j = r.json()
            except Exception:
                j = None
            if validate(r, j):
                ok(name, True)
            else:
                ok(name, False, "validation failed")
        else:
            ok(name, True)
        return r
    except requests.exceptions.RequestException as e:
        ok(name, False, str(e)[:120])
        return None


print(f"\n  M365-Copilot2API Full Feature Test")
print(f"  Target: {BASE}")
print(f"  Time: {time.strftime('%Y-%m-%d %H:%M:%S')}\n")

# ── 0. Admin Login ──
section("0. Admin Login")
try:
    sess = requests.Session()
    r = sess.post(f"{BASE}/api/admin/login", json={"password": ADMIN_PW}, timeout=15)
    sc = r.headers.get("Set-Cookie", "")
    if sc:
        session_cookie = sc.split(";")[0]
        ok("Admin login", True)
    elif sess.cookies.get("m365_admin_session"):
        session_cookie = f"m365_admin_session={sess.cookies.get('m365_admin_session')}"
        ok("Admin login (cookie jar)", True)
    else:
        try:
            d = r.json()
            if d.get("token"):
                session_cookie = f"session={d['token']}"
                ok("Admin login (token)", True)
            else:
                ok("Admin login", False, "no cookie or token")
        except Exception:
            ok("Admin login", False, "no cookie")
except Exception as e:
    ok("Admin login", False, str(e)[:120])

# ── 1. Health & Version ──
section("1. Health & Version")
test_endpoint("GET /api/health", url=f"{BASE}/api/health", expected=[200, 401])
test_endpoint("GET /api/version", url=f"{BASE}/api/version", expected=[200, 401])

# ── 2. Auth ──
section("2. Auth Endpoints")
test_endpoint("GET /api/auth/start", url=f"{BASE}/api/auth/start", expected=[200, 302, 401])
test_endpoint("GET /api/auth/status", url=f"{BASE}/api/auth/status", expected=[200, 400, 401])

# ── 3. Admin Session ──
section("3. Admin Session")
test_endpoint("GET /api/admin/session", url=f"{BASE}/api/admin/session",
              headers=admin_headers(), expected=[200])

# ── 4. Password Change ──
section("4. Admin Password")
test_endpoint("POST /api/admin/change-password (wrong)", method="POST",
              url=f"{BASE}/api/admin/change-password", headers=admin_headers(),
              body={"oldPassword": "wrong", "newPassword": "test12345678"}, expected=[400, 401, 403])

# ── 5. API Key CRUD ──
section("5. API Key CRUD")
test_endpoint("GET /api/admin/keys", url=f"{BASE}/api/admin/keys",
              headers=admin_headers(), expected=[200])

created_key_id = None
r = test_endpoint("POST /api/admin/keys (create)", method="POST",
                  url=f"{BASE}/api/admin/keys", headers=admin_headers(),
                  body={"name": f"test-key-{int(time.time())}"}, expected=[200, 201],
                  validate=lambda r, j: j is not None)
if r:
    try:
        d = r.json()
        created_key_id = d.get("id") or d.get("record", {}).get("id")
    except Exception:
        pass

if created_key_id:
    test_endpoint(f"DELETE /api/admin/keys/{created_key_id}", method="DELETE",
                  url=f"{BASE}/api/admin/keys?id={created_key_id}", headers=admin_headers(),
                  expected=[200, 204])
else:
    skip("API Key DELETE", "no id from create")

# ── 6. Models ──
section("6. Models")
test_endpoint("GET /api/admin/models", url=f"{BASE}/api/admin/models",
              headers=admin_headers(), expected=[200])

# ── 7. Settings ──
section("7. Settings")
test_endpoint("GET /api/admin/settings", url=f"{BASE}/api/admin/settings",
              headers=admin_headers(), expected=[200])

# ── 8. Proxy Pool ──
section("8. Proxy Pool")
test_endpoint("GET /api/admin/proxy-pool", url=f"{BASE}/api/admin/proxy-pool",
              headers=admin_headers(), expected=[200, 404])

# ── 9. Deployments ──
section("9. Deployments")
test_endpoint("GET /api/admin/deployments", url=f"{BASE}/api/admin/deployments",
              headers=admin_headers(), expected=[200, 404])

# ── 10. Accounts ──
section("10. Accounts")
test_endpoint("GET /api/accounts", url=f"{BASE}/api/accounts",
              headers=admin_headers(), expected=[200, 404])

# ── 11. /v1/models ──
section("11. OpenAI /v1/models")
test_endpoint("GET /v1/models", url=f"{BASE}/v1/models", headers=api_headers(), expected=[200],
              validate=lambda r, j: j is not None)

# ── 12. Chat Completions (non-stream) ──
section("12. Chat Completions (non-stream)")
test_endpoint("POST /v1/chat/completions", method="POST",
              url=f"{BASE}/v1/chat/completions", headers=api_headers(),
              body={"model": "gpt-4", "messages": [{"role": "user", "content": "Say hello in one word."}], "stream": False},
              expected=[200], timeout=120, validate=lambda r, j: j is not None)

# ── 13. Chat Completions (stream) ──
section("13. Chat Completions (stream)")
test_endpoint("POST /v1/chat/completions (stream)", method="POST",
              url=f"{BASE}/v1/chat/completions", headers=api_headers(),
              body={"model": "gpt-4", "messages": [{"role": "user", "content": "Say hi"}], "stream": True},
              expected=[200], timeout=120, validate=lambda r, j: len(r.content) > 0)

# ── 14. /v1/responses ──
section("14. /v1/responses")
test_endpoint("POST /v1/responses", method="POST", url=f"{BASE}/v1/responses",
              headers=api_headers(), body={"model": "gpt-4", "input": "Say hello", "stream": False},
              expected=[200, 404, 501], timeout=120)

# ── 15. /v1/messages ──
section("15. /v1/messages (Anthropic)")
test_endpoint("POST /v1/messages", method="POST", url=f"{BASE}/v1/messages",
              headers=api_headers(),
              body={"model": "claude-3-5-sonnet-20241022", "messages": [{"role": "user", "content": "Hi"}],
                    "max_tokens": 100, "stream": False},
              expected=[200, 404, 501], timeout=120)

# ── 16. Image Endpoints ──
section("16. Image Endpoints")
test_endpoint("POST /v1/images/generations", method="POST",
              url=f"{BASE}/v1/images/generations", headers=api_headers(),
              body={"model": "dall-e-3", "prompt": "a blue square", "n": 1, "size": "1024x1024"},
              expected=[200, 404, 501, 502], timeout=120)
test_endpoint("POST /v1/images/edits", method="POST",
              url=f"{BASE}/v1/images/edits", headers=api_headers(),
              expected=[200, 400, 404, 501], timeout=120)
test_endpoint("GET /v1/images/files", url=f"{BASE}/v1/images/files",
              headers=api_headers(), expected=[200, 301, 404])

# ── 17. MCP ──
section("17. MCP Endpoints")
test_endpoint("GET /v1/mcp/sse", url=f"{BASE}/v1/mcp/sse", headers=api_headers(),
              expected=[200, 404], timeout=15)
test_endpoint("GET /v1/mcp/tools", url=f"{BASE}/v1/mcp/tools", headers=api_headers(),
              expected=[200, 404])

# ── 18-24. Substrate Endpoints ──
section("18. Plugins")
test_endpoint("GET /api/plugins", url=f"{BASE}/api/plugins", headers=api_headers(),
              expected=[200, 404, 502])

section("19. Custom Instructions CRUD")
test_endpoint("GET /api/custom-instructions", url=f"{BASE}/api/custom-instructions",
              headers=api_headers(), expected=[200, 404])
test_endpoint("POST /api/custom-instructions", method="POST",
              url=f"{BASE}/api/custom-instructions", headers=api_headers(),
              body={"instructions": f"test-{int(time.time())}", "enabled": True},
              expected=[200, 201, 404, 502])
test_endpoint("DELETE /api/custom-instructions", method="DELETE",
              url=f"{BASE}/api/custom-instructions", headers=api_headers(),
              expected=[200, 204, 400, 404])

section("20. Personalization Flags")
test_endpoint("GET /api/personalization-flags", url=f"{BASE}/api/personalization-flags",
              headers=api_headers(), expected=[200, 404])
test_endpoint("POST /api/personalization-flags", method="POST",
              url=f"{BASE}/api/personalization-flags", headers=api_headers(),
              body={"flags": {"suggestFollowups": True}}, expected=[200, 201, 404, 502])

section("21. Sensitivity Labels")
test_endpoint("GET /api/sensitivity-labels", url=f"{BASE}/api/sensitivity-labels",
              headers=api_headers(), expected=[200, 401, 404])

section("22. ECS Endpoints")
test_endpoint("GET /api/ecs/designer", url=f"{BASE}/api/ecs/designer",
              headers=api_headers(), expected=[200, 404])
test_endpoint("GET /api/ecs/fluid", url=f"{BASE}/api/ecs/fluid",
              headers=api_headers(), expected=[200, 404])

section("23. User Config")
test_endpoint("GET /api/userconfig", url=f"{BASE}/api/userconfig",
              headers=api_headers(), expected=[200, 404, 405])

section("24. Suggestions")
test_endpoint("GET /api/suggestions", url=f"{BASE}/api/suggestions",
              headers=api_headers(), expected=[200, 404, 405])

# ── 25. Conversations ──
section("25. Conversations")
test_endpoint("GET /api/conversations", url=f"{BASE}/api/conversations",
              headers=api_headers(), expected=[200, 401, 404])
test_endpoint("GET /api/m365/conversations", url=f"{BASE}/api/m365/conversations",
              headers=admin_headers(), expected=[200, 404])

# ── 26. Admin Chat ──
section("26. Admin Chat")
chat_body = {"messages": [{"role": "user", "content": "ping"}]}
test_endpoint("POST /api/chat", method="POST", url=f"{BASE}/api/chat",
              headers=admin_headers(), body=chat_body, expected=[200, 400, 404], timeout=120)
test_endpoint("POST /api/chat/stream", method="POST", url=f"{BASE}/api/chat/stream",
              headers=admin_headers(), body=chat_body, expected=[200, 400, 404], timeout=120)

# ── 27. Auth Negative ──
section("27. Auth Negative Tests")
test_endpoint("GET /v1/models (no auth)", url=f"{BASE}/v1/models", expected=[401, 403])
test_endpoint("GET /v1/models (wrong key)", url=f"{BASE}/v1/models",
              headers={"Authorization": "Bearer wrong_key_12345"}, expected=[401, 403])
test_endpoint("POST /api/admin/login (wrong pw)", method="POST",
              url=f"{BASE}/api/admin/login", body={"password": "wrong_password"},
              expected=[401, 403])

# ── 28. FeatureFlags ──
section("28. FeatureFlags via Settings")
test_endpoint("GET /api/admin/settings (FeatureFlags)", url=f"{BASE}/api/admin/settings",
              headers=admin_headers(), expected=[200])

# ── 29. Additional Substrate ──
section("29. Additional Substrate")
test_endpoint("GET /api/plugins (admin)", url=f"{BASE}/api/plugins",
              headers=admin_headers(), expected=[200, 401, 403, 404])
test_endpoint("GET /api/ecs/designer (admin)", url=f"{BASE}/api/ecs/designer",
              headers=admin_headers(), expected=[200, 401, 403, 404])

# ── 30. Edge Cases ──
section("30. Edge Cases")
test_endpoint("POST /v1/chat/completions (empty msgs)", method="POST",
              url=f"{BASE}/v1/chat/completions", headers=api_headers(),
              body={"model": "gpt-4", "messages": [], "stream": False},
              expected=[200, 400, 422], timeout=120)
test_endpoint("POST /v1/chat/completions (bad model)", method="POST",
              url=f"{BASE}/v1/chat/completions", headers=api_headers(),
              body={"model": "nonexistent-model-xyz", "messages": [{"role": "user", "content": "hi"}], "stream": False},
              expected=[200, 400, 404, 422, 500], timeout=120)
test_endpoint("POST /v1/chat/completions (no model)", method="POST",
              url=f"{BASE}/v1/chat/completions", headers=api_headers(),
              body={"messages": [{"role": "user", "content": "hi"}], "stream": False},
              expected=[200, 400, 422], timeout=120)

# ── Summary ──
total = passed + failed + skipped
rate = round(passed / total * 100, 1) if total else 0
print(f"\n{'=' * 60}")
print(f"  SUMMARY")
print(f"{'=' * 60}")
print(f"  Total:  {total}")
print(color(f"  PASS:   {passed}", "green"))
print(color(f"  FAIL:   {failed}", "red"))
print(color(f"  SKIP:   {skipped}", "yellow"))
print(f"  Rate:   {rate}%\n")
if failed:
    print(color("  Failed tests:", "red"))
    for r in results:
        if r["status"] == "FAIL":
            print(color(f"    - {r['name']}" + (f" ({r['detail']})" if r['detail'] else ""), "red"))
    print()
sys.exit(1 if failed else 0)
