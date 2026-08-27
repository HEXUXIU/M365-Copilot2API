package web

import (
	"strings"
	"testing"
)

func TestExplicitToolRequestOnlyChecksLatestUserMessage(t *testing.T) {
	if !explicitToolRequest([]oaiMsg{{Role: "user", Content: "请使用终端工具读取 go.mod，必须实际调用工具。"}}) {
		t.Fatal("explicit Chinese tool request was not detected")
	}
	if !explicitToolRequest([]oaiMsg{{Role: "user", Content: "You must actually call the terminal tool."}}) {
		t.Fatal("explicit English tool request was not detected")
	}
	if explicitToolRequest([]oaiMsg{{Role: "user", Content: "必须调用工具"}, {Role: "assistant", Content: "ok"}, {Role: "user", Content: "现在直接回答问题"}}) {
		t.Fatal("stale tool request affected the latest user turn")
	}
}

func TestParseModelToolDecisionAutoAndParallel(t *testing.T) {
	calls, ok := parseModelToolDecision(`{"calls":[{"name":"get_weather","arguments":{"city":"Beijing"}},{"name":"get_time","arguments":{"city":"Beijing"}}]}`, testTools(), "auto")
	if !ok || len(calls) != 2 {
		t.Fatalf("calls=%v ok=%v", calls, ok)
	}
}
func TestParseModelToolDecisionNoCall(t *testing.T) {
	calls, ok := parseModelToolDecision(`{"calls":[]}`, testTools(), "auto")
	if !ok || len(calls) != 0 {
		t.Fatalf("calls=%v ok=%v", calls, ok)
	}
}
func TestModelToolRouterPromptMarksCompletedResults(t *testing.T) {
	p := modelToolRouterPrompt(`assistant tool_calls: [...]
tool[call_x]: 2026-07-18`, testTools(), "auto")
	if !strings.Contains(p, "Completed evidence must not be repeated") || !strings.Contains(p, "tool[call_x]: 2026-07-18") || !strings.Contains(p, "unfinished work remains") {
		t.Fatalf("missing multi-turn evidence constraint: %s", p)
	}
}

func TestParseModelToolDecisionRejectsBadSchema(t *testing.T) {
	calls, ok := parseModelToolDecision("```json\n{\"calls\":[{\"name\":\"get_weather\",\"arguments\":{\"city\":2}}]}\n```", testTools(), "auto")
	if !ok || len(calls) != 0 {
		t.Fatalf("calls=%v ok=%v", calls, ok)
	}
}

func TestToolChoiceRequiresCall(t *testing.T) {
	tests := []struct {
		name   string
		choice any
		want   bool
	}{
		{name: "implicit auto", choice: nil, want: false},
		{name: "auto", choice: "auto", want: false},
		{name: "none", choice: "none", want: false},
		{name: "required", choice: "required", want: true},
		{name: "named", choice: map[string]any{"function": map[string]any{"name": "get_weather"}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolChoiceRequiresCall(tt.choice); got != tt.want {
				t.Fatalf("toolChoiceRequiresCall(%#v)=%v want %v", tt.choice, got, tt.want)
			}
		})
	}
}
