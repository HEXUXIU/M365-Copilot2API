#!/usr/bin/env python3
"""M365-Copilot2API Stress & Limit Test"""

import sys, json, time, statistics, os, requests
from concurrent.futures import ThreadPoolExecutor, as_completed

BASE = "http://127.0.0.1:4141"
ADMIN_PW = "LNBuuAsbrS47XUM"
API_KEY = os.environ.get("M365_TEST_API_KEY", "")

all_results = []
session_cookie = None


def color(text, c):
    codes = {"green": 32, "red": 31, "yellow": 33, "cyan": 36, "gray": 90, "white": 97}
    return f"\033[{codes.get(c, 0)}m{text}\033[0m"


def section(name):
    print(f"\n{'#' * 60}")
    print(color(f"  {name}", "cyan"))
    print(f"{'#' * 60}")


def api_headers():
    return {"Authorization": f"Bearer {API_KEY}", "Content-Type": "application/json"}


def admin_headers():
    h = {"Content-Type": "application/json"}
    if session_cookie:
        h["Cookie"] = session_cookie
    return h


def measure(method, url, headers=None, body=None, timeout=120):
    t0 = time.monotonic()
    try:
        r = requests.request(method, url, headers=headers, json=body, timeout=timeout, allow_redirects=False)
        elapsed = (time.monotonic() - t0) * 1000
        return {"ok": True, "status": r.status_code, "ms": elapsed, "content": r.text,
                "headers": dict(r.headers), "error": None}
    except Exception as e:
        elapsed = (time.monotonic() - t0) * 1000
        return {"ok": False, "status": 0, "ms": elapsed, "content": None,
                "headers": None, "error": str(e)[:120]}


def calc_stats(values):
    if not values:
        return {"avg": 0, "p50": 0, "p95": 0, "p99": 0, "min": 0, "max": 0}
    s = sorted(values)
    n = len(s)
    return {
        "avg": round(statistics.mean(s), 1),
        "p50": s[int(n * 0.50)],
        "p95": s[int(n * 0.95)] if n > 1 else s[0],
        "p99": s[int(n * 0.99)] if n > 1 else s[0],
        "min": s[0],
        "max": s[-1],
    }


def print_result(name, total, success, fail, errors=None):
    rate = round(success / total * 100, 1) if total else 0
    c = "green" if rate >= 80 else "yellow" if rate >= 50 else "red"
    print(color(f"  {name}: {success}/{total} ({rate}%)", c))
    if errors:
        for e in errors[:3]:
            print(color(f"    err: {e}", "red"))


def add_result(test, total, success, fail, stats=None, errors=None):
    rate = round(success / total * 100, 1) if total else 0
    s = stats or {"avg": 0, "p50": 0, "p95": 0, "p99": 0}
    all_results.append({
        "test": test, "total": total, "ok": success, "fail": fail,
        "rate": rate, "avg": s["avg"], "p50": s["p50"], "p95": s["p95"], "p99": s["p99"],
        "errors": errors or []
    })


# ── 0. Login ──
section("0. Admin Login")
lr = measure("POST", f"{BASE}/api/admin/login", headers={"Content-Type": "application/json"},
             body={"password": ADMIN_PW}, timeout=15)
if lr["ok"]:
    sc = lr["headers"].get("Set-Cookie", "")
    if sc:
        session_cookie = sc.split(";")[0]
    else:
        try:
            d = json.loads(lr["content"])
            if d.get("token"):
                session_cookie = f"session={d['token']}"
        except Exception:
            pass
    print(color(f"  Admin login OK ({lr['ms']:.0f} ms)", "green" if session_cookie else "yellow"))
else:
    print(color(f"  Admin login FAIL: {lr['error']}", "red"))

# ── 1. Concurrent Chat ──
section("1. Concurrent Chat Completions")
chat_body = {"model": "gpt-4", "messages": [{"role": "user", "content": "Say 'ok' and nothing else."}], "stream": False}

