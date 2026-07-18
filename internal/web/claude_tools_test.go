package web

import "testing"

func TestClaudeModelAdvertisesToolCapability(t *testing.T) {
	var found int
	for _, m := range modelCatalog() {
		if m["id"] != "claude-sonnet" && m["id"] != "claude-sonnet-reasoning" {
			continue
		}
		found++
		caps, ok := m["capabilities"].(map[string]any)
		if !ok || caps["tools"] != true {
			t.Fatalf("Claude model does not advertise tools: %#v", m)
		}
	}
	if found != 2 {
		t.Fatalf("expected two Claude models, found %d", found)
	}
}

func TestClaudeAnthropicToolChoiceAndRoundTrip(t *testing.T) {
	base := anthropicRequest{
		Model:    "claude-sonnet",
		Messages: []anthropicMessage{{Role: "user", Content: "check the weather"}},
		Tools:    []anthropicTool{{Name: "weather", Description: "Get weather", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}}},
	}
	for _, tc := range []struct {
		name string
		want any
	}{
		{"auto", "auto"},
		{"any", "required"},
		{"none", "none"},
	} {
		r := base
		r.ToolChoice = map[string]any{"type": tc.name}
		o, err := r.openAI()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(o.Tools) != 1 || o.Tools[0].Type != "function" {
			t.Fatalf("%s: tools not converted: %#v", tc.name, o.Tools)
		}
		if o.ToolChoice != tc.want {
			t.Fatalf("%s: tool choice=%#v want %#v", tc.name, o.ToolChoice, tc.want)
		}
	}

	conversation := anthropicRequest{
		Model: "claude-sonnet",
		Messages: []anthropicMessage{
			{Role: "assistant", Content: []any{map[string]any{"type": "tool_use", "id": "call_weather", "name": "weather", "input": map[string]any{"city": "Paris"}}}},
			{Role: "user", Content: []any{map[string]any{"type": "tool_result", "tool_use_id": "call_weather", "content": "sunny"}}},
		},
	}
	o, err := conversation.openAI()
	if err != nil || len(o.Messages) != 2 {
		t.Fatalf("round trip conversion failed: messages=%#v err=%v", o.Messages, err)
	}
	if o.Messages[0].ToolCalls[0]["id"] != "call_weather" || o.Messages[1].ToolCallID != "call_weather" {
		t.Fatalf("tool identity was not preserved: %#v", o.Messages)
	}
}
