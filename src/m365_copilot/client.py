import json, uuid, asyncio, sys, re

import websockets
import websockets.exceptions

from .auth import TokenManager
from .payload import build_url, build_payload, build_conversation_payload
from .models import TOOL_MESSAGE_TYPES, MODELS


def clean_text(text):
    if not text:
        return ""
    if isinstance(text, bytes):
        text = text.decode("utf-8", errors="ignore")
    cleaned = "".join(c for c in text if c.isprintable() or c in "\n\t\r")
    cleaned = re.sub(r"[\x00-\x1f\x7f]{1,3}$", "", cleaned)
    return cleaned.strip()


def extract_tool_call(msg):
    mtype = msg.get("messageType", "")
    text = msg.get("text", "")
    if not mtype or not text:
        return None
    func_name = TOOL_MESSAGE_TYPES.get(mtype)
    if not func_name:
        return None
    if mtype == "InternalSearchQuery":
        query = text.replace("search: ", "", 1) if text.startswith("search: ") else text
        args = json.dumps({"query": query})
    elif mtype == "GeneratedCode":
        args = json.dumps({"code": text})
    elif mtype == "GenerateGraphicArt":
        args = json.dumps({"prompt": text})
    else:
        args = json.dumps({"input": text})
    return {
        "id": msg.get("messageId", f"call_{uuid.uuid4().hex}"),
        "type": "function",
        "function": {"name": func_name, "arguments": args},
    }


def _signalr_handshake(ws):
    return ws.send(json.dumps({"protocol": "json", "version": 1}) + "\x1e")