for conc in [5, 10, 20]:
    totals = []
    successes = 0
    fails = 0
    errs = []

    def _chat(i):
        return measure("POST", f"{BASE}/v1/chat/completions", headers=api_headers(), body=chat_body, timeout=120)

    with ThreadPoolExecutor(max_workers=conc) as pool:
        futs = [pool.submit(_chat, i) for i in range(conc)]
        for f in as_completed(futs):
            r = f.result()
            totals.append(r["ms"])
            if r["ok"] and r["status"] == 200:
                successes += 1
            else:
                fails += 1
                if r["error"]:
                    errs.append(r["error"])

    st = calc_stats(totals)
    rate = round(successes / conc * 100, 1)
    c = "green" if rate >= 80 else "yellow" if rate >= 50 else "red"
    print(color(f"  c={conc}: {successes}/{conc} ({rate}%), avg={st['avg']:.0f}ms, p50={st['p50']:.0f}ms, p95={st['p95']:.0f}ms", c))
    add_result(f"Concurrent Chat (c={conc})", conc, successes, fails, st, errs)

# ── 2. Long Context ──
section("2. Long Context (50000+ tokens)")
long_msg = "x " * 50000
lr = measure("POST", f"{BASE}/v1/chat/completions", headers=api_headers(),
             body={"model": "gpt-4", "messages": [{"role": "user", "content": long_msg}], "stream": False}, timeout=120)
lok = lr["ok"] or lr["status"] in [200, 400, 413]
print(color(f"  Status: {lr['status']}, Time: {lr['ms']:.0f} ms", "green" if lok else "red"))
add_result("Long Context 50k", 1, 1 if lok else 0, 0 if lok else 1)

# ── 3. Long Context + Tools ──
section("3. Long Context + Tool Calls")
tool_body = {
    "model": "gpt-4",
    "messages": [
        {"role": "user", "content": "y " * 50000},
        {"role": "assistant", "content": None, "tool_calls": [
            {"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": '{"location":"NYC"}'}}
        ]},
        {"role": "tool", "tool_call_id": "call_1", "content": '{"temp":72}'},
        {"role": "user", "content": "What is the weather?"}
    ],
    "tools": [{"type": "function", "function": {
        "name": "get_weather", "description": "Get weather",
        "parameters": {"type": "object", "properties": {"location": {"type": "string"}}, "required": ["location"]}
    }}],
    "stream": False
}
tr = measure("POST", f"{BASE}/v1/chat/completions", headers=api_headers(), body=tool_body, timeout=120)
tok = tr["ok"] or tr["status"] in [200, 400, 413]
print(color(f"  Status: {tr['status']}, Time: {tr['ms']:.0f} ms", "green" if tok else "red"))
add_result("Long Context + Tools", 1, 1 if tok else 0, 0 if tok else 1)

# ── 4. Stream Interruption ──
section("4. Stream Interruption Recovery")
print(color("  Starting stream then aborting...", "yellow"))
try:
    s = requests.Session()
    req = requests.Request("POST", f"{BASE}/v1/chat/completions", headers=api_headers(),
                           json={"model": "gpt-4", "messages": [{"role": "user", "content": "Write a 1000-word essay about AI."}], "stream": True})
    prep = s.prepare_request(req)
    resp = s.send(prep, timeout=120, stream=True)
    chunk = next(resp.iter_content(1024), None)
    resp.close()
    print(color("  Stream aborted after first chunk", "yellow"))
except Exception as e:
    print(color(f"  Stream abort: {e}", "yellow"))

time.sleep(2)
hr = measure("GET", f"{BASE}/api/health", timeout=10)
hok = hr["ok"]
print(color(f"  Server healthy after abort: {hok}", "green" if hok else "red"))
add_result("Stream Interruption Recovery", 1, 1 if hok else 0, 0 if hok else 1)

