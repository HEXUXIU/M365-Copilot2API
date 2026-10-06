package web

import (
	"encoding/json"
	"testing"
)

// Codex declares its tools inside the input array rather than the top-level
// tools field, and groups them under a namespace entry. Without unwrapping that
// namespace the model is never told it can call anything, so codex sessions
// produce an intent message and never execute a tool.
func TestResponsesParsesCodexAdditionalTools(t *testing.T) {
	r := responsesRequest{
		Model: "gpt-5.6-sol",
		Input: []any{
			map[string]any{
				"type": "additional_tools",
				"role": "developer",
				"tools": []any{
					map[string]any{
						"type": "namespace",
						"name": "functions",
						"tools": []any{
							map[string]any{"type": "custom", "name": "exec", "description": "run a shell command"},
							map[string]any{"type": "function", "name": "read_file", "description": "read a file"},
						},
					},
				},
			},
			map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "run dir"}},
			},
		},
	}

	o, err := r.openAI()
	if err != nil {
		t.Fatalf("openAI: %v", err)
	}
	// Codex ships a custom `exec` tool that orchestrates the rest, so the
	// gateway declares exec alone and injects the exec contract as a system
	// message. What matters here is that something callable is declared at all:
	// before the namespace was unwrapped this list was empty.
	if len(o.Tools) != 1 {
		t.Fatalf("tools=%#v, want the namespaced exec tool", o.Tools)
	}
	var f struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(o.Tools[0].Function, &f); err != nil {
		t.Fatalf("tool function: %v", err)
	}
	if f.Name != "exec" {
		t.Fatalf("tool name=%q, want exec", f.Name)
	}
	if len(o.Messages) == 0 || o.Messages[0].Role != "system" {
		t.Fatalf("exec contract not injected: %#v", o.Messages)
	}
}

// Without a namespaced wrapper the tools must survive on their own.
func TestResponsesParsesTopLevelCodexFunctionTools(t *testing.T) {
	r := responsesRequest{
		Model: "gpt-5.6-sol",
		Input: []any{
			map[string]any{
				"type": "additional_tools", "role": "developer",
				"tools": []any{
					map[string]any{"type": "function", "name": "read_file", "description": "read a file"},
					map[string]any{"type": "function", "name": "list_dir", "description": "list a directory"},
				},
			},
			map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "run dir"}},
			},
		},
	}
	o, err := r.openAI()
	if err != nil {
		t.Fatalf("openAI: %v", err)
	}
	if len(o.Tools) != 2 {
		t.Fatalf("tools=%#v, want both function tools", o.Tools)
	}
}

// The additional_tools item is transport metadata, not a message: it must not
// become a user turn.
func TestResponsesAdditionalToolsAreNotMessages(t *testing.T) {
	r := responsesRequest{
		Model: "gpt-5.6-sol",
		Input: []any{
			map[string]any{
				"type": "additional_tools", "role": "developer",
				"tools": []any{map[string]any{"type": "custom", "name": "exec"}},
			},
			map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "hi"}},
			},
		},
	}
	o, err := r.openAI()
	if err != nil {
		t.Fatalf("openAI: %v", err)
	}
	for _, m := range o.Messages {
		if m.Content == "developer" {
			t.Fatalf("additional_tools leaked into messages: %#v", o.Messages)
		}
	}
}

func TestFlattenAdditionalToolsIgnoresJunk(t *testing.T) {
	if got := flattenAdditionalTools("not a list"); got != nil {
		t.Fatalf("string input produced tools: %#v", got)
	}
	if got := flattenAdditionalTools([]any{42, "x"}); len(got) != 0 {
		t.Fatalf("non-object entries produced tools: %#v", got)
	}
}
