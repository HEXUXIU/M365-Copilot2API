package web

import "testing"

func TestShouldUseToolRouter(t *testing.T) {
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "workspace_shell"}}}
	cases := []struct {
		name     string
		messages []oaiMsg
		choice   any
		want     bool
	}{
		{"default catalog ordinary text", []oaiMsg{{Role: "user", Content: "hello"}}, "auto", false},
		{"none", []oaiMsg{{Role: "user", Content: "hello"}}, "none", false},
		{"required", []oaiMsg{{Role: "user", Content: "list files"}}, "required", true},
		{"named", []oaiMsg{{Role: "user", Content: "list files"}}, map[string]any{"name": "workspace_shell"}, true},
		{"tool continuation", []oaiMsg{{Role: "assistant", ToolCalls: []map[string]any{{"id": "c1"}}}, {Role: "tool", ToolCallID: "c1", Content: "ok"}}, "auto", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldUseToolRouter(tc.messages, tools, tc.choice); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
