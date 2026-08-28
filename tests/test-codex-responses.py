"""Deep test suite for POST /v1/responses (Codex CLI compat layer).

Expected behaviors are asserted against source of truth:
- internal/web/protocol_handlers.go : responses handler; CAS replay => 409,
  unknown previous_response_id => 400, empty input => 400.
- internal/web/codex_responses.go   : non-stream shape id=resp_*, status=completed.
- internal/web/stream.go            : SSE frames are "event: <name>\ndata: <json>".

Run: python tests/test-codex-responses.py
Exit code: 0 all pass, 1 any failure.
"""
import json
import os
import sys

import requests

if hasattr(sys.stdout, "reconfigure"):
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    sys.stderr.reconfigure(encoding="utf-8", errors="replace")

BASE = os.environ.get("M365_TEST_BASE", "http://127.0.0.1:4142")
API_KEY = os.environ.get("M365_TEST_API_KEY", "")
MODEL = "gpt-5-codex"
TIMEOUT = 120

os.system("")  # enable ANSI escape handling on Windows terminals
GREEN, RED, YELLOW, CYAN, RESET = "92", "91", "93", "96", "0"

RESULTS = []
ROUND2_PAYLOAD = None


def paint(text, code):
    return f"\033[{code}m{text}\033[0m"


def record(tag, ok, detail=""):
    RESULTS.append((tag, ok, detail))
    mark = paint("PASS", GREEN) if ok else paint("FAIL", RED)
    print(f"  [{mark}] {tag}" + (f" - {detail}" if detail else ""))


def warn(msg):
    print(f"  {paint('WARN', YELLOW)} {msg}")


def diag(r):
    body = r.text if r is not None else ""
    return f"status={getattr(r, 'status_code', '?')} body={body[:500]!r}"


def post(path, payload, auth=True):
    headers = {"Content-Type": "application/json"}
    if auth:
        headers["Authorization"] = f"Bearer {API_KEY}"
    return requests.post(BASE + path, json=payload, headers=headers, timeout=TIMEOUT)


def resp_text(d):
    parts = []
    for item in d.get("output", []) or []:
        if not isinstance(item, dict) or item.get("type") != "message":
            continue
        for blk in item.get("content", []) or []:
            if isinstance(blk, dict) and blk.get("type") == "output_text":
                parts.append(blk.get("text", "") or "")
    return "".join(parts)


def parse_sse(resp):
    events = []
    name = None
    for raw in resp.iter_lines(decode_unicode=True):
        if raw is None:
            continue
        line = raw.rstrip("\r")
        if line == "":
            name = None
            continue
        if line.startswith("event:"):
            name = line[len("event:"):].strip()
            continue
        if line.startswith("data:"):
            data = line[len("data:"):].strip()
            try:
                obj = json.loads(data)
            except ValueError:
                obj = None
            events.append((name if name is not None else (obj or {}).get("type"), obj))
    return events


def case_a_basic_non_stream():
    print(paint("\n[A] Basic non-stream request", CYAN))
    r = post("/v1/responses", {"model": MODEL, "input": "Reply with exactly: PONG"})
    assert r.status_code == 200, diag(r)
    d = r.json()
    assert str(d.get("id", "")).startswith("resp_"), f"id={d.get('id')!r} not resp_*"
    assert d.get("status") == "completed", f"status={d.get('status')!r}"
    messages = [o for o in d.get("output", []) if isinstance(o, dict) and o.get("type") == "message"]
    assert messages, f"no type=message output; types={[o.get('type') for o in d.get('output', [])]}"
    text = resp_text(d)
    assert "pong" in text.lower(), f"text={text[:300]!r} lacks PONG"
    assert d.get("usage"), f"usage missing: keys={sorted(d.keys())}"
    record("A basic_non_stream", True, f"text={text.strip()[:60]!r}")


def case_b_stream_sse():
    print(paint("\n[B] Streaming SSE", CYAN))
    r = post("/v1/responses", {"model": MODEL, "input": "Reply with exactly: PONG", "stream": True})
    assert r.status_code == 200, diag(r)
    ctype = r.headers.get("Content-Type", "")
    assert "text/event-stream" in ctype, f"Content-Type={ctype!r}"
    events = parse_sse(r)
    types = [t for t, _ in events]
    assert any(t == "response.created" for t in types), f"no response.created; got {types[:10]}"
    deltas = [obj.get("delta", "") for t, obj in events
              if t == "response.output_text.delta" and isinstance(obj, dict)]
    assert deltas, f"no output_text.delta events; event types={types}"
    joined = "".join(deltas)
    assert joined.strip(), f"delta concatenation empty"
    terminal = [t for t in types if t and "completed" in t]
    assert terminal, f"no terminal *completed* event; tail={types[-5:]}"
    record("B stream_sse", True, f"{len(events)} events, terminal={terminal[-1]}")


