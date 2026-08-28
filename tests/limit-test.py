#!/usr/bin/env python3
"""M365-Copilot2API Limit & Stability Test

Tests real-world opencode-style usage patterns:
- Progressive context growth until breakage
- Multi-turn tool call chains (read→grep→edit→bash cycles)
- Concurrent real conversations
- Measure exact breakpoint and degradation curve
"""

import sys, json, time, os, requests, statistics, textwrap
from concurrent.futures import ThreadPoolExecutor, as_completed

BASE = "http://127.0.0.1:4141"
ADMIN_PW = os.environ.get("M365_TEST_ADMIN_PASSWORD", "")
API_KEY = os.environ.get("M365_TEST_API_KEY", "")

session_cookie = None
all_results = []

TOOLS = [
    {"type": "function", "function": {
        "name": "read", "description": "Read a file",
        "parameters": {"type": "object", "properties": {"filePath": {"type": "string"}}, "required": ["filePath"]}
    }},
    {"type": "function", "function": {
        "name": "bash", "description": "Execute a command",
        "parameters": {"type": "object", "properties": {"command": {"type": "string"}}, "required": ["command"]}
    }},
    {"type": "function", "function": {
        "name": "edit", "description": "Edit a file",
        "parameters": {"type": "object", "properties": {"filePath": {"type": "string"}, "oldString": {"type": "string"}, "newString": {"type": "string"}}, "required": ["filePath", "oldString", "newString"]}
    }},
    {"type": "function", "function": {
        "name": "grep", "description": "Search code",
        "parameters": {"type": "object", "properties": {"pattern": {"type": "string"}}, "required": ["pattern"]}
    }},
    {"type": "function", "function": {
        "name": "glob", "description": "Find files",
        "parameters": {"type": "object", "properties": {"pattern": {"type": "string"}}, "required": ["pattern"]}
    }},
]


def color(t, c):
    codes = {"g": 32, "r": 31, "y": 33, "c": 36, "w": 97, "m": 35}
    return f"\033[{codes.get(c,0)}m{t}\033[0m"


def section(name):
    print(f"\n{'#'*60}\n  {name}\n{'#'*60}")


def api_headers():
    return {"Authorization": f"Bearer {API_KEY}", "Content-Type": "application/json"}


def chat(messages, tools=None, stream=False, timeout=300):
    body = {"model": "gpt-4", "messages": messages, "stream": stream}
    if tools:
        body["tools"] = tools
    t0 = time.monotonic()
    try:
        r = requests.post(f"{BASE}/v1/chat/completions", headers=api_headers(), json=body, timeout=timeout, stream=stream)
        elapsed = (time.monotonic() - t0) * 1000
        if stream:
            chunks = []
            for line in r.iter_lines():
                if line:
                    chunks.append(line)
            return {"ok": True, "status": r.status_code, "ms": elapsed, "chunks": len(chunks), "content_len": sum(len(c) for c in chunks)}
        else:
            j = r.json()
            return {"ok": r.status_code == 200, "status": r.status_code, "ms": elapsed, "json": j, "content_len": len(r.text)}
    except requests.exceptions.Timeout:
        elapsed = (time.monotonic() - t0) * 1000
        return {"ok": False, "status": 0, "ms": elapsed, "error": "TIMEOUT"}
    except Exception as e:
        elapsed = (time.monotonic() - t0) * 1000
        return {"ok": False, "status": 0, "ms": elapsed, "error": str(e)[:100]}