# ── 5. Image Generation Stress ──
section("5. Image Generation Stress (3 sequential)")
img_ok = img_fail = 0
img_ms = []
img_errs = []
for i in range(3):
    ir = measure("POST", f"{BASE}/v1/images/generations", headers=api_headers(),
                 body={"model": "dall-e-3", "prompt": f"a solid red circle, image {i}", "n": 1, "size": "1024x1024"}, timeout=120)
    img_ms.append(ir["ms"])
    if ir["ok"]:
        img_ok += 1
    else:
        img_fail += 1
        if ir["error"]:
            img_errs.append(ir["error"])
    print(color(f"  Image {i}: status={ir['status']}, {ir['ms']:.0f} ms", "green" if ir["ok"] else "red"))
st = calc_stats(img_ms)
add_result("Image Gen x3", 3, img_ok, img_fail, st, img_errs)

# ── 6. API Key Auth Stress ──
section("6. API Key Auth Stress (100 bad keys)")
print(color("  Sending 100 requests with invalid API keys...", "yellow"))

def _bad_key(i):
    try:
        r = requests.get(f"{BASE}/v1/models", headers={"Authorization": f"Bearer m365_invalid_{i}"}, timeout=10)
        return r.status_code
    except Exception:
        return 0

rejected = accepted = other = 0
with ThreadPoolExecutor(max_workers=20) as pool:
    futs = [pool.submit(_bad_key, i) for i in range(100)]
    for f in as_completed(futs):
        s = f.result()
        if s in [401, 403]:
            rejected += 1
        elif s == 200:
            accepted += 1
        else:
            other += 1

errs = [f"{accepted} accepted with bad key!"] if accepted else []
print(color(f"  Rejected: {rejected}, Accepted: {accepted}, Other: {other}", "green" if accepted == 0 else "red"))
add_result("100 Bad API Keys", 100, rejected, accepted, errors=errs)

# ── 7. Admin Login Rate Limit ──
section("7. Admin Login Rate Limiting (5 wrong)")
login_reject = login_accept = 0
for i in range(5):
    lr = measure("POST", f"{BASE}/api/admin/login", headers={"Content-Type": "application/json"},
                 body={"password": f"wrong_pw_{i}"}, timeout=10)
    if lr["status"] in [401, 403, 429]:
        login_reject += 1
    elif lr["status"] == 200:
        login_accept += 1
    print(color(f"  Attempt {i}: status={lr['status']}", "green" if lr["status"] in [401, 403, 429] else "red"))

lock_test = measure("POST", f"{BASE}/api/admin/login", headers={"Content-Type": "application/json"},
                     body={"password": ADMIN_PW}, timeout=10)
locked = lock_test["status"] in [429, 403]
print(color(f"  Correct pw after 5 wrong: {lock_test['status']} {'(LOCKED)' if locked else '(NOT LOCKED)'}",
            "green" if locked else "yellow"))
add_result("Admin Login Rate Limit", 6, login_reject, login_accept)

if locked:
    print(color("  Waiting 30s for lockout expiry...", "yellow"))
    time.sleep(30)
    rl = measure("POST", f"{BASE}/api/admin/login", headers={"Content-Type": "application/json"},
                 body={"password": ADMIN_PW}, timeout=15)
    if rl["ok"]:
        sc = rl["headers"].get("Set-Cookie", "")
        if sc:
            session_cookie = sc.split(";")[0]
        print(color("  Re-login OK", "green"))

# ── 8. Throttling Header ──
section("8. Throttling Header Monitoring (10 requests)")
throttle_found = 0
for i in range(10):
    r = measure("POST", f"{BASE}/v1/chat/completions", headers=api_headers(),
                body={"model": "gpt-4", "messages": [{"role": "user", "content": f"Reply with just the number {i}"}], "stream": False}, timeout=120)
    has_th = False
    if r["headers"]:
        for k, v in r["headers"].items():
            if "throttl" in k.lower() or "x-m365" in k.lower():
                has_th = True
                break
    if has_th:
        throttle_found += 1
    print(f"  Req {i}: status={r['status']}, throttle={'yes' if has_th else 'no'}")
add_result("Throttling Header Monitor", 10, throttle_found, 10 - throttle_found,
           errors=[] if throttle_found else ["No throttling headers detected"])

