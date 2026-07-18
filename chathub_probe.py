#!/usr/bin/env python3
"""Minimal working M365 Copilot ChatHub probe.

Uses token file produced by pkce_auth_gateway.py:
  ~/.config/m365-native/accounts.json

Protocol notes (from cramt/m365-copilot-proxy reverse eng, verified live):
- WS: wss://substrate.office.com/m365Copilot/Chathub/{oid}@{tid}
- access_token goes in query string (not Authorization header)
- SignalR JSON frames terminated by 0x1E
- Handshake: {"protocol":"json","version":1}\\x1e
- Chat turn requires chat frame (type 4 target chat) + Metrics frame (type 1)
  in the same send
"""

from __future__ import annotations

import argparse
import asyncio
import json
import uuid
import urllib.parse
from pathlib import Path

import websockets

DEFAULT_TOKEN_FILE = "~/.config/m365-native/accounts.json"

VARIANTS = ",".join(
    [
        "EnableMcpServerWidgets",
        "feature.EnableMcpServerWidgets",
        "feature.EnableLuForChatCIQ",
        "feature.enableChatCIQPlugin",
        "EnableRequestPlugins",
        "feature.EnableSensitivityLabels",
        "EnableUnsupportedUrlDetector",
        "feature.IsCustomEngineCopilotEnabled",
        "feature.bizchatfluxv3",
        "feature.enablechatpages",
        "feature.enableCodeCanvas",
        "feature.turnOnWorkTabRecommendation",
        "turnOffWorkTabUpsellFromClient",
        "feature.turnOnDARecommendation",
        "feature.IsStreamingModeInChatRequestEnabled",
        "IncludeSourceAttributionsConcise",
        "SkipPublishEmptyMessage",
        "feature.EnableDeduplicatingSourceAttributions",
        "Enable3PActionProgressMessages",
        "feature.enableClientWebRtc",
        "feature.EnableMeetingRecapOfSeriesMeetingWithCiq",
        "feature.EnableReferencesListCompleteSignal",
        "feature.StorageMessageSplitDisabled",
        "feature.EnableCuaTakeControlApi",
        "feature.cwcallowedos",
        "feature.disabledisallowedmsgs",
        "feature.enableCitationsForSynthesisData",
        "feature.enableGenerateGraphicArtOptionsSet",
        "cdximagen",
        "feature.EnableUpdatedUXForConfirmationDialog",
        "feature.EnableClientFileURLSupportForOfficeWebPaidCopilot",
        "feature.EnableDesignEditorImageGrounding",
        "feature.EnableDesignerEditor",
        "feature.OfficeWebToHelix",
        "feature.OfficeDesktopToHelix",
        "feature.M365TeamsHubToHelix",
        "feature.OwaHubToHelix",
        "feature.MonarchHubToHelix",
        "feature.Win32OutlookHubToHelix",
        "feature.MacOutlookHubToHelix",
        "Agt_bizchat_enableGpt5ForHelix",
    ]
)

RS = "\x1e"


def load_token(path: str) -> dict:
    data = json.loads(Path(path).read_text())
    return data["accounts"][0]


def build_ws_url(acc: dict, session_id: str, conversation_id: str, request_id: str) -> str:
    params = {
        "chatsessionid": request_id,
        "clientrequestid": request_id,
        "X-SessionId": session_id,
        "ConversationId": conversation_id,
        "access_token": acc["accessToken"],
        "variants": VARIANTS,
        "source": '"officeweb"',
        "product": "Office",
        "agentHost": "Bizchat.FullScreen",
        "licenseType": "Starter",
        "agent": "web",
        "scenario": "OfficeWebIncludedCopilot",
    }
    qs = urllib.parse.urlencode(params, safe='",')
    return f"wss://substrate.office.com/m365Copilot/Chathub/{acc['oid']}@{acc['tid']}?{qs}"


