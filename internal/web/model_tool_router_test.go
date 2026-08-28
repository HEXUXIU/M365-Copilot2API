package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"m365-copilot2api/internal/chathub"
)

func workspaceTestTools(names ...string) []chathub.Tool {
	tools := make([]chathub.Tool, 0, len(names))
	for _, name := range names {
		definition, _ := json.Marshal(map[string]any{"name": name, "description": "test tool", "parameters": map[string]any{"type": "object"}})
		tools = append(tools, chathub.Tool{Type: "function", Function: definition})
	}
	return tools
}

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
	if explicitToolRequest([]oaiMsg{{Role: "user", Content: "必须调用工具"}, {Role: "assistant", ToolCalls: []map[string]any{{"id": "call_1"}}}, {Role: "tool", ToolCallID: "call_1", Content: "done"}}) {
		t.Fatal("completed tool result reactivated the original tool request")
	}
	if !explicitToolRequest([]oaiMsg{{Role: "user", Content: "必须调用工具"}, {Role: "user", Content: "You have 100 weighted tokens left"}}) {
		t.Fatal("Codex token notice hid the explicit tool request")
	}
}

func TestWorkspaceToolRequestPromotesDirectExecution(t *testing.T) {
	tools := workspaceTestTools("shell_command")
	for _, request := range []string{
		"在当前目录创建一个 1.txt 文件，里面写 123214324",
		"你倒是创建啊",
		"Read config.json and check its contents.",
		"現在、config.jsonファイルを作成してください",
	} {
		if !workspaceToolRequest([]oaiMsg{{Role: "user", Content: request}}, tools) {
			t.Fatalf("direct workspace request was not detected: %q", request)
		}
	}
}

func TestWorkspaceToolRequestAvoidsQuestionsAndNegation(t *testing.T) {
	tools := workspaceTestTools("apply_patch")
	for _, request := range []string{
		"请解释如何创建一个 1.txt 文件",
		"不要创建 1.txt，只告诉我它是否存在",
		"What does create file mean?",
		"How to write a file in Go?",
		"I already know the file exists.",
	} {
		if workspaceToolRequest([]oaiMsg{{Role: "user", Content: request}}, tools) {
			t.Fatalf("non-execution request was promoted: %q", request)
		}
	}
	if workspaceToolRequest([]oaiMsg{{Role: "user", Content: "创建 1.txt 并写入内容"}}, workspaceTestTools("get_weather")) {
		t.Fatal("request was promoted without a workspace-capable tool")
	}
	if workspaceToolRequest([]oaiMsg{{Role: "user", Content: "创建 1.txt 并写入内容"}}, workspaceTestTools("get_profile")) {
		t.Fatal("profile tool was mistaken for a file tool")
	}
}

func TestWorkspaceToolRequestOnlyChecksLatestTurn(t *testing.T) {
	tools := workspaceTestTools("exec")
	if !workspaceToolRequest([]oaiMsg{
		{Role: "user", Content: "创建 1.txt"},
		{Role: "user", Content: "You have 100 weighted tokens left"},
	}, tools) {
		t.Fatal("Codex token notice hid the workspace action")
	}
	if workspaceToolRequest([]oaiMsg{
		{Role: "user", Content: "创建 1.txt"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "解释一下刚才的结果"},
	}, tools) {
		t.Fatal("stale workspace action affected a later question")
	}
	if workspaceToolRequest([]oaiMsg{
		{Role: "user", Content: "创建 1.txt"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "call_1"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}, tools) {
		t.Fatal("completed workspace action was promoted again")
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

func TestToolRouterTimeoutsStayBelowDownstreamDeadline(t *testing.T) {
	if got := toolRouterTotalTimeout(120); got != 55*time.Second {
		t.Fatalf("default router budget=%s want 55s", got)
	}
	if got := toolRouterTotalTimeout(9); got != 9*time.Second {
		t.Fatalf("configured short router budget=%s want 9s", got)
	}
	if got := requiredToolRouterAttemptTimeout(120); got != 18*time.Second {
		t.Fatalf("required router attempt timeout=%s want 18s", got)
	}
	if maxToolRouterTotalTimeout >= 60*time.Second {
		t.Fatalf("router failover budget=%s must stay below downstream 60s deadline", maxToolRouterTotalTimeout)
	}
	if total := time.Duration(maxToolRouterAccountAttempts) * requiredToolRouterAttemptTimeout(120); total > maxToolRouterTotalTimeout {
		t.Fatalf("required account attempts=%s exceed router budget=%s", total, maxToolRouterTotalTimeout)
	}
}

func TestRequiredToolDecisionRejectsEmptyOrInvalidOutput(t *testing.T) {
	for _, output := range []string{
		"I will answer without a tool.",
		`{"calls":[]}`,
		`{"calls":[{"name":"missing_tool","arguments":{}}]}`,
	} {
		calls, parsed := parseModelToolDecision(output, testTools(), "required")
		calls, _ = validateDetectedToolCalls(calls, testTools(), "required")
		if parsed && len(calls) > 0 {
			t.Fatalf("invalid required decision accepted: %q", output)
		}
	}

	calls, parsed := parseModelToolDecision(`{"calls":[{"name":"get_weather","arguments":{"city":"Shanghai"}}]}`, testTools(), "required")
	calls, _ = validateDetectedToolCalls(calls, testTools(), "required")
	if !parsed || len(calls) != 1 || calls[0].Name != "get_weather" {
		t.Fatalf("valid required decision rejected: parsed=%v calls=%+v", parsed, calls)
	}
}
