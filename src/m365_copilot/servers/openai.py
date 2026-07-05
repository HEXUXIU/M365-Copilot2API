import json, os, sys, asyncio, time, uuid, logging, http.server, socketserver, threading, hashlib, re
from functools import lru_cache

from .. import __version__
from ..auth import TokenManager
from ..client import M365Client
from ..models import MODELS, lookup_model, TENANT_ID, USER_OID, CLIENT_ID, SCOPE
from ..tools import provider, ToolCallDetector
from ..tools.detector import detect_tool_intent, extract_tool_args
from ..scripts.plugin_loader import load_user_tools

logging.basicConfig(level=logging.INFO, format="%(asctime)s [%(levelname)s] %(message)s")
load_user_tools()

MAX_TOOL_ROUNDS = 3

BASE_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
RT_FILE = os.path.join(BASE_DIR, "data", "tokens", "rt_90day.txt")
CACHE_FILE = os.path.join(BASE_DIR, "data", "tokens", "token_cache.json")
CONTEXT_CACHE_DIR = os.path.join(BASE_DIR, "data", "cache")

ANTHROPIC_MODEL_MAP = {
    "claude-sonnet-4-20250514": "claude",
    "claude-3.5-sonnet": "claude",
    "gpt-5.5": "gpt5.5",
    "gpt-5.4": "gpt5.4",
    "gpt-5.3": "gpt5.3",
    "gpt-5.2": "gpt5.2",
    "gpt-4o": "auto",
    "gpt-4": "auto",
}


def _resolve_anthropic_model(anthropic_id):
    if anthropic_id in ANTHROPIC_MODEL_MAP:
        return ANTHROPIC_MODEL_MAP[anthropic_id]
    for prefix, mapped in ANTHROPIC_MODEL_MAP.items():
        if anthropic_id.startswith(prefix):
            return mapped
    return "auto"


class ContextCache:
    ctx_cache_lock = threading.Lock()

    def __init__(self, cache_dir, maxsize=256):
        self.cache_dir = cache_dir
        os.makedirs(cache_dir, exist_ok=True)
        self._mem = {}
        self._order = []
        self._maxsize = maxsize

    def _evict(self):
        while len(self._order) > self._maxsize:
            old = self._order.pop(0)
            self._mem.pop(old, None)

    def _path(self, key):
        safe = hashlib.md5(key.encode()).hexdigest()
        return os.path.join(self.cache_dir, f"{safe}.json")

    def get(self, key):
        with self.ctx_cache_lock:
            if key in self._mem:
                self._order.remove(key)
                self._order.append(key)
                return self._mem[key]
        p = self._path(key)
        if os.path.exists(p):
            with open(p) as f:
                data = json.load(f)
            with self.ctx_cache_lock:
                self._mem[key] = data
                self._order.append(key)
                self._evict()
            return data
        return None

    def set(self, key, value):
        with self.ctx_cache_lock:
            self._mem[key] = value
            if key in self._order:
                self._order.remove(key)
            self._order.append(key)
            self._evict()
        p = self._path(key)
        with open(p, "w") as f:
            json.dump(value, f, ensure_ascii=False)

    def pop(self, key):
        with self.ctx_cache_lock:
            self._mem.pop(key, None)
            if key in self._order:
                self._order.remove(key)
        p = self._path(key)
        if os.path.exists(p):
            os.remove(p)


def sse_msg(data, chunk_id=None, model="gpt-4"):
    if chunk_id is None:
        chunk_id = f"chatcmpl-{uuid.uuid4().hex}"
    chunk = {
        "id": chunk_id, "object": "chat.completion.chunk",
        "created": int(time.time()), "model": model,
        "choices": [{"index": 0, "delta": data, "finish_reason": None}],
    }
    return f"data: {json.dumps(chunk, ensure_ascii=False)}\n\n"


def sse_done(chunk_id=None, model="gpt-4", usage=None, reasoning=None):
    if chunk_id is None:
        chunk_id = f"chatcmpl-{uuid.uuid4().hex}"
    chunk = {
        "id": chunk_id, "object": "chat.completion.chunk",
        "created": int(time.time()), "model": model,
        "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
    }
    if usage:
        chunk["usage"] = usage
    out = f"data: {json.dumps(chunk, ensure_ascii=False)}\n\n"
    out += "data: [DONE]\n\n"
    return out