def chat_payload(text: str, session_id: str, conversation_id: str, request_id: str, tone: str) -> str:
    chat = {
        "arguments": [
            {
                "source": "officeweb",
                "clientCorrelationId": str(uuid.uuid4()),
                "sessionId": session_id,
                "optionsSets": [],
                "options": {},
                "allowedMessageTypes": [
                    "Chat",
                    "Suggestion",
                    "Disengaged",
                    "Progress",
                    "EndOfRequest",
                    "InternalLoaderMessage",
                ],
                "sliceIds": [],
                "threadLevelGptId": {},
                "conversationId": conversation_id,
                "traceId": str(uuid.uuid4()),
                "isStartOfSession": True,
                "productThreadType": "Office",
                "clientInfo": {
                    "clientPlatform": "mcmcopilot-web",
                    "clientAppName": "Office",
                },
                "tone": tone,
                "streamingMode": "ConciseWithPadding",
                "message": {
                    "author": "user",
                    "inputMethod": "Keyboard",
                    "text": text,
                    "requestId": request_id,
                    "locationInfo": {
                        "timeZoneOffset": 8,
                        "timeZone": "Asia/Shanghai",
                    },
                    "locale": "en-US",
                    "messageType": "Chat",
                    "experienceType": "Default",
                },
                "plugins": [{"Id": "BingWebSearch", "Source": "BuiltIn"}],
            }
        ],
        "invocationId": "0",
        "target": "chat",
        "type": 4,
    }
    metrics = {
        "arguments": [
            {
                "Timestamps": {
                    "ConnectionStart": "",
                    "UserInputStart": "",
                    "ConnectionEstablished": "",
                    "UserInputSubmit": "",
                }
            }
        ],
        "target": "Metrics",
        "type": 1,
    }
    return (
        json.dumps(chat, separators=(",", ":"))
        + RS
        + json.dumps(metrics, separators=(",", ":"))
        + RS
    )


async def run_once(token_file: str, text: str, tone: str) -> str:
    acc = load_token(token_file)
    session_id = str(uuid.uuid4())
    conversation_id = str(uuid.uuid4())
    request_id = str(uuid.uuid4())
    ws_url = build_ws_url(acc, session_id, conversation_id, request_id)
    headers = {
        "Origin": "https://m365.cloud.microsoft",
        "User-Agent": (
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) "
            "Gecko/20100101 Firefox/148.0"
        ),
    }

    out: list[str] = []
    final = ""

    async with websockets.connect(
        ws_url,
        additional_headers=headers,
        open_timeout=15,
        close_timeout=5,
        max_size=8 * 1024 * 1024,
    ) as ws:
        await ws.send('{"protocol":"json","version":1}' + RS)
        hs = await asyncio.wait_for(ws.recv(), timeout=10)
        print("handshake:", repr(hs)[:80])

        await ws.send(chat_payload(text, session_id, conversation_id, request_id, tone))
        print("sent chat+metrics")

        for _ in range(40):
            try:
                msg = await asyncio.wait_for(ws.recv(), timeout=20)
            except asyncio.TimeoutError:
                print("frame timeout")
                break

            if isinstance(msg, bytes):
                msg = msg.decode("utf-8", "replace")

            for part in [p for p in msg.split(RS) if p]:
                try:
                    obj = json.loads(part)
                except json.JSONDecodeError:
                    print("raw:", part[:160])
                    continue

                t = obj.get("type")
                target = obj.get("target")

                if t == 6:
                    await ws.send(json.dumps({"type": 6}) + RS)
                    continue

                if t == 1 and target == "update":
                    for arg in obj.get("arguments") or []:
                        if not isinstance(arg, dict):
                            continue
                        if "writeAtCursor" in arg:
                            out.append(arg["writeAtCursor"])
                            print("delta:", arg["writeAtCursor"][:120].replace("\n", " "))
                        for m in arg.get("messages") or []:
                            if (
                                isinstance(m, dict)
                                and m.get("author") == "bot"
                                and not m.get("messageType")
                                and m.get("text")
                            ):
                                print("snapshot:", m["text"][:160].replace("\n", " "))
                                out.append(m["text"])
                        thr = arg.get("throttling")
                        if thr:
                            print("throttle:", thr)
                elif t in (2, 3, 7):
                    if t == 2:
                        item = obj.get("item") or {}
                        res = item.get("result") or {}
                        final = str(res.get("message") or "")
                        print("result:", res.get("value"), final[:200])
                        thr = item.get("throttling")
                        if thr:
                            print("throttle:", thr)
                    elif t == 3 and obj.get("error"):
                        print("completion error:", obj.get("error"))
                    return final or "".join(out)

    return final or "".join(out)


def main() -> None:
    parser = argparse.ArgumentParser(description="Probe M365 Copilot ChatHub")
    parser.add_argument("--token-file", default=DEFAULT_TOKEN_FILE)
    parser.add_argument("--text", default="Say only: pong")
    parser.add_argument("--tone", default="magic")
    args = parser.parse_args()
    answer = asyncio.run(run_once(args.token_file, args.text, args.tone))
    print("ANSWER:", answer)


if __name__ == "__main__":
    main()