def case_c_previous_response_id_chain():
    print(paint("\n[C] previous_response_id chained conversation", CYAN))
    r1 = post("/v1/responses", {"model": MODEL, "input": "My favorite number is 42. Just acknowledge."})
    assert r1.status_code == 200, diag(r1)
    rid = r1.json().get("id")
    assert rid, f"no id in first response: {str(r1.text)[:300]}"
    payload = {
        "model": MODEL,
        "previous_response_id": rid,
        "input": "What is my favorite number? Reply with just the number.",
    }
    # Register before asserting so D can still verify replay protection even
    # if the model's answer below is wrong (the id is consumed server-side
    # regardless of reply content).
    global ROUND2_PAYLOAD
    ROUND2_PAYLOAD = payload
    r2 = post("/v1/responses", payload)
    assert r2.status_code == 200, diag(r2)
    text = resp_text(r2.json())
    assert "42" in text, f"reply lacks 42: {text[:300]!r}"
    record("C chain_previous_id", True, f"parent={rid} reply={text.strip()[:40]!r}")


def case_d_replay_protection(round2_payload):
    # protocol_handlers.go marks prior.Consumed on use and answers a replay
    # with 409 conflict ("previous_response_id already consumed").
    print(paint("\n[D] Replay protection (CAS, expect 409 per source)", CYAN))
    r = post("/v1/responses", round2_payload)
    assert r.status_code == 409, f"expected 409 conflict, got {diag(r)}"
    record("D replay_protection_409", True, f"body={r.text[:120]!r}")


def case_e_unknown_previous_id():
    print(paint("\n[E] Unknown previous_response_id", CYAN))
    r = post("/v1/responses", {
        "model": MODEL,
        "previous_response_id": "resp_nonexistent123",
        "input": "hi",
    })
    assert r.status_code == 400, diag(r)
    record("E unknown_previous_id_400", True, f"body={r.text[:120]!r}")


def case_f_tool_call_roundtrip():
    print(paint("\n[F] Tool call full roundtrip", CYAN))
    tools = [{
        "type": "function",
        "name": "get_weather",
        "description": "Get current weather",
        "parameters": {
            "type": "object",
            "properties": {"city": {"type": "string"}},
            "required": ["city"],
        },
    }]
    r1 = post("/v1/responses", {
        "model": MODEL,
        "input": "What is the weather in Paris? You must call the tool.",
        "tools": tools,
        "tool_choice": "auto",
    })
    assert r1.status_code == 200, diag(r1)
    d1 = r1.json()
    calls = [o for o in d1.get("output", []) if isinstance(o, dict) and o.get("type") == "function_call"]
    assert calls, (
        f"no function_call item; types={[o.get('type') if isinstance(o, dict) else '?' for o in d1.get('output', [])]} "
        f"body={r1.text[:500]!r}"
    )
    fc = calls[0]
    call_id = fc.get("call_id") or ""
    assert call_id, f"function_call without call_id: {fc}"
    assert fc.get("name") == "get_weather", f"name={fc.get('name')!r}"
    args_raw = str(fc.get("arguments", ""))
    assert "paris" in args_raw.lower(), f"arguments lack Paris: {args_raw!r}"
    assert fc.get("status") == "completed" or d1.get("status") == "completed", \
        f"not completed: item={fc.get('status')} resp={d1.get('status')}"
    record("F1 tool_call_emitted", True, f"call_id={call_id} args={args_raw[:60]}")

    r2 = post("/v1/responses", {
        "model": MODEL,
        "previous_response_id": d1["id"],
        "input": [{
            "type": "function_call_output",
            "call_id": call_id,
            "output": json.dumps({"temp_c": 18, "condition": "cloudy"}),
        }],
    })
    assert r2.status_code == 200, diag(r2)
    text = resp_text(r2.json())
    lowered = text.lower()
    assert any(k in lowered for k in ("18", "cloudy", "paris")), \
        f"final answer lacks weather info: {text[:300]!r}"
    record("F2 tool_output_accepted", True, f"reply={text.strip()[:80]!r}")


def case_g_codex_input_array():
    print(paint("\n[G] Codex CLI style input array", CYAN))
    r = post("/v1/responses", {
        "model": MODEL,
        "input": [{"role": "user", "content": [{"type": "input_text", "text": "Reply OK"}]}],
    })
    assert r.status_code == 200, diag(r)
    text = resp_text(r.json())
    assert text.strip(), f"empty reply: {r.text[:400]!r}"
    record("G codex_input_array", True, f"reply={text.strip()[:60]!r}")


