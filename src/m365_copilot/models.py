import os

CLIENT_ID = os.environ.get("M365_CLIENT_ID", "4765445b-32c6-49b0-83e6-1d93765276ca")
TENANT_ID = os.environ.get("M365_TENANT_ID", "")
USER_OID = os.environ.get("M365_USER_OID", "")
SCOPE = "https://substrate.office.com/sydney/.default openid profile offline_access"

MODELS = {
    "auto":      {"tone": "Magic",     "override": None,                "openai_id": "gpt-4-auto"},
    "quick":     {"tone": "Chat",      "override": None,                "openai_id": "gpt-4-quick"},
    "reasoning": {"tone": "Reasoning", "override": None,                "openai_id": "gpt-4-reasoning"},
    "gpt5.2":    {"tone": "Magic",     "override": "Gpt_5_2_Chat",      "openai_id": "gpt-5.2"},
    "gpt5.3":    {"tone": "Magic",     "override": "Gpt_5_3_Chat",      "openai_id": "gpt-5.3"},
    "gpt5.4":    {"tone": "Magic",     "override": "Gpt_5_4_Chat",      "openai_id": "gpt-5.4"},
    "gpt5.5":    {"tone": "Magic",     "override": "Gpt_5_5_Chat",      "openai_id": "gpt-5.5"},
    "claude":    {"tone": "Magic",     "override": "Claude_Sonnet",      "openai_id": "claude-sonnet"},
}

TOOL_MESSAGE_TYPES = {
    "InternalSearchQuery": "search",
    "GeneratedCode": "code_interpreter",
    "GenerateGraphicArt": "generate_image",
    "TriggerPlugin": "trigger_plugin",
    "InvokeAction": "invoke_action",
}


def _require_env(key):
    val = os.environ.get(key)
    if not val:
        raise ValueError(
            f"Environment variable {key} is required.\n"
            "Get it from: https://graph.microsoft.com/v1.0/me (id and tenantId)"
        )
    return val


def lookup_model(model_key):
    if model_key in MODELS:
        return MODELS[model_key]
    for v in MODELS.values():
        if v["openai_id"] == model_key:
            return v
    return MODELS["auto"]