def build_tool_call_turns(n_rounds):
    """Build realistic opencode multi-turn tool call history."""
    messages = []
    # Simulate a large codebase exploration session
    files = [
        "internal/web/server.go", "internal/chathub/client.go", "internal/web/stream.go",
        "internal/web/images.go", "internal/web/settings.go", "internal/web/answer.go",
        "cmd/main.go", "internal/web/personalization.go", "internal/web/substrate_api.go",
    ]
    code_snippets = []
    for i in range(30):
        code_snippets.append(textwrap.dedent(f"""\
            // Code block {i}
            func handler{i}(w http.ResponseWriter, r *http.Request) {{
                ctx := r.Context()
                req, err := decodeRequest(r)
                if err != nil {{
                    http.Error(w, err.Error(), http.StatusBadRequest)
                    return
                }}
                result, err := process{i}(ctx, req)
                if err != nil {{
                    log.Printf("[handler{i}] error: %v", err)
                    http.Error(w, err.Error(), http.StatusInternalServerError)
                    return
                }}
                jsonOut(w, result)
            }}
        """))

    for round_i in range(n_rounds):
        # User asks to explore code
        messages.append({"role": "user", "content": f"I need to understand how handler{round_i % 30} works in {files[round_i % len(files)]}. Read the file and search for related patterns."})

        # Assistant responds with tool calls (2-3 per turn, like real opencode)
        tc_calls = []
        tc_results = []
        file_idx = round_i % len(files)
        tc_calls.append({"id": f"call_{round_i}_0", "type": "function", "function": {"name": "read", "arguments": json.dumps({"filePath": files[file_idx]})}})
        tc_results.append({"role": "tool", "tool_call_id": f"call_{round_i}_0", "content": code_snippets[round_i % 30]})

        tc_calls.append({"id": f"call_{round_i}_1", "type": "function", "function": {"name": "grep", "arguments": json.dumps({"pattern": f"handler{round_i % 30}"})}})
        tc_results.append({"role": "tool", "tool_call_id": f"call_{round_i}_1", "content": f"Found 5 matches in 3 files:\n{files[file_idx]}:42:func handler{round_i % 30}\n{files[(file_idx+1)%len(files)]}:88: calls handler{round_i % 30}"})

        tc_calls.append({"id": f"call_{round_i}_2", "type": "function", "function": {"name": "glob", "arguments": json.dumps({"pattern": "internal/**/*.go"})}})
        tc_results.append({"role": "tool", "tool_call_id": f"call_{round_i}_2", "content": "\n".join(files)})

        messages.append({"role": "assistant", "content": None, "tool_calls": tc_calls})
        messages.extend(tc_results)

        # Assistant summary after tool results
        messages.append({"role": "assistant", "content": f"Based on the code, handler{round_i % 30} in {files[file_idx]} decodes the request, processes it via process{round_i % 30}, and returns JSON. Error handling includes 400 and 500 responses."})

    return messages


def count_tokens_approx(messages):
    total = 0
    for m in messages:
        c = m.get("content", "")
        if c:
            total += len(c) // 4
        for tc in m.get("tool_calls", []):
            total += len(json.dumps(tc)) // 4
    return total


def print_result(name, data, extra=""):
    st = "PASS" if data["ok"] else "FAIL"
    c = "g" if data["ok"] else "r"
    err = data.get("error", "")
    detail = f"status={data['status']} {data['ms']:.0f}ms"
    if extra:
        detail += f" {extra}"
    if err:
        detail += f" err={err}"
    print(color(f"  {st} {name}: {detail}", c))


# ── 0. Login ──
section("0. Setup")
lr = requests.post(f"{BASE}/api/admin/login", json={"password": ADMIN_PW}, timeout=15)
if lr.status_code == 200:
    print(color("  Admin login OK", "g"))
else:
    print(color(f"  Admin login: {lr.status_code}", "y"))

ar = requests.get(f"{BASE}/api/accounts", cookies=lr.cookies)
try:
    acc_data = ar.json()
    accs = acc_data.get("accounts", acc_data) if isinstance(acc_data, dict) else acc_data
    online = [a for a in accs if a.get("status") == "online"] if isinstance(accs, list) else []
    print(color(f"  Accounts: {len(accs) if isinstance(accs,list) else '?'} total, {len(online)} online", "g" if online else "y"))
except Exception:
    print(color("  Accounts: parse error", "y"))