def inject_tools_prompt(messages, tools):
    tools_json = []
    for t in tools:
        fn = t.get("function", t)
        tools_json.append({
            "name": fn.get("name", "unknown"),
            "description": fn.get("description", ""),
            "parameters": fn.get("parameters", {}),
        })
    prompt = (
        f"You have access to these tools:\n{json.dumps(tools_json, ensure_ascii=False)}\n\n"
        'When you use a tool, respond with ONLY: {"name": "<tool_name>", "arguments": {<args>}}\n'
    )
    for m in reversed(messages):
        if m.get("role") == "user":
            content = m.get("content", "")
            if isinstance(content, list):
                for part in content:
                    if part.get("type") == "text":
                        part["text"] = prompt + part["text"]
            else:
                m["content"] = prompt + content
            break


def inject_json_mode(messages):
    for m in messages:
        if m.get("role") == "system":
            existing = m.get("content", "")
            m["content"] = (existing + "\n" if existing else "") + (
                "You MUST respond with valid JSON only. "
                "Do not include markdown code blocks, explanation, or any text outside the JSON object."
            )
            return
    messages.insert(0, {
        "role": "system",
        "content": "You MUST respond with valid JSON only. Do not include markdown code blocks, explanation, or any text outside the JSON object."
    })


def fim_to_chat(prompt, suffix=None):
    if suffix:
        return [
            {"role": "user", "content": f"Complete the middle of the following text naturally.\n\n--- BEGIN TEXT ---\n{prompt}\n--- MIDDLE ---\n{suffix}\n--- END ---\n\nWrite only the middle part that connects the two sections."}
        ]
    return [
        {"role": "user", "content": f"Continue writing from this point:\n\n{prompt}"}
    ]


_client_lock = threading.Lock()
_loop = None
_tm = None
_client = None


def _get_loop():
    try:
        return asyncio.get_event_loop()
    except RuntimeError:
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        return loop


def _get_client():
    global _tm, _client
    if _client is None:
        with _client_lock:
            if _client is None:
                _tm = TokenManager(TENANT_ID, CLIENT_ID, SCOPE, RT_FILE, CACHE_FILE)
                _client = M365Client(_tm)
    return _client


def _run_async(coro):
    return _get_loop().run_until_complete(coro)


def _execute_tool(name, args):
    tool = provider.tools.get(name)
    if not tool:
        return f"Error: unknown tool '{name}'"
    try:
        return str(tool.execute(**(args or {})))
    except Exception as e:
        return f"Error: {e}"


