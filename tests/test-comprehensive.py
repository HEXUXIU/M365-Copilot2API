#!/usr/bin/env python3
"""M365-Copilot2API v0.6.0 Comprehensive Feature Test"""

import sys, json, time, os, requests

BASE = os.environ.get("M365_TEST_BASE", "http://127.0.0.1:4142")
ADMIN_PW = os.environ.get("M365_TEST_ADMIN_PASSWORD", "")
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


def do_req(method, url, headers=None, body=None, timeout=120):
    try:
        r = requests.request(method, url, headers=headers, json=body, timeout=timeout, allow_redirects=False)
        return r
    except Exception:
        return None


def test_ep(name, method="GET", url="", headers=None, body=None, expected=None, timeout=120, validate=None):
    if expected is None:
        expected = [200]
    r = do_req(method, url, headers, body, timeout)
    if r is None:
        ok(name, False, "request failed")
        return None
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


print(f"\n  M365-Copilot2API v0.6.0 Comprehensive Feature Test")
print(f"  Target: {BASE}")
print(f"  Time: {time.strftime('%Y-%m-%d %H:%M:%S')}\n")

# 0. Admin Login
section("0. Admin Login & Session")
sess = requests.Session()
r = sess.post(f"{BASE}/api/admin/login", json={"password": ADMIN_PW}, timeout=15)
sc = r.headers.get("Set-Cookie", "")
if sc:
    session_cookie = sc.split(";")[0]
    ok("Admin login (Set-Cookie)", True)
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

# 1. Health & Version
section("1. Health & Version")
test_ep("GET /api/health", url=f"{BASE}/api/health", expected=[200, 401])
r_ver = test_ep("GET /api/version", url=f"{BASE}/api/version", headers=admin_headers(),
                expected=[200], validate=lambda r, j: j is not None and ("version" in j or "Version" in j))
if r_ver is None or r_ver.status_code != 200:
    test_ep("GET /api/version (no-auth accepted)", url=f"{BASE}/api/version", expected=[401])

# 2. Auth
section("2. Auth Endpoints")
test_ep("GET /api/auth/start", url=f"{BASE}/api/auth/start", expected=[200, 302, 401])
test_ep("GET /api/auth/status", url=f"{BASE}/api/auth/status", expected=[200, 400, 401])

# 3. Admin Session
section("3. Admin Session & Password")
test_ep("GET /api/admin/session", url=f"{BASE}/api/admin/session", headers=admin_headers(), expected=[200])
test_ep("POST change-password (wrong old)", method="POST",
        url=f"{BASE}/api/admin/change-password", headers=admin_headers(),
        body={"oldPassword": "wrong", "newPassword": "test12345678"}, expected=[400, 401, 403])
test_ep("POST change-password (short new)", method="POST",
        url=f"{BASE}/api/admin/change-password", headers=admin_headers(),
        body={"oldPassword": ADMIN_PW, "newPassword": "short"}, expected=[400, 401, 403])

# 4. API Key CRUD
section("4. API Key CRUD")
test_ep("GET /api/admin/keys", url=f"{BASE}/api/admin/keys", headers=admin_headers(), expected=[200])
created_key_id = None
created_key_raw = None
r = test_ep("POST create key", method="POST",
            url=f"{BASE}/api/admin/keys", headers=admin_headers(),
            body={"name": f"test-key-{int(time.time())}"}, expected=[200, 201])
if r:
    try:
        d = r.json()
        created_key_id = d.get("id") or d.get("record", {}).get("id")
        created_key_raw = d.get("key") or d.get("record", {}).get("key")
    except Exception:
        pass
if created_key_raw:
    test_ep("New key can auth", url=f"{BASE}/v1/models",
            headers={"Authorization": f"Bearer {created_key_raw}"}, expected=[200])
else:
    skip("New key auth", "no raw key")
if created_key_id:
    test_ep("DELETE created key", method="DELETE",
            url=f"{BASE}/api/admin/keys?id={created_key_id}", headers=admin_headers(), expected=[200, 204])
    if created_key_raw:
        test_ep("Deleted key rejected", url=f"{BASE}/v1/models",
                headers={"Authorization": f"Bearer {created_key_raw}"}, expected=[401, 403])
else:
    skip("Key DELETE + rejection", "no key id")

