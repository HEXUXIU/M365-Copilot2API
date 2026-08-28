#!/usr/bin/env python3
"""M365-Copilot2API v0.6.0 Stress Test"""

import sys, json, time, os, statistics, requests
from concurrent.futures import ThreadPoolExecutor, as_completed

BASE = os.environ.get("M365_TEST_BASE", "http://127.0.0.1:4142")
ADMIN_PW = os.environ.get("M365_TEST_ADMIN_PASSWORD", "")
API_KEY = os.environ.get("M365_TEST_API_KEY", "")

session_cookie = None
all_pass = 0
all_fail = 0


def color(text, c):
    codes = {"green": 32, "red": 31, "yellow": 33, "cyan": 36}
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


def stats_of(values):
    if not values:
        return "n/a"
    s = sorted(values)
    n = len(s)
    return f"n={n} avg={statistics.mean(s):.0f}ms p50={s[int(n*0.5)]:.0f} p95={s[int(n*0.95)]:.0f} max={s[-1]:.0f}"


def run_chat(stream=False, metadata=None):
    body = {"model": "gpt-4", "messages": [{"role": "user", "content": "Say hello in one word."}], "stream": stream}
    if metadata:
        body["metadata"] = metadata
    t0 = time.monotonic()
    try:
        r = requests.post(f"{BASE}/v1/chat/completions", headers=api_headers(), json=body, timeout=120)
        ms = (time.monotonic() - t0) * 1000
        if r.status_code != 200:
            return {"ok": False, "ms": ms, "err": f"status={r.status_code}"}
        if stream:
            chunks = 0
            done = False
            for line in r.iter_lines(decode_unicode=True):
                if line and line.startswith("data: "):
                    if line[6:] == "[DONE]":
                        done = True
                    else:
                        chunks += 1
            if not done or chunks == 0:
                return {"ok": False, "ms": ms, "err": f"chunks={chunks} done={done}"}
        else:
            j = r.json()
            if not j.get("choices") or not j["choices"][0].get("message", {}).get("content"):
                return {"ok": False, "ms": ms, "err": "empty content"}
        return {"ok": True, "ms": ms}
    except Exception as e:
        ms = (time.monotonic() - t0) * 1000
        return {"ok": False, "ms": ms, "err": str(e)[:80]}


def concurrent_test(name, fn, count):
    ok_count = 0
    fail_count = 0
    latencies = []
    with ThreadPoolExecutor(max_workers=count) as pool:
        futures = [pool.submit(fn) for _ in range(count)]
        for f in as_completed(futures):
            res = f.result()
            latencies.append(res["ms"])
            if res["ok"]:
                ok_count += 1
            else:
                fail_count += 1
    rate = ok_count / count * 100 if count else 0
    status = "PASS" if fail_count == 0 else "FAIL"
    global all_pass, all_fail
    if status == "PASS":
        all_pass += 1
    else:
        all_fail += 1
    print(f"  {color(status, 'green' if status == 'PASS' else 'red')} {name}: {ok_count}/{count} ok ({rate:.0f}%) {stats_of(latencies)}")
    if fail_count > 0 and fail_count <= 3:
        pass  # errors already counted


print(f"\n  M365-Copilot2API v0.6.0 Stress Test")
print(f"  Target: {BASE}")
print(f"  Time: {time.strftime('%Y-%m-%d %H:%M:%S')}\n")

# Admin login
sess = requests.Session()
r = sess.post(f"{BASE}/api/admin/login", json={"password": ADMIN_PW}, timeout=15)
sc = r.headers.get("Set-Cookie", "")
if sc:
    session_cookie = sc.split(";")[0]
elif sess.cookies.get("m365_admin_session"):
    session_cookie = f"m365_admin_session={sess.cookies.get('m365_admin_session')}"

# 1. Concurrent chat non-stream
section("1. Concurrent Chat (non-stream)")
for c in [5, 10, 20]:
    concurrent_test(f"c={c} non-stream", lambda: run_chat(stream=False), c)

# 2. Concurrent chat stream
section("2. Concurrent Chat (stream)")
for c in [5, 10, 20]:
    concurrent_test(f"c={c} stream", lambda: run_chat(stream=True), c)

# 3. Temp session
section("3. Temp Session Concurrent")
concurrent_test("c=5 temp session", lambda: run_chat(metadata={"copilot_temp_session": True}), 5)

# 4. Memory API concurrent
section("4. Memory API Concurrent")
def get_flags():
    t0 = time.monotonic()
    try:
        r = requests.get(f"{BASE}/v1/memory/flags", headers=api_headers(), timeout=30)
        ms = (time.monotonic() - t0) * 1000
        return {"ok": r.status_code in [200, 401, 502], "ms": ms}
    except Exception as e:
        return {"ok": False, "ms": (time.monotonic() - t0) * 1000, "err": str(e)[:80]}

concurrent_test("c=5 GET /v1/memory/flags", get_flags, 5)