class OpenAIHandler(http.server.BaseHTTPRequestHandler):
    ctx_cache = ContextCache(CONTEXT_CACHE_DIR)
    _msg_prefix_map: dict[str, str] = {}  # message_prefix_hash → session_id

    def _compute_prefix_key(self, messages):
        """Hash all messages except the last to create a conversation fingerprint."""
        if len(messages) <= 1:
            return None
        prefix = messages[:-1]
        data = json.dumps(
            [{"role": m.get("role"), "content": str(m.get("content", ""))[:300]} for m in prefix],
            sort_keys=True, ensure_ascii=False,
        )
        return hashlib.sha256(data.encode()).hexdigest()[:16]

    def log_message(self, format, *args):
        logging.info(f"{self.client_address[0]} - {format % args}")

    def _send_json(self, code, data):
        body = json.dumps(data, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()
        self.wfile.write(body)

    def _send_error(self, code, msg):
        self._send_json(code, {"error": {"message": msg, "type": "error", "code": code}})

    def _read_body(self):
        length = int(self.headers.get("Content-Length", 0))
        return json.loads(self.rfile.read(length)) if length else {}

    def _parse_params(self, req):
        model = req.get("model", "auto")
        messages = req.get("messages", [])
        stream = bool(req.get("stream", False))
        tools = req.get("tools", None)
        response_format = req.get("response_format", None) or {}

        cfg = lookup_model(model)
        if not cfg:
            return self._send_error(400, f"Unknown model: {model}")
        return model, messages, stream, tools, response_format, cfg

    def _get_session_id(self, req):
        sid = self.headers.get("X-Session-Id", "")
        if not sid and "session_id" in req:
            sid = req["session_id"]
        if not sid and "user" in req:
            sid = req["user"]
        return sid or None

    # ---- HTTP routing ----
    def do_GET(self):
        if self.path == "/v1/models":
            models = [{"id": v["openai_id"], "object": "model", "created": 1700000000, "owned_by": "microsoft"} for v in MODELS.values()]
            self._send_json(200, {"object": "list", "data": models})
        elif self.path == "/health":
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.end_headers()
            self.wfile.write(b"OK")
        else:
            self._send_error(404, "Not found")

    def do_OPTIONS(self):
        self.send_response(200)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Session-Id")
        self.end_headers()

    def do_POST(self):
        path = self.path.rstrip("/")
        try:
            req = self._read_body()
        except Exception as e:
            self._send_error(400, f"Invalid JSON: {e}")
            return

        if path == "/v1/chat/completions":
            self._handle_chat(req)
        elif path == "/v1/completions":
            self._handle_completions(req)
        elif path == "/v1/messages":
            self._handle_anthropic_messages(req)
        elif path == "/v1/complete":
            self._handle_anthropic_complete(req)
        else:
            self._send_error(404, f"Not found: {self.path}")

    # ---- OpenAI Chat Completions ----
    def _handle_chat(self, req):
        parsed = self._parse_params(req)
        if parsed is None:
            return
        model, messages, stream, tools, response_format, cfg = parsed

        if response_format.get("type") == "json_object":
            inject_json_mode(messages)

        client = _get_client()

        session_id = self._get_session_id(req)
        conv_id = None
        if session_id:
            cached = self.ctx_cache.get(f"session:{session_id}")
            if cached:
                conv_id = cached.get("conversation_id")
            if not conv_id:
                conv_id = uuid.uuid4().hex
                self.ctx_cache.set(f"session:{session_id}", {"conversation_id": conv_id})
        else:
            # Message-prefix fallback: auto-match by history hash
            prefix_key = self._compute_prefix_key(messages)
            if prefix_key and prefix_key in self._msg_prefix_map:
                sid = self._msg_prefix_map[prefix_key]
                cached = self.ctx_cache.get(f"session:{sid}")
                if cached:
                    conv_id = cached.get("conversation_id")
            if not conv_id:
                conv_id = uuid.uuid4().hex
                # Store mapping from full message hash for future matching
                full_key = json.dumps(
                    [{"role": m.get("role"), "content": str(m.get("content", ""))[:300]} for m in messages],
                    sort_keys=True, ensure_ascii=False,
                )
                fallback_sid = hashlib.sha256(full_key.encode()).hexdigest()[:16]
                self.ctx_cache.set(f"session:{fallback_sid}", {"conversation_id": conv_id})
                if prefix_key:
                    self._msg_prefix_map[prefix_key] = fallback_sid

        messages = self._apply_intent_tools(messages)

        if stream:
            self._stream_chat(messages, cfg, client, conv_id)
        else:
            self._non_stream_chat(messages, cfg, client, conv_id)

    def _apply_intent_tools(self, messages):
        last_user = ""
        for m in reversed(messages):
            if m.get("role") == "user":
                last_user = m.get("content", "")
                break

        intent = detect_tool_intent(last_user)
        if intent and intent in provider.tools:
            args = extract_tool_args(last_user, intent)
            result = _execute_tool(intent, args)
            logging.info(f"[tool] {intent}({args}) -> {result[:80]}")
            messages = list(messages)
            for m in reversed(messages):
                if m.get("role") == "user":
                    m["content"] = f"{last_user}\n\n[Tool Result: {intent} returned '{result}']"
                    break
        return messages

    def _save_conv_id(self, session_id, conv_id):
        if session_id and conv_id:
            self.ctx_cache.set(f"session:{session_id}", {"conversation_id": conv_id})

    def _stream_chat(self, messages, cfg, client, conv_id):
        chunk_id = f"chatcmpl-{uuid.uuid4().hex}"
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()

        openai_model = cfg["openai_id"]
        tone = cfg["tone"]
        gpt_override = cfg["override"]

        try:
            async def stream_loop():
                has_content = False
                current_messages = list(messages)

                for _round in range(MAX_TOOL_ROUNDS + 1):
                    full_text = ""
                    async for chunk, is_final in client.chat_conversation_stream_gen(
                        current_messages, tone, gpt_override, conversation_id=conv_id):
                        if is_final:
                            break
                        full_text += chunk
                        if not has_content:
                            self.wfile.write(sse_msg({"role": "assistant", "content": chunk}, chunk_id, openai_model).encode())
                            has_content = True
                        else:
                            self.wfile.write(sse_msg({"content": chunk}, chunk_id, openai_model).encode())

                    detected = ToolCallDetector.detect(full_text)
                    if not detected:
                        break

                    tool_name, tool_args = detected
                    if tool_name not in provider.tools:
                        break

                    result = _execute_tool(tool_name, tool_args)
                    logging.info(f"[tool] {tool_name}({tool_args}) -> {result[:80]}")
                    current_messages = list(current_messages)
                    current_messages.append({"role": "assistant", "content": full_text})
                    current_messages.append({"role": "tool", "content": result, "name": tool_name})

                prompt_str = str(messages)
                usage = {
                    "prompt_tokens": len(prompt_str.split()),
                    "completion_tokens": len(full_text.split()),
                    "total_tokens": len(prompt_str.split()) + len(full_text.split()),
                }
                self.wfile.write(sse_done(chunk_id, openai_model, usage).encode())

            _run_async(stream_loop())
        except Exception as e:
            err = {"id": chunk_id, "object": "chat.completion.chunk",
                   "created": int(time.time()), "model": openai_model,
                   "choices": [{"index": 0, "delta": {"content": f"Error: {e}"}, "finish_reason": "stop"}]}
            self.wfile.write(f"data: {json.dumps(err)}\n\n".encode())
            self.wfile.write(b"data: [DONE]\n\n")

    def _non_stream_chat(self, messages, cfg, client, conv_id):
        openai_model = cfg["openai_id"]
        tone = cfg["tone"]
        gpt_override = cfg["override"]

        try:
            current_messages = list(messages)
            for _round in range(MAX_TOOL_ROUNDS + 1):
                result_text, tool_calls, finish_reason = _run_async(
                    client.chat_conversation(current_messages, tone, gpt_override, conversation_id=conv_id)
                )
                if not tool_calls:
                    break
                for tc in tool_calls:
                    name = tc["function"]["name"]
                    if name not in provider.tools:
                        continue
                    args = json.loads(tc["function"]["arguments"])
                    result = _execute_tool(name, args)
                    logging.info(f"[tool] {name}({args}) -> {result[:80]}")
                    current_messages = list(current_messages)
                    current_messages.append({"role": "assistant", "content": result_text})
                    current_messages.append({"role": "tool", "content": result, "name": name})
        except Exception as e:
            self._send_error(500, str(e))
            return

        msg = {"role": "assistant", "content": result_text if result_text else None}
        if tool_calls:
            msg["tool_calls"] = tool_calls
            msg["content"] = None
        elif not result_text:
            msg["content"] = None

        prompt_str = str(messages)
        response = {
            "id": f"chatcmpl-{uuid.uuid4().hex}",
            "object": "chat.completion",
            "created": int(time.time()),
            "model": openai_model,
            "choices": [{"index": 0, "message": msg, "finish_reason": finish_reason}],
            "usage": {
                "prompt_tokens": len(prompt_str.split()),
                "completion_tokens": len((result_text or "").split()),
                "total_tokens": len(prompt_str.split()) + len((result_text or "").split()),
            },
        }
        self._send_json(200, response)

    # ---- OpenAI Completions (FIM / legacy) ----
    def _handle_completions(self, req):
        model = req.get("model", "auto")
        prompt = req.get("prompt", "")
        suffix = req.get("suffix", None)
        stream = bool(req.get("stream", False))
        max_tokens = req.get("max_tokens", 512)

        cfg = lookup_model(model)
        if not cfg:
            self._send_error(400, f"Unknown model: {model}")
            return

        messages = fim_to_chat(prompt, suffix)

        client = _get_client()

        if stream:
            self._stream_completions(messages, cfg, client, prompt[:20])
        else:
            self._non_stream_completions(messages, cfg, client)

    def _stream_completions(self, messages, cfg, client, partial_prompt):
        openai_model = cfg["openai_id"]
        tone = cfg["tone"]
        gpt_override = cfg["override"]
        comp_id = f"cmpl-{uuid.uuid4().hex}"

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()

        try:
            async def stream_loop():
                async for chunk, is_final in client.chat_conversation_stream_gen(messages, tone, gpt_override):
                    if is_final:
                        break
                    cdata = {
                        "id": comp_id, "object": "text_completion",
                        "created": int(time.time()), "model": openai_model,
                        "choices": [{"index": 0, "text": chunk, "finish_reason": None, "logprobs": None}],
                    }
                    self.wfile.write(f"data: {json.dumps(cdata, ensure_ascii=False)}\n\n".encode())
                done = {
                    "id": comp_id, "object": "text_completion",
                    "created": int(time.time()), "model": openai_model,
                    "choices": [{"index": 0, "text": "", "finish_reason": "stop", "logprobs": None}],
                }
                self.wfile.write(f"data: {json.dumps(done, ensure_ascii=False)}\n\n".encode())
                self.wfile.write(b"data: [DONE]\n\n")

            _run_async(stream_loop())
        except Exception as e:
            err = f"data: {json.dumps({'id': comp_id, 'object': 'text_completion', 'created': int(time.time()), 'model': openai_model, 'choices': [{'index': 0, 'text': f'Error: {e}', 'finish_reason': 'stop', 'logprobs': None}]})}\n\n"
            self.wfile.write(err.encode())
            self.wfile.write(b"data: [DONE]\n\n")

    def _non_stream_completions(self, messages, cfg, client):
        openai_model = cfg["openai_id"]
        tone = cfg["tone"]
        gpt_override = cfg["override"]

        try:
            result_text, _, _ = _run_async(
                client.chat_conversation(messages, tone, gpt_override)
            )
        except Exception as e:
            self._send_error(500, str(e))
            return

        response = {
            "id": f"cmpl-{uuid.uuid4().hex}",
            "object": "text_completion",
            "created": int(time.time()),
            "model": openai_model,
            "choices": [{"index": 0, "text": result_text, "finish_reason": "stop", "logprobs": None}],
            "usage": {
                "prompt_tokens": len(str(messages).split()),
                "completion_tokens": len(result_text.split()),
                "total_tokens": len(str(messages).split()) + len(result_text.split()),
            },
        }
        self._send_json(200, response)

    # ---- Anthropic Messages API ----
    def _handle_anthropic_messages(self, req):
        model = req.get("model", "claude-sonnet-4-20250514")
        messages = req.get("messages", [])
        system_prompt = req.get("system", "")
        stream = bool(req.get("stream", False))
        max_tokens = req.get("max_tokens", 1024)
        temperature = req.get("temperature", 1.0)

        mapped = _resolve_anthropic_model(model)
        cfg = lookup_model(mapped)

        chat_messages = []
        if system_prompt:
            chat_messages.append({"role": "system", "content": system_prompt})
        for m in messages:
            role = m.get("role", "")
            content = m.get("content", "")
            if isinstance(content, list):
                texts = [p.get("text", "") for p in content if p.get("type") == "text"]
                content = " ".join(texts)
            chat_messages.append({"role": role, "content": content})

        client = _get_client()

        if stream:
            self._anthropic_stream_messages(chat_messages, cfg, client, model)
        else:
            self._anthropic_non_stream_messages(chat_messages, cfg, client, model)

    def _anthropic_stream_messages(self, chat_messages, cfg, client, anthropic_model):
        openai_model = cfg["openai_id"]
        tone = cfg["tone"]
        gpt_override = cfg["override"]
        msg_id = f"msg_{uuid.uuid4().hex}"

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.end_headers()

        try:
            async def stream_loop():
                full_text = ""
                header = {
                    "type": "message_start",
                    "message": {
                        "id": msg_id, "type": "message", "role": "assistant",
                        "content": [], "model": anthropic_model,
                        "stop_reason": None, "stop_sequence": None,
                        "usage": {"input_tokens": len(str(chat_messages).split()), "output_tokens": 0},
                    },
                }
                self.wfile.write(f"event: message_start\ndata: {json.dumps(header, ensure_ascii=False)}\n\n".encode())

                cb_start = {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}
                self.wfile.write(f"event: content_block_start\ndata: {json.dumps(cb_start, ensure_ascii=False)}\n\n".encode())

                async for chunk, is_final in client.chat_conversation_stream_gen(chat_messages, tone, gpt_override):
                    if is_final:
                        break
                    full_text += chunk
                    delta = {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": chunk}}
                    self.wfile.write(f"event: content_block_delta\ndata: {json.dumps(delta, ensure_ascii=False)}\n\n".encode())

                cb_stop = {"type": "content_block_stop", "index": 0}
                self.wfile.write(f"event: content_block_stop\ndata: {json.dumps(cb_stop, ensure_ascii=False)}\n\n".encode())

                msg_delta = {
                    "type": "message_delta",
                    "delta": {"stop_reason": "end_turn", "stop_sequence": None},
                    "usage": {"output_tokens": len(full_text.split())},
                }
                self.wfile.write(f"event: message_delta\ndata: {json.dumps(msg_delta, ensure_ascii=False)}\n\n".encode())

                msg_stop = {"type": "message_stop"}
                self.wfile.write(f"event: message_stop\ndata: {json.dumps(msg_stop, ensure_ascii=False)}\n\n".encode())

            _run_async(stream_loop())
        except Exception as e:
            self.wfile.write(f"event: error\ndata: {json.dumps({'type': 'error', 'error': {'type': 'server_error', 'message': str(e)}})}\n\n".encode())

    def _anthropic_non_stream_messages(self, chat_messages, cfg, client, anthropic_model):
        openai_model = cfg["openai_id"]
        tone = cfg["tone"]
        gpt_override = cfg["override"]

        try:
            result_text, tool_calls, finish_reason = _run_async(
                client.chat_conversation(chat_messages, tone, gpt_override)
            )
        except Exception as e:
            self._send_error(500, str(e))
            return

        stop_reason = {"tool_calls": "tool_use", "stop": "end_turn"}.get(finish_reason or "stop", "end_turn")
        response = {
            "id": f"msg_{uuid.uuid4().hex}",
            "type": "message",
            "role": "assistant",
            "content": [{"type": "text", "text": result_text or ""}],
            "model": anthropic_model,
            "stop_reason": stop_reason,
            "stop_sequence": None,
            "usage": {
                "input_tokens": len(str(chat_messages).split()),
                "output_tokens": len((result_text or "").split()),
            },
        }
        if tool_calls:
            for tc in tool_calls:
                response["content"].append({
                    "type": "tool_use",
                    "id": tc.get("id", f"tu_{uuid.uuid4().hex}"),
                    "name": tc["function"]["name"],
                    "input": json.loads(tc["function"]["arguments"]),
                })
        self._send_json(200, response)

    # ---- Anthropic Complete (FIM) ----
    def _handle_anthropic_complete(self, req):
        model = req.get("model", "claude-sonnet-4-20250514")
        prompt = req.get("prompt", "")
        max_tokens_to_sample = req.get("max_tokens_to_sample", 300)
        stream = bool(req.get("stream", False))
        stop_sequences = req.get("stop_sequences", [])

        mapped = _resolve_anthropic_model(model)
        cfg = lookup_model(mapped)

        messages = fim_to_chat(prompt)

        client = _get_client()

        try:
            result_text, _, _ = _run_async(
                client.chat_conversation(messages, cfg["tone"], cfg["override"])
            )
        except Exception as e:
            self._send_error(500, str(e))
            return

        response = {
            "completion": result_text,
            "stop_reason": "stop_sequence" if any(s in result_text for s in stop_sequences) else "end_turn",
            "model": model,
            "stop": None,
            "log_id": f"cmpl_{uuid.uuid4().hex}",
        }
        self._send_json(200, response)


class ThreadedServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def main():
    import argparse
    parser = argparse.ArgumentParser(description=f"M365 Copilot API Server v{__version__}")
    parser.add_argument("--port", type=int, default=8000, help="监听端口")
    parser.add_argument("--host", type=str, default="127.0.0.1", help="绑定地址")
    parser.add_argument("--setup", action="store_true", help="首次设置")
    args = parser.parse_args()

    if not TENANT_ID or not USER_OID:
        print("错误: M365_TENANT_ID 和 M365_USER_OID 未配置")
        print("运行: m365-copilot-setup 或 python -m m365_copilot.scripts.setup_wizard")
        sys.exit(1)

    os.makedirs(os.path.dirname(RT_FILE), exist_ok=True)
    os.makedirs(CONTEXT_CACHE_DIR, exist_ok=True)
    tm = TokenManager(TENANT_ID, CLIENT_ID, SCOPE, RT_FILE, CACHE_FILE)

    if args.setup:
        from ..scripts.setup_wizard import main as setup_main
        setup_main()
        return

    if not os.path.exists(RT_FILE):
        print(f"首次使用: m365-copilot-setup")
        sys.exit(1)

    try:
        tm.get()
        print("Token OK")
    except Exception as e:
        print(f"Token 失败: {e}")
        sys.exit(1)

    server = ThreadedServer((args.host, args.port), OpenAIHandler)
    print(f"M365 Copilot API Server v{__version__}")
    print(f"  http://{args.host}:{args.port}")
    print(f"  POST /v1/chat/completions  (OpenAI)")
    print(f"  POST /v1/completions        (OpenAI FIM)")
    print(f"  POST /v1/messages           (Anthropic)")
    print(f"  POST /v1/complete           (Anthropic FIM)")
    print(f"  GET  /v1/models             (model list)")
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down...")
        server.shutdown()


if __name__ == "__main__":
    main()