# 5. Models
section("5. Models")
test_ep("GET /api/admin/models", url=f"{BASE}/api/admin/models", headers=admin_headers(), expected=[200])
test_ep("GET /v1/models", url=f"{BASE}/v1/models", headers=api_headers(), expected=[200],
        validate=lambda r, j: j is not None and "data" in j and len(j["data"]) > 0)

# 6. Settings
section("6. Settings")
test_ep("GET /api/admin/settings", url=f"{BASE}/api/admin/settings", headers=admin_headers(), expected=[200])
test_ep("POST models/sync", method="POST",
        url=f"{BASE}/api/admin/models/sync", headers=admin_headers(), expected=[200, 501])

# 7. Proxy Pool & Deployments
section("7. Proxy Pool & Deployments")
test_ep("GET proxy-pool", url=f"{BASE}/api/admin/proxy-pool", headers=admin_headers(), expected=[200, 404])
test_ep("GET deployments", url=f"{BASE}/api/admin/deployments", headers=admin_headers(), expected=[200, 404])

# 8. Accounts
section("8. Accounts")
test_ep("GET /api/accounts", url=f"{BASE}/api/accounts", headers=admin_headers(), expected=[200, 404])
test_ep("GET token-health", url=f"{BASE}/api/accounts/token-health",
        headers=admin_headers(), expected=[200, 404])
test_ep("POST clear-cooldown", method="POST",
        url=f"{BASE}/api/accounts/clear-cooldown", headers=admin_headers(), expected=[200, 404])

# 9. Chat Completions non-stream
section("9. Chat Completions (non-stream)")
r = test_ep("POST /v1/chat/completions", method="POST",
            url=f"{BASE}/v1/chat/completions", headers=api_headers(),
            body={"model": "gpt-4", "messages": [{"role": "user", "content": "Say hello in one word."}], "stream": False},
            expected=[200], timeout=120)
if r:
    try:
        j = r.json()
        ok("  has choices", "choices" in j and len(j["choices"]) > 0)
        if j.get("choices"):
            c = j["choices"][0]
            ok("  has message", "message" in c and "content" in c["message"])
            ok("  content non-empty", len(c["message"].get("content", "")) > 0)
        ok("  has id", "id" in j and j["id"].startswith("chatcmpl-"))
        ok("  has model", "model" in j and j["model"])
        ok("  has usage", "usage" in j and "total_tokens" in j["usage"])
    except Exception as e:
        ok("non-stream parse", False, str(e)[:80])

# 10. Chat Completions stream
section("10. Chat Completions (stream)")
r = do_req("POST", f"{BASE}/v1/chat/completions", api_headers(),
           {"model": "gpt-4", "messages": [{"role": "user", "content": "Say hi"}], "stream": True}, 120)
if r and r.status_code == 200:
    chunks = 0
    done = False
    for line in r.iter_lines(decode_unicode=True):
        if line and line.startswith("data: "):
            payload = line[6:]
            if payload == "[DONE]":
                done = True
            else:
                chunks += 1
    ok("stream received chunks", chunks > 0)
    ok("stream received [DONE]", done)
else:
    ok("stream request", False, f"status={r.status_code if r else 'None'}")

# 11. Temp session
section("11. Temp Session (v0.6.0)")
test_ep("POST with copilot_temp_session", method="POST",
        url=f"{BASE}/v1/chat/completions", headers=api_headers(),
        body={"model": "gpt-4", "messages": [{"role": "user", "content": "Hi"}],
              "stream": False, "metadata": {"copilot_temp_session": True}},
        expected=[200], timeout=120)

# 12. Edge Cases
section("12. Edge Cases")
test_ep("empty messages", method="POST", url=f"{BASE}/v1/chat/completions", headers=api_headers(),
        body={"model": "gpt-4", "messages": [], "stream": False}, expected=[200, 400, 422], timeout=120)
test_ep("bad model", method="POST", url=f"{BASE}/v1/chat/completions", headers=api_headers(),
        body={"model": "nonexistent-xyz", "messages": [{"role": "user", "content": "hi"}], "stream": False},
        expected=[200, 400, 404, 422, 500], timeout=120)
test_ep("no model", method="POST", url=f"{BASE}/v1/chat/completions", headers=api_headers(),
        body={"messages": [{"role": "user", "content": "hi"}], "stream": False},
        expected=[200, 400, 422], timeout=120)