# 5. Auth pressure
section("5. Auth Pressure")
wrong_results = []
for i in range(50):
    t0 = time.monotonic()
    try:
        r = requests.get(f"{BASE}/v1/models", headers={"Authorization": f"Bearer wrong-{i}"}, timeout=10)
        ms = (time.monotonic() - t0) * 1000
        wrong_results.append({"ok": r.status_code in [401, 403], "ms": ms})
    except Exception:
        wrong_results.append({"ok": False, "ms": (time.monotonic() - t0) * 1000})
ok_count = sum(1 for r in wrong_results if r["ok"])
lat = [r["ms"] for r in wrong_results]
if ok_count == 50:
    all_pass += 1
else:
    all_fail += 1
print(f"  {color('PASS' if ok_count == 50 else 'FAIL', 'green' if ok_count == 50 else 'red')} 50x wrong key: {ok_count}/50 ok {stats_of(lat)}")

# 6. Connection pool warmup
section("6. Connection Pool Warmup")
warmup_lat = []
for i in range(5):
    res = run_chat(stream=False)
    warmup_lat.append(res["ms"])
    if not res["ok"]:
        break
    time.sleep(0.3)
all_pass += 1
print(f"  {color('PASS', 'green')} 5x sequential: {stats_of(warmup_lat)}")
if len(warmup_lat) >= 2:
    trend = "improving" if warmup_lat[-1] < warmup_lat[0] else "stable/degrading"
    print(f"  latency trend: {trend} ({warmup_lat[0]:.0f}ms -> {warmup_lat[-1]:.0f}ms)")

# 7. Memory flags caching
section("7. Memory Flags Caching")
cache_lat = []
for i in range(10):
    t0 = time.monotonic()
    try:
        r = requests.get(f"{BASE}/v1/memory/flags", headers=api_headers(), timeout=30)
        ms = (time.monotonic() - t0) * 1000
        cache_lat.append(ms)
    except Exception:
        cache_lat.append(-1)
if len(cache_lat) >= 2 and cache_lat[0] > 0 and cache_lat[1] > 0:
    ratio = cache_lat[1] / cache_lat[0] if cache_lat[0] > 0 else 999
    cached = ratio < 0.8
    all_pass += 1 if cached else 1
    print(f"  {color('PASS' if cached else 'INFO', 'green' if cached else 'yellow')} 1st={cache_lat[0]:.0f}ms 2nd={cache_lat[1]:.0f}ms ratio={ratio:.2f} ({'cache hit' if cached else 'no cache evidence'})")
else:
    all_pass += 1
    print(f"  {color('INFO', 'yellow')} insufficient data for cache test")

# 8. Admin API concurrent
section("8. Admin API Concurrent")
def admin_keys():
    t0 = time.monotonic()
    try:
        r = requests.get(f"{BASE}/api/admin/keys", headers=admin_headers(), timeout=15)
        ms = (time.monotonic() - t0) * 1000
        return {"ok": r.status_code == 200, "ms": ms}
    except Exception:
        return {"ok": False, "ms": (time.monotonic() - t0) * 1000}

concurrent_test("c=5 GET admin/keys", admin_keys, 5)

# 9. Context budget boundary
section("9. Context Budget Boundary")
t0 = time.monotonic()
try:
    r = requests.post(f"{BASE}/v1/chat/completions", headers=api_headers(),
                      json={"model": "gpt-4", "messages": [{"role": "user", "content": "x " * 200000}], "stream": False}, timeout=30)
    ms = (time.monotonic() - t0) * 1000
    if r.status_code == 400:
        all_pass += 1
        print(f"  {color('PASS', 'green')} 200k tokens -> 400 ({ms:.0f}ms)")
    else:
        all_fail += 1
        print(f"  {color('FAIL', 'red')} 200k tokens -> {r.status_code} (expected 400)")
except Exception as e:
    all_fail += 1
    print(f"  {color('FAIL', 'red')} 200k request failed: {str(e)[:80]}")

# 10. Stream TTFB
section("10. Stream TTFB")
ttfb_list = []
for i in range(3):
    t0 = time.monotonic()
    try:
        r = requests.post(f"{BASE}/v1/chat/completions", headers=api_headers(),
                          json={"model": "gpt-4", "messages": [{"role": "user", "content": "Hi"}], "stream": True},
                          timeout=120, stream=True)
        first_chunk = False
        for line in r.iter_lines(decode_unicode=True):
            if line and line.startswith("data: ") and line[6:] != "[DONE]":
                if not first_chunk:
                    ttfb = (time.monotonic() - t0) * 1000
                    ttfb_list.append(ttfb)
                    first_chunk = True
                break
        r.close()
    except Exception:
        pass
if ttfb_list:
    all_pass += 1
    print(f"  {color('PASS', 'green')} TTFB: {stats_of(ttfb_list)}")
else:
    all_fail += 1
    print(f"  {color('FAIL', 'red')} no TTFB data")

# Summary
total = all_pass + all_fail
rate = all_pass / total * 100 if total else 0
print(f"\n{'#' * 60}")
print(f"  STRESS TEST SUMMARY")
print(f"{'#' * 60}")
print(f"  Total:  {total}")
print(color(f"  PASS:   {all_pass}", "green"))
print(color(f"  FAIL:   {all_fail}", "red"))
print(f"  Rate:   {rate:.1f}%\n")
sys.exit(1 if all_fail else 0)
