import json, uuid, datetime, locale

from .models import USER_OID, TENANT_ID

def _get_local_tz():
    now = datetime.datetime.now(datetime.timezone.utc).astimezone()
    offset = int(now.utcoffset().total_seconds() // 3600)
    tz_name = now.tzname() or "UTC"
    tz_map = {
        "中国标准时间": "Asia/Shanghai",
        "中国夏令时": "Asia/Shanghai",
        "Pacific Standard Time": "America/Los_Angeles",
        "Pacific Daylight Time": "America/Los_Angeles",
        "Eastern Standard Time": "America/New_York",
        "Eastern Daylight Time": "America/New_York",
        "UTC": "UTC",
    }
    return offset, tz_map.get(tz_name, tz_name)

def _get_locale():
    loc = locale.getdefaultlocale()[0] or "en_US"
    return loc.lower().replace("_", "-")

LOCAL_TZ_OFFSET, LOCAL_TZ_NAME = _get_local_tz()
LOCAL_LOCALE = _get_locale()

VARIANTS = (
    "EnableMcpServerWidgets,feature.EnableLuForChatCIQ,feature.enableChatCIQPlugin,"
    "EnableRequestPlugins,feature.IsCustomEngineCopilotEnabled,feature.bizchatfluxv3,"
    "feature.enablechatpages,feature.IsStreamingModeInChatEnabled,"
    "IncludeSourceAttributionsConcise,SkipPublishEmptyMessage,"
    "feature.EnableDeduplicatingSourceAttributions,feature.enableDeltaStreamingForReferences,"
    "feature.enableIncludeReferencesInDeltaResponse,feature.enablereferencesforagents,"
    "feature.EnableReferencesListCompleteSignal,SingletonEnvOn,cdxenablefccinmainline,"
    "feature.disabledisallowedmsgs,cdximagen,cdxenablerenderforisocomp,"
    "feature.EnablePersonalization,feature.EnableSkipEmittingMessageOnFlush,"
    "feature.EnableRemoveEmptySourceAttributions,feature.EnableRemoveStreamingMode,"
    "feature.OfficeWebToHelix,feature.OfficeDesktopToHelix,feature.M365TeamsHubToHelix,"
    "feature.OwaHubToHelix,feature.MonarchHubToHelix,feature.Win32OutlookHubToHelix,"
    "feature.MacOutlookHubToHelix,Agt_bizchat_enableGpt5ForHelix"
)

OPTIONS_SETS_FULL = [
    "search_result_progress_messages_with_search_queries",
    "update_textdoc_response_after_streaming",
    "deepleo_networking_timeout_10minutes_canmore",
    "cwc_flux_image", "cwc_code_interpreter",
    "cwc_code_interpreter_amsfix", "cwcfluxgptv",
    "flux_v3_gptv_enable_upload_multi_image_in_turn_wo_ch",
    "gptvnorm2048", "cwc_code_interpreter_citation_fix",
    "code_interpreter_interactive_charts",
    "cwc_code_interpreter_interactive_charts_inline_image",
    "code_interpreter_matplotlib_patching",
    "cwc_fileupload_odb", "update_memory_plugin",
    "add_custom_instructions", "cwc_flux_v3",
    "flux_v3_progress_messages", "enable_batch_token_processing",
    "enable_gg_gpt", "flux_v3_references",
    "flux_v3_references_entities",
    "flux_v3_image_gen_enable_dimensions",
    "flux_v3_image_gen_enable_non_watermarked_storage",
    "flux_v3_image_gen_enable_icon_dimensions",
    "flux_v3_image_gen_enable_system_text_with_params",
    "flux_v3_image_gen_enable_designer_dimensions_meta_prompting_in_system_prompts",
    "flux_v3_image_gen_enable_story", "rich_responses",
    "pages_citations", "pages_citations_multiturn",
]

OPTIONS_SETS_LITE = [
    "search_result_progress_messages_with_search_queries",
    "deepleo_networking_timeout_10minutes_canmore",
    "cwc_flux_image", "cwc_code_interpreter",
    "enable_batch_token_processing", "rich_responses",
]

IMAGE_OPTIONS = frozenset({
    "cwc_flux_image", "cwc_flux_v3", "flux_v3_progress_messages",
    "flux_v3_references", "flux_v3_references_entities",
    "flux_v3_image_gen_enable_dimensions",
    "flux_v3_image_gen_enable_non_watermarked_storage",
    "flux_v3_image_gen_enable_icon_dimensions",
    "flux_v3_image_gen_enable_system_text_with_params",
    "flux_v3_image_gen_enable_designer_dimensions_meta_prompting_in_system_prompts",
    "flux_v3_image_gen_enable_story", "flux_v3_gptv_enable_upload_multi_image_in_turn_wo_ch",
})

FILE_UPLOAD_OPTIONS = frozenset({"cwc_fileupload_odb"})

ALLOWED_MSG_TYPES = [
    "Chat", "Suggestion", "InternalSearchQuery", "Disengaged",
    "InternalLoaderMessage", "Progress", "GeneratedCode",
    "RenderCardRequest", "AdsQuery", "SemanticSerp",
    "GenerateContentQuery", "GenerateGraphicArt", "SearchQuery",
    "ConfirmationCard", "AuthError", "DeveloperLogs",
    "TriggerPlugin", "HintInvocation", "MemoryUpdate",
    "EndOfRequest", "TriggerConfirmation",
    "ResumeInvokeAction", "ResumeUserInputRequest",
]


def build_url(token, hex_sid=None, conversation_id=None):
    if not USER_OID or not TENANT_ID:
        from .models import USER_OID as u, TENANT_ID as t
        raise ValueError(
            "M365_USER_OID and M365_TENANT_ID environment variables required.\n"
            "Get them from: https://graph.microsoft.com/v1.0/me (id and tenantId)"
        )
    if hex_sid is None:
        hex_sid = uuid.uuid4().hex
    uuid_sid = f"{hex_sid[:8]}-{hex_sid[8:12]}-{hex_sid[12:16]}-{hex_sid[16:20]}-{hex_sid[20:32]}"
    url = f"wss://substrate.office.com/m365Copilot/Chathub/{USER_OID}@{TENANT_ID}"
    url += f"?chatsessionid={hex_sid}&XRoutingParameterSessionKey={hex_sid}"
    url += f"&clientrequestid={hex_sid}&X-SessionId={uuid_sid}"
    if conversation_id:
        url += f"&ConversationId={conversation_id}"
    url += f"&access_token={token}"
    url += f"&variants={VARIANTS}"
    url += "&source=%22officeweb%22&product=Office&agentHost=Bizchat.FullScreen"
    url += "&licenseType=Starter&isEdu=false&agent=web&scenario=OfficeWebIncludedCopilot"
    return url, hex_sid, uuid_sid


def build_payload(hex_sid, uuid_sid, text, tone="Magic", gpt_override=None,
                  enable_image_gen=False, enable_file_upload=False, extra_options=None):
    inv_id = str(uuid.uuid4())
    options = list(OPTIONS_SETS_FULL)
    if not enable_image_gen:
        options = [o for o in options if o not in IMAGE_OPTIONS]
    if not enable_file_upload:
        options = [o for o in options if o not in FILE_UPLOAD_OPTIONS]
    if extra_options:
        options.extend(extra_options)
    p = {
        "type": 4, "invocationId": inv_id, "target": "chat",
        "arguments": [{
            "source": "officeweb", "clientCorrelationId": hex_sid, "sessionId": uuid_sid,
            "message": {
                "author": "user", "inputMethod": "Keyboard", "text": text,
                "entityAnnotationTypes": ["People", "File", "Event", "Email", "TeamsMessage"],
                "requestId": f"{hex_sid}_0",
                "locationInfo": {"timeZoneOffset": LOCAL_TZ_OFFSET, "timeZone": LOCAL_TZ_NAME},
                "locale": LOCAL_LOCALE, "messageType": "Chat", "experienceType": "Default",
                "adaptiveCards": [], "clientPreferences": {},
                "connectedFederatedConnections": ["dummyId"],
            },
            "optionsSets": options,
            "streamingMode": "ConciseWithPadding",
            "spokenTextMode": "None", "options": {}, "extraExtensionParameters": {},
            "allowedMessageTypes": ALLOWED_MSG_TYPES,
            "sliceIds": [], "tone": tone,
            "plugins": [{"Id": "BingWebSearch", "Source": "BuiltIn"}],
            "isStartOfSession": False,
            "isSbsSupported": True, "renderReferencesBehindEOS": True,
            "disconnectBehavior": "continue",
        }]
    }
    if gpt_override:
        p["arguments"][0]["gptIdOverride"] = {"id": gpt_override, "source": "MOS3"}
    return json.dumps(p)


def build_payload_with_tools(hex_sid, uuid_sid, text, tone="Magic", gpt_override=None):
    inv_id = str(uuid.uuid4())
    p = {
        "type": 4, "invocationId": inv_id, "target": "chat",
        "arguments": [{
            "source": "BCBv2Windows",
            "clientCorrelationId": hex_sid, "sessionId": uuid_sid,
            "hostContext": {"hostType": "BCBv2Windows", "hostVersion": "1.0.0"},
            "message": {
                "author": "user", "inputMethod": "Keyboard", "text": text,
                "messageType": "Chat", "locale": LOCAL_LOCALE,
            },
            "clientInfo": {
                "clientPlatform": "mcmcopilot-desktop", "clientAppName": "Copilot",
                "clientEntrypoint": "mcmcopilot-win32", "clientSessionId": uuid_sid,
                "ProductCategory": "Chat", "clientAppType": "Desktop",
                "productEntryPoint": "WindowsCopilotSidebar",
                "deviceOS": "Windows", "deviceType": "Desktop",
            },
            "optionsSets": OPTIONS_SETS_LITE,
            "streamingMode": "ConciseWithPadding",
            "spokenTextMode": "None", "options": {}, "extraExtensionParameters": {},
            "allowedMessageTypes": ["Chat"],
            "sliceIds": [], "tone": tone,
            "plugins": [{"Id": "BingWebSearch", "Source": "BuiltIn"}],
            "isStartOfSession": False,
            "isSbsSupported": True, "renderReferencesBehindEOS": True,
            "disconnectBehavior": "continue",
        }]
    }
    if gpt_override:
        p["arguments"][0]["gptIdOverride"] = {"id": gpt_override, "source": "MOS3"}
    return p


def build_conversation_payload(hex_sid, uuid_sid, messages, tone="Magic", gpt_override=None,
                               enable_image_gen=False, enable_file_upload=False, extra_options=None):
    inv_id = str(uuid.uuid4())
    options = list(OPTIONS_SETS_FULL)
    if not enable_image_gen:
        options = [o for o in options if o not in IMAGE_OPTIONS]
    if not enable_file_upload:
        options = [o for o in options if o not in FILE_UPLOAD_OPTIONS]
    if extra_options:
        options.extend(extra_options)
    m365_history = []
    last_text = messages[-1].get("content", "") if messages else ""

    for m in messages[:-1]:
        role = m.get("role", "")
        content = m.get("content", "")
        if isinstance(content, list):
            texts = [p.get("text", "") for p in content if p.get("type") == "text"]
            content = " ".join(texts)

        if role == "user":
            m365_history.append({
                "author": "user", "inputMethod": "Keyboard", "text": content or last_text,
                "messageType": "Chat", "experienceType": "Default",
                "adaptiveCards": [], "clientPreferences": {},
            })
        elif role == "assistant" and content:
            m365_history.append({
                "author": "bot", "text": content, "messageType": "Chat",
            })
        elif role == "tool":
            m365_history.append({
                "author": "user", "inputMethod": "Keyboard",
                "text": f"[Tool result: {content}]",
                "messageType": "Chat", "adaptiveCards": [], "clientPreferences": {},
            })

    p = {
        "type": 4, "invocationId": inv_id, "target": "chat",
        "arguments": [{
            "source": "officeweb", "clientCorrelationId": hex_sid, "sessionId": uuid_sid,
            "message": {
                "author": "user", "inputMethod": "Keyboard", "text": last_text,
                "entityAnnotationTypes": [],
                "requestId": f"{hex_sid}_0",
                "locale": LOCAL_LOCALE, "messageType": "Chat", "experienceType": "Default",
                "adaptiveCards": [], "clientPreferences": {},
            },
            "optionsSets": options,
            "streamingMode": "ConciseWithPadding",
            "spokenTextMode": "None", "options": {}, "extraExtensionParameters": {},
            "allowedMessageTypes": ALLOWED_MSG_TYPES,
            "sliceIds": [], "tone": tone,
            "plugins": [{"Id": "BingWebSearch", "Source": "BuiltIn"}],
            "isStartOfSession": False,
            "isSbsSupported": True, "renderReferencesBehindEOS": True,
            "disconnectBehavior": "continue",
        }]
    }
    if gpt_override:
        p["arguments"][0]["gptIdOverride"] = {"id": gpt_override, "source": "MOS3"}
    if m365_history:
        p["arguments"][0]["messageHistory"] = m365_history
    return json.dumps(p)