# ── 9. New Endpoints Concurrent ──
section("9. All New Endpoints Concurrent")
endpoints = [
    ("/api/plugins", "plugins"),
    ("/api/custom-instructions", "ci"),
    ("/api/personalization-flags", "pf"),
    ("/api/ecs/designer", "designer"),
    ("/api/ecs/fluid", "fluid"),
]

def _ep(ep):
    r = measure("GET", f"{BASE}{ep[0]}", headers=api_headers(), timeout=15)
    return ep[1], r

ep_ok = ep_fail = 0
ep_errs = []
with ThreadPoolExecutor(max_workers=5) as pool:
    futs = [pool.submit(_ep, ep) for ep in endpoints]
    for f in as_completed(futs):
        name, r = f.result()
        ok_ = r["ok"] or r["status"] in [200, 404]
        if ok_:
            ep_ok += 1
        else:
            ep_fail += 1
            ep_errs.append(f"{name}: {r['error']}")
        print(color(f"  {name}: status={r['status']}, {r['ms']:.0f}ms", "green" if ok_ else "red"))
add_result("Concurrent New Endpoints", len(endpoints), ep_ok, ep_fail, errors=ep_errs)

# ── 10. FeatureFlags Toggle ──
section("10. FeatureFlags Toggle via Settings")
ff_ok = False
ff_errs = []
gs = measure("GET", f"{BASE}/api/admin/settings", headers=admin_headers(), timeout=15)
if gs["ok"]:
    try:
        settings_obj = json.loads(gs["content"])
        orig_flags = settings_obj.get("featureFlags")
        print(color(f"  GET settings OK ({gs['ms']:.0f}ms)", "green"))

        pr = measure("PATCH", f"{BASE}/api/admin/settings", headers=admin_headers(),
                     body={"featureFlags": {"testStressFlag": True}}, timeout=15)
        if pr["ok"] or pr["status"] in [200, 204]:
            print(color(f"  PATCH enable flag: {pr['status']}", "green"))
            vr = measure("GET", f"{BASE}/api/admin/settings", headers=admin_headers(), timeout=15)
            if vr["ok"]:
                ff_ok = True
                print(color("  Verify after toggle: OK", "green"))
            if orig_flags:
                measure("PATCH", f"{BASE}/api/admin/settings", headers=admin_headers(),
                        body={"featureFlags": orig_flags}, timeout=15)
        else:
            ff_errs.append(f"PATCH failed: {pr['error']}")
    except Exception as e:
        ff_errs.append(f"Parse error: {e}")
else:
    ff_errs.append("GET settings failed")
add_result("FeatureFlags Toggle", 3, 3 if ff_ok else 0, 0 if ff_ok else 3, errors=ff_errs)

# ── Summary ──
print(f"\n{'=' * 80}")
print("  STRESS TEST SUMMARY")
print(f"{'=' * 80}\n")
hdr = f"{'Test':<35} {'Total':>5} {'OK':>5} {'Fail':>5} {'Rate%':>8} {'Avg ms':>8} {'P50':>8} {'P95':>8} {'P99':>8}"
print(color(hdr, "cyan"))
print("-" * 95)
for r in all_results:
    c = "green" if r["rate"] >= 80 else "yellow" if r["rate"] >= 50 else "red"
    line = f"{r['test']:<35} {r['total']:>5} {r['ok']:>5} {r['fail']:>5} {r['rate']:>8} {r['avg']:>8.0f} {r['p50']:>8.0f} {r['p95']:>8.0f} {r['p99']:>8.0f}"
    print(color(line, c))
print("-" * 95)

failed_tests = [r for r in all_results if r["rate"] < 80]
if failed_tests:
    print(color("\n  Tests with < 80% success:", "red"))
    for ft in failed_tests:
        print(color(f"    - {ft['test']} ({ft['rate']}%)", "red"))
        for e in ft.get("errors", [])[:2]:
            print(color(f"      {e}", "red"))
print()
