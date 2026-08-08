package web

import (
	"encoding/json"
	"testing"
)

// codexAdditionalToolsInput mirrors the shape captured from Codex Desktop
// 0.147.0: the tool catalog arrives as an `additional_tools` input item whose
// entries are `namespace` groups, and the top-level tools field is absent.
func codexAdditionalToolsInput() []any {
	return []any{
		map[string]any{
			"type": "additional_tools",
			"role": "developer",
			"tools": []any{
				map[string]any{
					"name": "functions",
					"type": "namespace",
					"tools": []any{
						map[string]any{"name": "exec", "type": "custom", "description": "run js", "format": map[string]any{"type": "grammar", "syntax": "lark"}},
						map[string]any{"name": "wait", "type": "function", "description": "wait", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}},
					},
				},
				map[string]any{
					"name": "collaboration",
					"type": "namespace",
					"tools": []any{
						map[string]any{"name": "spawn_agent", "type": "function", "description": "spawn", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}},
					},
				},
			},
		},
		map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{
				map[string]any{"type": "input_text", "text": "分析下当前项目"},
			},
		},
	}
}

func TestResponsesExtractsCodexAdditionalTools(t *testing.T) {
	r := responsesRequest{Model: "gpt-5.6-sol", Input: codexAdditionalToolsInput(), ToolChoice: "auto"}
	o, err := r.openAI()
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Tools) == 0 {
		t.Fatal("additional_tools were dropped; request would reach ChatHub with no tools")
	}
	// The catalog contains a custom exec tool, so the exec bridge takes over and
	// it must be the single forwarded tool with the string-input schema.
	if len(o.Tools) != 1 {
		t.Fatalf("want only the custom exec bridge, got %d tools", len(o.Tools))
	}
	if o.Tools[0].Type != "custom" {
		t.Fatalf("type=%q want custom", o.Tools[0].Type)
	}
	var fn struct {
		Name       string         `json:"name"`
		Parameters map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(o.Tools[0].Function, &fn); err != nil {
		t.Fatal(err)
	}
	if fn.Name != "exec" {
		t.Fatalf("name=%q want exec", fn.Name)
	}
	props, _ := fn.Parameters["properties"].(map[string]any)
	if _, ok := props["input"]; !ok {
		t.Fatalf("exec bridge lost its input schema: %#v", fn.Parameters)
	}
}

func TestResponsesAdditionalToolsKeepsUserMessage(t *testing.T) {
	r := responsesRequest{Model: "gpt-5.6-sol", Input: codexAdditionalToolsInput()}
	o, err := r.openAI()
	if err != nil {
		t.Fatal(err)
	}
	// The additional_tools item must not become a prompt message, and the real
	// user turn must survive alongside the injected exec instruction.
	var user int
	for _, m := range o.Messages {
		if m.Role == "user" {
			user++
			if text := contentToString(m.Content); text != "分析下当前项目" {
				t.Fatalf("user content=%q", text)
			}
		}
	}
	if user != 1 {
		t.Fatalf("user messages=%d want 1: %#v", user, o.Messages)
	}
}

func TestResponsesAdditionalToolsWithoutCustomExec(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "additional_tools",
			"tools": []any{
				map[string]any{
					"name": "functions",
					"type": "namespace",
					"tools": []any{
						map[string]any{"name": "shell", "type": "function", "description": "run", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}},
						map[string]any{"name": "update_plan", "type": "function", "description": "plan", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}},
					},
				},
			},
		},
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "hi"}}},
	}
	o, err := responsesRequest{Model: "gpt-5.6-sol", Input: input}.openAI()
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Tools) != 2 {
		t.Fatalf("tools=%d want 2", len(o.Tools))
	}
	for _, tool := range o.Tools {
		if tool.Type != "function" {
			t.Fatalf("type=%q want function", tool.Type)
		}
	}
}

func TestResponsesTopLevelAndInputToolsMerge(t *testing.T) {
	input := []any{
		map[string]any{
			"type": "additional_tools",
			"tools": []any{
				map[string]any{"name": "update_plan", "type": "function", "description": "plan", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}},
			},
		},
	}
	o, err := responsesRequest{
		Model: "gpt-5.6-sol",
		Input: input,
		Tools: []map[string]any{{"type": "function", "name": "shell", "description": "run", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}},
	}.openAI()
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Tools) != 2 {
		t.Fatalf("tools=%d want top-level and input tools merged", len(o.Tools))
	}
}
