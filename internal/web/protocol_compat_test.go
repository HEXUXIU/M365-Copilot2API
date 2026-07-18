package web

import "testing"

func TestResponsesToOpenAI(t *testing.T) {
	r := responsesRequest{Model: "m", Input: "what time", Tools: []map[string]any{{"type": "function", "name": "clock", "parameters": map[string]any{"type": "object"}}}}
	o, err := r.openAI()
	if err != nil || len(o.Messages) != 1 || len(o.Tools) != 1 {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestAnthropicToOpenAI(t *testing.T) {
	r := anthropicRequest{Model: "m", System: any("be concise"), Messages: []anthropicMessage{{Role: "user", Content: any("weather")}}, Tools: []anthropicTool{{Name: "weather", InputSchema: map[string]any{"type": "object"}}}}
	o, err := r.openAI()
	if err != nil || len(o.Messages) != 2 || len(o.Tools) != 1 {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestAnthropicToolResult(t *testing.T) {
	r := anthropicRequest{Messages: []anthropicMessage{{Role: "assistant", Content: []any{map[string]any{"type": "tool_use", "id": "x", "name": "f", "input": map[string]any{}}}}, {Role: "user", Content: []any{map[string]any{"type": "tool_result", "tool_use_id": "x", "content": "ok"}}}}}
	o, err := r.openAI()
	if err != nil || len(o.Messages) != 2 || o.Messages[1].ToolCallID != "x" {
		t.Fatalf("%+v %v", o, err)
	}
}
