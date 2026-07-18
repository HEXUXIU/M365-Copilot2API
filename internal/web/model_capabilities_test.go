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
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) == 0 {
		t.Fatal("empty model catalog")
	}
	for _, m := range body.Data {
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