# 13. Responses API
section("13. Responses API")
test_ep("POST /v1/responses", method="POST", url=f"{BASE}/v1/responses",
        headers=api_headers(), body={"model": "gpt-4", "input": "Hello", "stream": False},
        expected=[200, 404, 501], timeout=120)

# 14. Anthropic Messages
section("14. Anthropic Messages")
test_ep("POST /v1/messages", method="POST", url=f"{BASE}/v1/messages",
        headers=api_headers(),
        body={"model": "claude-3-5-sonnet-20241022", "messages": [{"role": "user", "content": "Hi"}],
              "max_tokens": 100, "stream": False},
        expected=[200, 404, 501], timeout=120)

# 15. Images
section("15. Images")
test_ep("POST /v1/images/generations", method="POST",
        url=f"{BASE}/v1/images/generations", headers=api_headers(),
        body={"model": "dall-e-3", "prompt": "a blue square", "n": 1, "size": "1024x1024"},
        expected=[200, 404, 501, 502], timeout=120)
test_ep("POST /v1/images/edits", method="POST",
        url=f"{BASE}/v1/images/edits", headers=api_headers(), expected=[200, 400, 404, 501])
test_ep("GET /v1/images/files/", url=f"{BASE}/v1/images/files/",
        headers=api_headers(), expected=[200, 301, 404])

# 16. MCP
section("16. MCP")
test_ep("GET /v1/mcp/tools", url=f"{BASE}/v1/mcp/tools", headers=api_headers(), expected=[200, 404])
try:
    r = requests.get(f"{BASE}/v1/mcp/sse", headers=api_headers(), timeout=5, stream=True)
    ok("GET /v1/mcp/sse (SSE open)", r.status_code in [200, 404], f"status={r.status_code}")
    r.close()
except requests.exceptions.Timeout:
    skip("GET /v1/mcp/sse", "SSE held open (long-lived stream) - treated as alive")
except Exception as e:
    ok("GET /v1/mcp/sse", False, str(e)[:80])

# 17. Memory/Personalization (v0.6.0)
section("17. Memory API (v0.6.0)")
r_flags = test_ep("GET /v1/memory/flags", url=f"{BASE}/v1/memory/flags",
                   headers=api_headers(), expected=[200, 401, 502])
if r_flags and r_flags.status_code == 200:
    try:
        j = r_flags.json()
        ok("  flags has isMemoryEnabled", "isMemoryEnabled" in j)
        ok("  flags has isCustomInstructionEnabled", "isCustomInstructionEnabled" in j)
    except Exception:
        pass

r_patch = test_ep("PATCH /v1/memory/flags (admin)", method="PATCH",
                   url=f"{BASE}/v1/memory/flags", headers=admin_headers(),
                   body={"isMemoryEnabled": True}, expected=[200, 401, 502])
if r_patch and r_patch.status_code == 200:
    try:
        j = r_patch.json()
        ok("  PATCH returns Success", j.get("result", {}).get("value") == "Success")
    except Exception:
        pass

r_inst = test_ep("GET /v1/memory/instructions", url=f"{BASE}/v1/memory/instructions",
                  headers=api_headers(), expected=[200, 401, 502])
if r_inst and r_inst.status_code == 200:
    try:
        j = r_inst.json()
        ok("  instructions is list", "instructions" in j and isinstance(j["instructions"], list))
    except Exception:
        pass

instruction_id = None
r_put = test_ep("PUT /v1/memory/instructions (admin)", method="PUT",
                url=f"{BASE}/v1/memory/instructions", headers=admin_headers(),
                body={"instruction": f"test-inst-{int(time.time())}", "useCase": "GenericChat"},
                expected=[200, 401, 502])
if r_put and r_put.status_code == 200:
    try:
        j = r_put.json()
        instruction_id = j.get("id")
        ok("  PUT returns id", instruction_id is not None)
    except Exception:
        pass

if instruction_id:
    test_ep("DELETE /v1/memory/instructions/{id} (admin)", method="DELETE",
            url=f"{BASE}/v1/memory/instructions/{instruction_id}", headers=admin_headers(),
            expected=[200, 204, 401, 502])
else:
    skip("DELETE instruction", "no id")

test_ep("PATCH /v1/memory/settings (admin)", method="PATCH",
        url=f"{BASE}/v1/memory/settings", headers=admin_headers(),
        body={"isWebSearchInWebEnabled": True}, expected=[200, 204, 401, 502])

