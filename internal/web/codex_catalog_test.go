package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelTokenLimitsAreConsistent(t *testing.T) {
	t.Setenv("M365_CONTEXT_WINDOW", "128000")
	t.Setenv("M365_MAX_OUTPUT_TOKENS", "16384")
	l := configuredModelLimits()
	if l.ContextWindow != 128000 || l.MaxOutputTokens != 16384 || l.MaxInputTokens != 111616 {
		t.Fatalf("limits=%+v", l)
	}
}

func TestModelTokenLimitsNormalizeBadOutputLimit(t *testing.T) {
	t.Setenv("M365_CONTEXT_WINDOW", "100")
	t.Setenv("M365_MAX_OUTPUT_TOKENS", "500")
	l := configuredModelLimits()
	if l.MaxInputTokens <= 0 || l.MaxOutputTokens <= 0 || l.MaxInputTokens+l.MaxOutputTokens != l.ContextWindow {
		t.Fatalf("inconsistent limits=%+v", l)
	}
}

func TestModelsAdvertiseContextAndReasoning(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	s.openaiModels(w, r)
	var body struct {
		Data   []map[string]any `json:"data"`
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) == 0 {
		t.Fatal("empty model catalog")
	}
	if len(body.Models) != len(body.Data) {
		t.Fatalf("models alias length=%d, data length=%d", len(body.Models), len(body.Data))
	}
	for _, m := range body.Data {
		baseInstructions, ok := m["base_instructions"].(string)
		if !ok || baseInstructions == "" {
			t.Fatalf("missing Codex base instructions: %#v", m)
		}
		modelMessages, ok := m["model_messages"].(map[string]any)
		if !ok || modelMessages["instructions_template"] != baseInstructions {
			t.Fatalf("missing or inconsistent Codex model messages: %#v", m)
		}
		variables, ok := modelMessages["instructions_variables"].(map[string]any)
		if !ok || variables["personality_default"] != "" || variables["personality_friendly"] != "" || variables["personality_pragmatic"] != "" {
			t.Fatalf("invalid Codex instruction variables: %#v", modelMessages)
		}
		if modelMessages["approvals"] != nil || modelMessages["auto_review"] != nil {
			t.Fatalf("invalid optional Codex model messages: %#v", modelMessages)
		}
		if m["slug"] != m["id"] {
			t.Fatalf("missing or inconsistent slug: %#v", m)
		}
		if displayName, ok := m["display_name"].(string); !ok || displayName == "" {
			t.Fatalf("missing display_name: %#v", m)
		}
		levels, ok := m["supported_reasoning_levels"].([]any)
		if !ok || len(levels) == 0 {
			t.Fatalf("missing supported reasoning levels: %#v", m)
		}
		for _, level := range levels {
			preset, ok := level.(map[string]any)
			if !ok || preset["effort"] == "" || preset["description"] == "" {
				t.Fatalf("invalid reasoning preset: %#v", level)
			}
		}
		defaultReasoningLevel, ok := m["default_reasoning_level"].(string)
		if effort, err := normalizeReasoningEffort(defaultReasoningLevel); !ok || err != nil || effort == "" || m["description"] == "" {
			t.Fatalf("missing Codex catalog metadata: %#v", m)
		}
		if m["shell_type"] != "shell_command" || m["visibility"] != "list" || m["supported_in_api"] != true || m["priority"] != float64(1) {
			t.Fatalf("missing Codex execution metadata: %#v", m)
		}
		if _, ok := m["additional_speed_tiers"].([]any); !ok {
			t.Fatalf("missing speed tiers: %#v", m)
		}
		if _, ok := m["service_tiers"].([]any); !ok {
			t.Fatalf("missing service tiers: %#v", m)
		}
		if m["apply_patch_tool_type"] != "freeform" || m["web_search_tool_type"] != "text_and_image" || m["tool_mode"] != "code_mode_only" || m["multi_agent_version"] != "v2" {
			t.Fatalf("missing Codex tool metadata: %#v", m)
		}
		if m["max_context_window"] != m["context_window"] || m["effective_context_window_percent"] != float64(95) {
			t.Fatalf("inconsistent Codex context metadata: %#v", m)
		}
		policy, ok := m["truncation_policy"].(map[string]any)
		if !ok || policy["mode"] != "tokens" || policy["limit"] != float64(10000) {
			t.Fatalf("missing truncation policy: %#v", m)
		}
		if _, ok := m["experimental_supported_tools"].([]any); !ok || m["supports_search_tool"] != true || m["use_responses_lite"] != false {
			t.Fatalf("missing Codex capability metadata: %#v", m)
		}
		if m["context_window"].(float64) <= 0 || m["max_input_tokens"].(float64) <= 0 || m["max_output_tokens"].(float64) <= 0 {
			t.Fatalf("missing limits: %#v", m)
		}
		caps, ok := m["capabilities"].(map[string]any)
		if !ok {
			t.Fatalf("missing capabilities: %#v", m)
		}
		if caps["reasoning"] != true {
			t.Fatalf("reasoning not advertised: %#v", m)
		}
		if levels, ok := caps["supported_reasoning_levels"].([]any); !ok || len(levels) == 0 {
			t.Fatalf("capabilities missing supported reasoning levels: %#v", m)
		}
		if efforts, ok := caps["reasoning_efforts"].([]any); !ok || len(efforts) == 0 {
			t.Fatalf("capabilities missing object reasoning efforts: %#v", m)
		} else if _, ok := efforts[0].(map[string]any); !ok {
			t.Fatalf("reasoning efforts must be preset objects: %#v", efforts)
		}
	}
	for i, m := range body.Models {
		if m["slug"] != body.Data[i]["slug"] {
			t.Fatalf("models alias missing slug at %d: %#v", i, m)
		}
		if m["display_name"] != body.Data[i]["display_name"] {
			t.Fatalf("models alias missing display_name at %d: %#v", i, m)
		}
		if m["supported_reasoning_levels"] == nil {
			t.Fatalf("models alias missing supported reasoning levels at %d: %#v", i, m)
		}
		if m["base_instructions"] != body.Data[i]["base_instructions"] || m["model_messages"] == nil {
			t.Fatalf("models alias missing instruction metadata at %d: %#v", i, m)
		}
	}
}