class M365Client:
    def __init__(self, token_manager: TokenManager, timeout_handshake=15, timeout_recv=45, timeout_recv_final=60):
        self.token_manager = token_manager
        self.timeout_handshake = timeout_handshake
        self.timeout_recv = timeout_recv
        self.timeout_recv_final = timeout_recv_final
        self._ws = None
        self._ws_token = None
        self._ws_url = None
        self._ws_dirty = False
        self._last_tool_calls = []
        self._last_finish_reason = "stop"
        self._last_full_text = ""

    async def close(self):
        if self._ws is not None:
            try:
                await self._ws.close()
            except Exception:
                pass
        self._ws = None
        self._ws_token = None
        self._ws_url = None
        self._ws_dirty = False

    def _invalidate_ws(self):
        self._ws = None
        self._ws_token = None
        self._ws_url = None
        self._ws_dirty = False

    def _mark_dirty(self):
        self._ws_dirty = True

    async def _ensure_ws(self, conversation_id=None, hex_sid=None):
        token = self.token_manager.get()
        url, hex_sid, uuid_sid = build_url(token, hex_sid=hex_sid, conversation_id=conversation_id)

        ws_alive = (
            self._ws is not None
            and not self._ws_dirty
            and self._ws_token == token
            and self._ws_url == url
        )
        if not ws_alive:
            if self._ws is not None:
                try:
                    await self._ws.close()
                except Exception:
                    pass
            self._ws = await websockets.connect(
                url, max_size=50 * 1024 * 1024, ping_interval=None, close_timeout=10
            )
            await _signalr_handshake(self._ws)
            await asyncio.wait_for(self._ws.recv(), timeout=self.timeout_handshake)
            self._ws_token = token
            self._ws_url = url
            self._ws_dirty = False

        return self._ws, hex_sid, uuid_sid

    async def chat(self, text, tone="Magic", gpt_override=None, conversation_id=None,
                   enable_image_gen=False, extra_options=None, hex_sid=None):
        ws, hex_sid, uuid_sid = await self._ensure_ws(conversation_id, hex_sid=hex_sid)
        payload = build_payload(hex_sid, uuid_sid, text, tone, gpt_override,
                                enable_image_gen=enable_image_gen, extra_options=extra_options)
        return await self._send_recv(ws, payload)

    async def chat_stream(self, text, tone="Magic", gpt_override=None, conversation_id=None,
                          enable_image_gen=False, extra_options=None, hex_sid=None):
        full_text = ""
        async for chunk, is_final in self.chat_stream_gen(text, tone, gpt_override, conversation_id,
                                                          enable_image_gen=enable_image_gen, extra_options=extra_options, hex_sid=hex_sid):
            if not is_final:
                sys.stdout.buffer.write(chunk.encode('utf-8'))
                sys.stdout.flush()
                full_text += chunk
        return full_text

    async def chat_stream_gen(self, text, tone="Magic", gpt_override=None, conversation_id=None,
                              enable_image_gen=False, extra_options=None, hex_sid=None):
        ws, hex_sid, uuid_sid = await self._ensure_ws(conversation_id, hex_sid=hex_sid)
        payload = build_payload(hex_sid, uuid_sid, text, tone, gpt_override,
                                enable_image_gen=enable_image_gen, extra_options=extra_options)
        await ws.send(payload + "\x1e")

        while True:
            try:
                msg = await asyncio.wait_for(ws.recv(), timeout=self.timeout_recv)
            except (asyncio.TimeoutError, websockets.exceptions.ConnectionClosed,
                    ConnectionResetError, OSError):
                self._invalidate_ws()
                break
            if isinstance(msg, bytes):
                msg = msg.decode("utf-8", errors="ignore")
            for part in msg.split("\x1e"):
                part = part.strip()
                if not part:
                    continue
                try:
                    data = json.loads(part)
                except json.JSONDecodeError:
                    continue
                if data.get("type") == 1 and data.get("target") == "update":
                    for arg in data.get("arguments", []):
                        if "writeAtCursor" in arg:
                            yield (arg["writeAtCursor"], False)
                elif data.get("type") == 3:
                    self._mark_dirty()
                    yield ("", True)
                    return
        yield ("", True)

    async def chat_conversation(self, messages, tone="Magic", gpt_override=None, conversation_id=None,
                                enable_image_gen=False, extra_options=None, hex_sid=None):
        result_text = ""
        async for chunk, is_final in self.chat_conversation_stream_gen(
            messages, tone, gpt_override, conversation_id,
            enable_image_gen=enable_image_gen, extra_options=extra_options, hex_sid=hex_sid):
            if not is_final:
                result_text += chunk
        return clean_text(result_text), self._last_tool_calls, self._last_finish_reason

    async def chat_conversation_stream_gen(self, messages, tone="Magic", gpt_override=None, conversation_id=None,
                                          enable_image_gen=False, extra_options=None, hex_sid=None):
        ws, hex_sid, uuid_sid = await self._ensure_ws(conversation_id, hex_sid=hex_sid)
        payload = build_conversation_payload(hex_sid, uuid_sid, messages, tone, gpt_override,
                                             enable_image_gen=enable_image_gen, extra_options=extra_options)
        await ws.send(payload + "\x1e")

        tool_calls = []
        self._last_full_text = ""

        while True:
            try:
                msg = await asyncio.wait_for(ws.recv(), timeout=self.timeout_recv_final)
            except (asyncio.TimeoutError, websockets.exceptions.ConnectionClosed,
                    ConnectionResetError, OSError):
                self._invalidate_ws()
                break
            if isinstance(msg, bytes):
                msg = msg.decode("utf-8", errors="ignore")
            for part in msg.split("\x1e"):
                part = part.strip()
                if not part:
                    continue
                try:
                    data = json.loads(part)
                except json.JSONDecodeError:
                    continue
                mt = data.get("type")
                if mt == 1 and data.get("target") == "update":
                    for arg in data.get("arguments", []):
                        if "messages" in arg and arg["messages"]:
                            for m in arg["messages"]:
                                if m.get("messageType") in TOOL_MESSAGE_TYPES:
                                    tc = extract_tool_call(m)
                                    if tc:
                                        tool_calls.append(tc)
                            new_text = arg["messages"][-1].get("text", "")
                            if new_text and new_text != self._last_full_text:
                                if new_text.startswith(self._last_full_text):
                                    chunk = new_text[len(self._last_full_text):]
                                else:
                                    chunk = new_text
                                self._last_full_text = new_text
                                if chunk:
                                    yield (chunk, False)
                        if "writeAtCursor" in arg:
                            self._last_full_text += arg["writeAtCursor"]
                            yield (arg["writeAtCursor"], False)
                elif mt == 3:
                    self._last_tool_calls = tool_calls
                    self._last_finish_reason = "tool_calls" if tool_calls else "stop"
                    self._mark_dirty()
                    yield ("", True)
                    return
                elif mt == -1:
                    raise RuntimeError(str(data)[:200])

    async def _send_recv(self, ws, payload):
        full_text = ""
        await ws.send(payload + "\x1e")

        while True:
            try:
                msg = await asyncio.wait_for(ws.recv(), timeout=self.timeout_recv)
            except (asyncio.TimeoutError, websockets.exceptions.ConnectionClosed,
                    ConnectionResetError, OSError):
                self._invalidate_ws()
                break
            if isinstance(msg, bytes):
                msg = msg.decode("utf-8", errors="ignore")
            for part in msg.split("\x1e"):
                part = part.strip()
                if not part:
                    continue
                try:
                    data = json.loads(part)
                except json.JSONDecodeError:
                    continue
                mt = data.get("type")
                if mt == 1 and data.get("target") == "update":
                    for arg in data.get("arguments", []):
                        if "messages" in arg and arg["messages"]:
                            full_text = arg["messages"][-1].get("text", full_text)
                elif mt == 3:
                    self._mark_dirty()
                    return full_text