# ── 1. Progressive Context Growth ──
section("1. Progressive Context Growth — find breakpoint")
print("  Building increasingly long conversations and testing until failure...\n")

context_levels = [
    (5, "5-round tool calls"),
    (10, "10-round tool calls"),
    (20, "20-round tool calls"),
    (30, "30-round tool calls"),
    (50, "50-round tool calls"),
    (80, "80-round tool calls"),
    (100, "100-round tool calls"),
    (150, "150-round tool calls"),
]

breakpoint = None
for n_rounds, label in context_levels:
    msgs = build_tool_call_turns(n_rounds)
    approx_tokens = count_tokens_approx(msgs)
    print(f"  Testing {label} (~{approx_tokens} tokens, {len(msgs)} messages)...", end=" ", flush=True)
    r = chat(msgs, tools=TOOLS, timeout=300)
    if r["ok"]:
        print(color(f"OK {r['ms']:.0f}ms", "g"))
        all_results.append({"test": label, "ok": True, "ms": r["ms"], "tokens": approx_tokens, "rounds": n_rounds})
    else:
        err = r.get("error", f"status={r['status']}")
        print(color(f"FAIL {err} {r['ms']:.0f}ms", "r"))
        all_results.append({"test": label, "ok": False, "ms": r["ms"], "tokens": approx_tokens, "rounds": n_rounds, "error": err})
        if not breakpoint:
            breakpoint = (n_rounds, approx_tokens, err)

if breakpoint:
    print(color(f"\n  ** BREAKPOINT: {breakpoint[0]} rounds (~{breakpoint[1]} tokens) — {breakpoint[2]} **", "r"))
else:
    print(color("\n  No breakpoint found within tested range — system stable!", "g"))

# ── 2. Real Opencode Session Simulation ──
section("2. Real Opencode Session — 25-round deep code modification")

real_msgs = []
# Initial large context: system prompt + existing code
system_code = "\n".join([f"// File: internal/web/handler{i}.go\nfunc handler{i}(w http.ResponseWriter, r *http.Request) {{\n    // implementation\n    w.WriteHeader(http.StatusOK)\n}}" for i in range(20)])

real_msgs.append({"role": "user", "content": f"I'm working on M365-Copilot2API. Here's the current codebase overview:\n\n{system_code}\n\nI need to add a new endpoint /api/admin/export that exports all settings and accounts as JSON. Read the relevant files first."})

# Round 1: read server.go
real_msgs.append({"role": "assistant", "content": None, "tool_calls": [
    {"id": "c1_0", "type": "function", "function": {"name": "read", "arguments": json.dumps({"filePath": "internal/web/server.go"})}},
    {"id": "c1_1", "type": "function", "function": {"name": "glob", "arguments": json.dumps({"pattern": "internal/web/*.go"})}},
]})
real_msgs.append({"role": "tool", "tool_call_id": "c1_0", "content": "package web\n\nimport (...)\n\nfunc (s *Server) RegisterRoutes() {\n    m := http.NewServeMux()\n    m.HandleFunc(\"/api/admin/settings\", s.settingsHandler)\n    // ... 50+ routes\n}"})
real_msgs.append({"role": "tool", "tool_call_id": "c1_1", "content": "internal/web/server.go\ninternal/web/settings.go\ninternal/web/personalization.go\ninternal/web/substrate_api.go\n..."})

# Round(assistant summary
real_msgs.append({"role": "assistant", "content": "I can see server.go has 50+ routes registered. I'll add the export endpoint alongside the existing settings handler."})