func TestConfiguredModelMappingsDriveCatalogAndRouting(t *testing.T) {
	mappings := []modelMapping{{PublicModel: "gpt-5.6-sol", UpstreamTone: "Gpt_5_6_Reasoning", DisplayName: "GPT-5.6-Sol", DefaultReasoningLevel: "low"}}
	models := configuredModelSpecs(mappings)
	if len(models) != len(gatewayModels)+1 || models[len(models)-1].ID != "gpt-5.6-sol" || models[len(models)-1].DefaultReasoningLevel != "low" {
		t.Fatalf("configured models=%#v", models)
	}
	mapping, ok := configuredModelMapping("GPT-5.6-SOL", mappings)
	if !ok || mapping.UpstreamTone != "Gpt_5_6_Reasoning" {
		t.Fatalf("mapping=%#v ok=%t", mapping, ok)
	}
	if tone, ok := configuredModelTone("gpt-5.6-sol", mappings); !ok || tone != "Gpt_5_6_Reasoning" {
		t.Fatalf("tone=%q ok=%t", tone, ok)
	}
	override := configuredModelSpecs([]modelMapping{{PublicModel: "gpt-5.5", UpstreamTone: "Gpt_5_5_Reasoning", DisplayName: "GPT-5.5", DefaultReasoningLevel: "high"}})
	if len(override) != len(gatewayModels) || override[5].DefaultReasoningLevel != "high" {
		t.Fatalf("built-in override=%#v", override)
	}
}

func TestReasoningEffortRouting(t *testing.T) {
	cases := []struct{ model, effort, want string }{
		{"claude-sonnet", "none", "Claude_Sonnet"},
		{"claude-sonnet", "high", "Claude_Sonnet_Reasoning"},
		{"gpt-5.5", "low", "Gpt_5_5_Chat"},
		{"gpt-5.5", "medium", "Gpt_5_5_Reasoning"},
		{"gpt-5.6-reasoning", "none", "Gpt_5_6_Reasoning"},
	}
	for _, tc := range cases {
		got, err := reasoningTone(tc.model, tc.effort)
		if err != nil || got != tc.want {
			t.Fatalf("%s/%s got=%q err=%v", tc.model, tc.effort, got, err)
		}
	}
	if _, err := reasoningTone("gpt-5.6-reasoning", "extreme"); err == nil {
		t.Fatal("invalid effort accepted")
	}
}

func TestChatRejectsInvalidReasoningBeforeUpstream(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.6-reasoning","reasoning_effort":"extreme","messages":[{"role":"user","content":"hello"}]}`))
	w := httptest.NewRecorder()
	s.openaiChat(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported reasoning effort") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestResponsesReasoningConvertsToOpenAI(t *testing.T) {
	r := responsesRequest{Model: "gpt-5.6-reasoning", Input: "hello", Reasoning: &reasoningConfig{Effort: "high"}}
	o, err := r.openAI()
	if err != nil {
		t.Fatal(err)
	}
	if o.ReasoningEffort != "high" {
		t.Fatalf("effort=%q", o.ReasoningEffort)
	}
}
