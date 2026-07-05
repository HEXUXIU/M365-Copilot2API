import os

CLIENT_ID = os.environ.get("M365_CLIENT_ID", "4765445b-32c6-49b0-83e6-1d93765276ca")
TENANT_ID = os.environ.get("M365_TENANT_ID", "")
USER_OID = os.environ.get("M365_USER_OID", "")
SCOPE = "https://substrate.office.com/sydney/.default openid profile offline_access"

MODELS = {
    # Default modes
    "auto":      {"tone": "Magic",     "override": None,                "openai_id": "gpt-4-auto"},
    "quick":     {"tone": "Chat",      "override": None,                "openai_id": "gpt-4-quick"},
    "reasoning": {"tone": "Reasoning", "override": None,                "openai_id": "gpt-4-reasoning"},

    # GPT-5.x series
    "gpt5.2":    {"tone": "Gpt_5_2_Quick",   "override": None,          "openai_id": "gpt-5.2"},
    "gpt5.3":    {"tone": "Gpt_5_3_Quick",   "override": None,          "openai_id": "gpt-5.3"},
    "gpt5.4":    {"tone": "Gpt_5_4_Quick",   "override": None,          "openai_id": "gpt-5.4"},
    "gpt5.5":    {"tone": "Gpt_5_5_Chat",    "override": None,          "openai_id": "gpt-5.5"},
    "gpt5.2-reasoning": {"tone": "Gpt_5_2_Reasoning", "override": None, "openai_id": "gpt-5.2-reasoning"},
    "gpt5.3-reasoning": {"tone": "Gpt_5_3_Reasoning", "override": None, "openai_id": "gpt-5.3-reasoning"},
    "gpt5.4-reasoning": {"tone": "Gpt_5_4_Reasoning", "override": None, "openai_id": "gpt-5.4-reasoning"},
    "gpt5.5-reasoning": {"tone": "Gpt_5_5_Reasoning", "override": None, "openai_id": "gpt-5.5-reasoning"},

    # Claude — real Anthropic models (verified June 2026)
    "claude":         {"tone": "Claude_Sonnet",           "override": None, "openai_id": "claude-sonnet-4.6"},
    "claude-sonnet":  {"tone": "Claude_Sonnet",           "override": None, "openai_id": "claude-sonnet-4.6"},
    "claude-reasoning": {"tone": "Claude_Sonnet_Reasoning", "override": None, "openai_id": "claude-sonnet-reasoning"},
    "claude-opus":    {"tone": "Claude_Opus",             "override": None, "openai_id": "claude-opus"},

    # Aliases for OpenAI-style model names
    "claude-sonnet-4-20250514": {"tone": "Claude_Sonnet", "override": None, "openai_id": "claude-sonnet-4.6"},
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