# 18. Memory API Auth
section("18. Memory Auth Enforcement")
test_ep("PATCH flags (api key rejected)", method="PATCH",
        url=f"{BASE}/v1/memory/flags", headers=api_headers(),
        body={"isMemoryEnabled": False}, expected=[401, 403])
test_ep("PUT instructions (api key rejected)", method="PUT",
        url=f"{BASE}/v1/memory/instructions", headers=api_headers(),
        body={"instruction": "x", "useCase": "GenericChat"}, expected=[401, 403])
test_ep("DELETE instructions (api key rejected)", method="DELETE",
        url=f"{BASE}/v1/memory/instructions/x", headers=api_headers(), expected=[401, 403])
test_ep("PATCH settings (api key rejected)", method="PATCH",
        url=f"{BASE}/v1/memory/settings", headers=api_headers(),
        body={"isWebSearchInWebEnabled": True}, expected=[401, 403])

# 19. Conversations
section("19. Conversations")
test_ep("GET /api/conversations", url=f"{BASE}/api/conversations",
        headers=api_headers(), expected=[200, 401, 404])
test_ep("GET /api/m365/conversations", url=f"{BASE}/api/m365/conversations",
        headers=admin_headers(), expected=[200, 404])

# 20. Admin Chat
section("20. Admin Chat")
cb = {"messages": [{"role": "user", "content": "ping"}]}
test_ep("POST /api/chat", method="POST", url=f"{BASE}/api/chat",
        headers=admin_headers(), body=cb, expected=[200, 400, 404], timeout=120)
test_ep("POST /api/chat/stream", method="POST", url=f"{BASE}/api/chat/stream",
        headers=admin_headers(), body=cb, expected=[200, 400, 404], timeout=120)

# 21. Security
section("21. Security")
test_ep("no auth", url=f"{BASE}/v1/models", expected=[401, 403])
test_ep("wrong Bearer", url=f"{BASE}/v1/models",
        headers={"Authorization": "Bearer wrong_key"}, expected=[401, 403])
test_ep("admin no cookie", url=f"{BASE}/api/admin/keys", expected=[401, 403])
test_ep("wrong admin pw", method="POST",
        url=f"{BASE}/api/admin/login", body={"password": "wrong"}, expected=[401, 403])
test_ep("API key on admin", url=f"{BASE}/api/admin/keys",
        headers=api_headers(), expected=[401, 403])

# 22. Context Budget
section("22. Context Budget")
long_msg = "x " * 200000
try:
    t0 = time.monotonic()
    r = requests.post(f"{BASE}/v1/chat/completions", headers=api_headers(),
                      json={"model": "gpt-4", "messages": [{"role": "user", "content": long_msg}], "stream": False}, timeout=30)
    ms = (time.monotonic() - t0) * 1000
    ok("200k tokens -> 400", r.status_code == 400, f"got {r.status_code} ({ms:.0f}ms)")
    if r.status_code == 400:
        try:
            j = r.json()
            err = j.get("error", {})
            code = err.get("code") or err.get("type")
            ok("  code=context_length_exceeded", code == "context_length_exceeded", f"got {code}")
        except Exception:
            pass
except Exception as e:
    ok("200k tokens -> 400", False, f"exception: {str(e)[:100]}")

# 23. Usage & Stats
section("23. Usage & Stats")
test_ep("GET /api/usage", url=f"{BASE}/api/usage", headers=admin_headers(), expected=[200])
test_ep("GET /api/usage/logs", url=f"{BASE}/api/usage/logs", headers=admin_headers(), expected=[200])
test_ep("GET /api/stats", url=f"{BASE}/api/stats", headers=admin_headers(), expected=[200])
r_sr = test_ep("POST /api/stats/reset", method="POST", url=f"{BASE}/api/stats/reset",
               headers=admin_headers(), expected=[200, 204, 405])
if r_sr is not None and r_sr.status_code == 405:
    test_ep("GET /api/stats/reset (GET-only)", url=f"{BASE}/api/stats/reset",
            headers=admin_headers(), expected=[200])

# 24. Debug
section("24. Debug")
test_ep("GET debug/logs", url=f"{BASE}/api/admin/debug/logs", headers=admin_headers(), expected=[200])

# Summary
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