# Rounds 2-10: edit cycles (simulated)
for i in range(2, 11):
    real_msgs.append({"role": "user", "content": f"Now add the export endpoint. Step {i}: implement the handler function."})
    tc_id = f"c{i}_0"
    real_msgs.append({"role": "assistant", "content": None, "tool_calls": [
        {"id": tc_id, "type": "function", "function": {"name": "edit", "arguments": json.dumps({"filePath": "internal/web/server.go", "oldString": "m.HandleFunc(\"/api/admin/settings\", s.settingsHandler)", "newString": f"m.HandleFunc(\"/api/admin/settings\", s.settingsHandler)\n    m.HandleFunc(\"/api/admin/export\", s.exportHandler)"})}}
    ]})
    real_msgs.append({"role": "tool", "tool_call_id": tc_id, "content": f"File edited successfully. Added route /api/admin/export at line {42+i}."})
    real_msgs.append({"role": "assistant", "content": f"Added the route. Now I need to implement the exportHandler function. Let me read settings.go to understand the pattern."})

# Rounds 11-20: bash + grep cycles
for i in range(11, 21):
    real_msgs.append({"role": "user", "content": f"Run tests and check for errors. Round {i}."})
    real_msgs.append({"role": "assistant", "content": None, "tool_calls": [
        {"id": f"c{i}_0", "type": "function", "function": {"name": "bash", "arguments": json.dumps({"command": "cd D:\\M365-Copilot2API-dev && go build ./..."})}},
        {"id": f"c{i}_1", "type": "function", "function": {"name": "grep", "arguments": json.dumps({"pattern": "exportHandler"})}},
    ]})
    real_msgs.append({"role": "tool", "tool_call_id": f"c{i}_0", "content": "# build output\nok  github.com/HEXUXIU/M365-Copilot2API/cmd/m365-copilot2api\nok  github.com/HEXUXIU/M365-Copilot2API/internal/web"})
    real_msgs.append({"role": "tool", "tool_call_id": f"c{i}_1", "content": f"internal/web/server.go:{42+i}: m.HandleFunc(\"/api/admin/export\", s.exportHandler)"})
    real_msgs.append({"role": "assistant", "content": f"Build passes and exportHandler is registered. All tests green."})

# Rounds 21-25: final review + long output
real_msgs.append({"role": "user", "content": "Show me the complete implementation with all changes."})
real_msgs.append({"role": "assistant", "content": None, "tool_calls": [
    {"id": "c25_0", "type": "function", "function": {"name": "read", "arguments": json.dumps({"filePath": "internal/web/server.go"})}},
]})
big_code = "\n".join([f"func (s *Server) exportHandler(w http.ResponseWriter, r *http.Request) {{\n    settings := s.settings.get()\n    accounts := s.accountStore.list()\n    jsonOut(w, map[string]any{{\"settings\": settings, \"accounts\": accounts}})\n}}" for _ in range(5)])
real_msgs.append({"role": "tool", "tool_call_id": "c25_0", "content": big_code})
real_msgs.append({"role": "assistant", "content": "Here's the complete implementation. The export endpoint at /api/admin/export returns both settings and accounts as a JSON object."})

approx = count_tokens_approx(real_msgs)
print(f"  Sending real opencode session: {len(real_msgs)} messages, ~{approx} tokens")
r = chat(real_msgs, tools=TOOLS, timeout=300)
print_result("Real opencode 25-round session", r, f"msgs={len(real_msgs)} tokens~{approx}")
all_results.append({"test": "Real opencode 25-round", "ok": r["ok"], "ms": r["ms"], "tokens": approx})

# ── 3. Pure Token Volume — no tools ──
section("3. Pure Token Volume — no tools, just long messages")

volumes = [
    (10000, "10k tokens in single message"),
    (25000, "25k tokens in single message"),
    (50000, "50k tokens in single message"),
    (100000, "100k tokens in single message"),
    (200000, "200k tokens in single message"),
]

for n_tokens, label in volumes:
    long_msg = "x " * n_tokens
    msgs = [{"role": "user", "content": long_msg}]
    print(f"  {label}...", end=" ", flush=True)
    r = chat(msgs, timeout=300)
    if r["ok"]:
        print(color(f"OK {r['ms']:.0f}ms", "g"))
    else:
        err = r.get("error", f"status={r['status']}")
        print(color(f"FAIL {err} {r['ms']:.0f}ms", "r"))
    all_results.append({"test": label, "ok": r["ok"], "ms": r["ms"], "tokens": n_tokens})