def case_h_instructions():
    print(paint("\n[H] instructions injection", CYAN))
    r = post("/v1/responses", {
        "model": MODEL,
        "instructions": "You are a pirate. Always say Arrr.",
        "input": "hello",
    })
    assert r.status_code == 200, diag(r)
    text = resp_text(r.json())
    assert text.strip(), f"empty reply: {r.text[:400]!r}"
    if not any(k in text.lower() for k in ("arrr", "ahoy")):
        warn(f"instructions persona not reflected loosely (non-blocking): {text[:120]!r}")
    record("H instructions", True, f"reply={text.strip()[:60]!r}")


def case_i_error_paths():
    print(paint("\n[I] Error paths", CYAN))

    r = post("/v1/responses", {"model": MODEL, "input": ""})
    assert r.status_code == 400, f"empty input expected 400, got {diag(r)}"
    record("I1 empty_input_400", True)

    r = post("/v1/responses", {"model": MODEL})
    assert r.status_code == 400, f"missing input expected 400, got {diag(r)}"
    record("I2 missing_input_400", True)

    r = requests.post(
        BASE + "/v1/responses",
        json={"model": MODEL, "input": "hi"},
        headers={"Content-Type": "application/json"},
        timeout=TIMEOUT,
    )
    assert r.status_code == 401, f"no auth expected 401, got {diag(r)}"
    record("I3 no_auth_401", True)


def case_j_regression():
    print(paint("\n[J] Regression: other protocol surfaces", CYAN))

    r = post("/v1/chat/completions", {
        "model": "gpt-4",
        "messages": [{"role": "user", "content": "Reply PONG"}],
        "stream": False,
    })
    assert r.status_code == 200, diag(r)
    d = r.json()
    choices = d.get("choices")
    assert choices, f"no choices in chat/completions response: {r.text[:400]!r}"
    record("J1 chat_completions", True)

    r = post("/v1/messages", {
        "model": "claude-3-5-sonnet-20241022",
        "max_tokens": 50,
        "messages": [{"role": "user", "content": "Reply PONG"}],
    })
    assert r.status_code in (200, 404, 501), f"/v1/messages unexpected {diag(r)}"
    detail = "accepted per spec (200/404/501)"
    if r.status_code == 200:
        content = r.json().get("content")
        assert content, f"200 but no content: {r.text[:400]!r}"
        detail = "200 with content blocks"
    record("J2 anthropic_messages", True, f"status={r.status_code} ({detail})")


def main():
    print(paint("=" * 72, CYAN))
    print(paint(f"Codex /v1/responses deep test suite -> {BASE}", CYAN))
    print(paint("=" * 72, CYAN))

    cases = [
        ("A basic non-stream", case_a_basic_non_stream),
        ("B streaming SSE", case_b_stream_sse),
        ("C previous_response_id chain", case_c_previous_response_id_chain),
        ("E unknown previous_response_id", case_e_unknown_previous_id),
        ("F tool call roundtrip", case_f_tool_call_roundtrip),
        ("G Codex input array", case_g_codex_input_array),
        ("H instructions", case_h_instructions),
        ("I error paths", case_i_error_paths),
        ("J regression", case_j_regression),
    ]

    for name, fn in cases:
        try:
            fn()
        except AssertionError as exc:
            record(name, False, str(exc)[:500])
        except Exception as exc:  # noqa: BLE001
            record(name, False, f"{type(exc).__name__}: {exc}")

    # D depends on C's consumed state and must run after C.
    try:
        if ROUND2_PAYLOAD is None:
            raise RuntimeError("skipped: C did not produce a round-2 payload")
        case_d_replay_protection(ROUND2_PAYLOAD)
    except AssertionError as exc:
        record("D replay_protection_409", False, str(exc)[:500])
    except Exception as exc:  # noqa: BLE001
        record("D replay_protection_409", False, f"{type(exc).__name__}: {exc}")

    print(paint("\n" + "=" * 72, CYAN))
    print(paint("SUMMARY", CYAN))
    print(paint("=" * 72, CYAN))
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    failed = len(RESULTS) - passed
    width = max(len(tag) for tag, _, _ in RESULTS)
    for tag, ok, detail in RESULTS:
        mark = paint("PASS", GREEN) if ok else paint("FAIL", RED)
        line = f"  {tag.ljust(width)}  {mark}"
        if not ok and detail:
            line += f"  | {detail[:160]}"
        print(line)
    print(paint("-" * 72, CYAN))
    total_color = GREEN if failed == 0 else RED
    print(paint(f"  TOTAL: {len(RESULTS)}  PASS: {passed}  FAIL: {failed}", total_color))
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys_exit_code = main()
    raise SystemExit(sys_exit_code)