# ── 4. Concurrent Real Sessions ──
section("4. Concurrent Real Sessions — 5 parallel opencode-style conversations")

def run_real_session(idx):
    msgs = build_tool_call_turns(15)
    return idx, chat(msgs, tools=TOOLS, timeout=300)

print("  Launching 5 parallel 15-round tool-call sessions...")
t0 = time.monotonic()
results_4 = []
with ThreadPoolExecutor(max_workers=5) as pool:
    futs = [pool.submit(run_real_session, i) for i in range(5)]
    for f in as_completed(futs):
        idx, r = f.result()
        results_4.append(r)
        c = "g" if r["ok"] else "r"
        print(color(f"  Session {idx}: {'OK' if r['ok'] else 'FAIL'} {r['ms']:.0f}ms", c))

ok_4 = sum(1 for r in results_4 if r["ok"])
total_ms_4 = (time.monotonic() - t0) * 1000
print(f"  Total wall time: {total_ms_4:.0f}ms, {ok_4}/5 succeeded")
all_results.append({"test": "5x concurrent 15-round", "ok": ok_4 == 5, "ms": total_ms_4})

# ── 5. Stream with Massive Context ──
section("5. Stream with Massive Context — 30-round tool calls + stream")

msgs_5 = build_tool_call_turns(30)
approx_5 = count_tokens_approx(msgs_5)
print(f"  30-round tool calls (~{approx_5} tokens), streaming...")
r = chat(msgs_5, tools=TOOLS, stream=True, timeout=300)
if r["ok"]:
    print(color(f"  OK: {r['chunks']} chunks, {r['content_len']} bytes, {r['ms']:.0f}ms", "g"))
else:
    print(color(f"  FAIL: {r.get('error', r['status'])} {r['ms']:.0f}ms", "r"))
all_results.append({"test": "Stream 30-round tools", "ok": r["ok"], "ms": r["ms"], "tokens": approx_5})

# ── 6. Single Account Load — all requests through one account ──
section("6. Single Account Saturation — 10 sequential requests")

print("  Sending 10 sequential chat requests (all hit same account)...")
times_6 = []
ok_6 = 0
for i in range(10):
    msgs = [{"role": "user", "content": f"Request {i}: Analyze this Go code and suggest improvements. " + "package main\nfunc main() {}\n" * 50}]
    r = chat(msgs, timeout=120)
    times_6.append(r["ms"])
    if r["ok"]:
        ok_6 += 1
    print(f"  Req {i}: {'OK' if r['ok'] else 'FAIL'} {r['ms']:.0f}ms")

if times_6:
    st = {"avg": round(statistics.mean(times_6)), "p50": sorted(times_6)[len(times_6)//2], "max": max(times_6), "min": min(times_6)}
    print(f"  {ok_6}/10 OK, avg={st['avg']}ms, p50={st['p50']}ms, min={st['min']}ms, max={st['max']}ms")
all_results.append({"test": "Single-acc 10x sequential", "ok": ok_6 == 10, "ms": statistics.mean(times_6) if times_6 else 0})

# ── Summary ──
print(f"\n{'='*70}")
print("  LIMIT & STABILITY TEST SUMMARY")
print(f"{'='*70}\n")

for r in all_results:
    c = "g" if r["ok"] else "r"
    tok = f" ~{r['tokens']}tok" if "tokens" in r else ""
    print(color(f"  {'PASS' if r['ok'] else 'FAIL'}5 {r['test']}{tok} {r['ms']:.0f}ms" + (f" err={r['error']}" if r.get("error") else ""), c))

if breakpoint:
    print(color(f"\n  ** CONTEXT BREAKPOINT: ~{breakpoint[1]} tokens ({breakpoint[0]} tool rounds) — {breakpoint[2]} **", "r"))
else:
    print(color("\n  No breakpoint found — system handles all tested loads", "g"))
