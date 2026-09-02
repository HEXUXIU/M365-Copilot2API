package web

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSandboxHallucinationRecognizesWindowsDesktopFallbackText(t *testing.T) {
	text := "执行环境仍然没有挂载 Windows 的 C: 盘，因此只能访问隔离环境 /mnt/data，无法触达你的真实桌面目录。"
	if !isSandboxHallucination(text) {
		t.Fatal("sandbox fallback text was not recognized")
	}
}

func TestSandboxRepairPromptUsesDeclaredRuntimeContract(t *testing.T) {
	tools := []map[string]any{{
		"type": "custom",
		"function": map[string]any{
			"name":        "exec",
			"description": "Run JavaScript.\n### shell_command\nRuns the caller's PowerShell command.",
			"parameters":  map[string]any{"type": "object"},
		},
	}}
	p := modelToolSandboxRepairPrompt("在桌面创建 report.txt", "只能访问 /mnt/data", tools, "auto")
	for _, want := range []string{"declared top-level tool", "caller-provided shell", "tools.<name>(...)", "PowerShell command"} {
		if !strings.Contains(p, want) {
			t.Fatalf("sandbox repair prompt missing %q: %s", want, p)
		}
	}
	if strings.Contains(strings.ToLower(p), "bash tool") || strings.Contains(p, "Windows PowerShell 5.1") {
		t.Fatalf("sandbox repair prompt hard-coded a runtime: %s", p)
	}
}

func TestValidateDetectedToolCallsRejectsUndeclaredName(t *testing.T) {
	calls := []detectedToolCall{{
		ID:        "tool_call_0",
		Name:      "unknown_tool",
		Arguments: json.RawMessage(`{"path":"E:\\SoarClient-fork","pattern":"*.jsonl"}`),
	}}

	valid, rejected := validateDetectedToolCalls(calls, testTools(), "auto")
	if len(valid) != 0 {
		t.Fatalf("undeclared call escaped validation: %#v", valid)
	}
	if len(rejected) != 1 || rejected[0].Name != "unknown_tool" {
		t.Fatalf("rejected=%#v", rejected)
	}
}

func TestValidateDetectedToolCallsRejectsInvalidArguments(t *testing.T) {
	calls := []detectedToolCall{{
		ID:        "tool_call_0",
		Name:      "get_weather",
		Arguments: json.RawMessage(`{"city":2}`),
	}}

	valid, rejected := validateDetectedToolCalls(calls, testTools(), "auto")
	if len(valid) != 0 || len(rejected) != 1 {
		t.Fatalf("valid=%#v rejected=%#v", valid, rejected)
	}
}

func TestValidateDetectedToolCallsAcceptsDeclaredCall(t *testing.T) {
	calls := []detectedToolCall{{
		Name:      "get_weather",
		Arguments: json.RawMessage(`{"city":"Paris"}`),
	}}

	valid, rejected := validateDetectedToolCalls(calls, testTools(), "auto")
	if len(rejected) != 0 || len(valid) != 1 {
		t.Fatalf("valid=%#v rejected=%#v", valid, rejected)
	}
	if valid[0].ID == "" || valid[0].Type != "function" {
		t.Fatalf("call was not normalized: %#v", valid[0])
	}
}

func TestValidateDetectedToolCallsRejectsEmptyCustomInput(t *testing.T) {
	tools := []map[string]any{{
		"type": "custom",
		"function": map[string]any{
			"name": "apply_patch",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string"},
				},
				"required":             []any{"input"},
				"additionalProperties": false,
			},
		},
	}}

	for _, arguments := range []string{`{"input":""}`, `{"input":"  \r\n"}`} {
		valid, rejected := validateDetectedToolCalls([]detectedToolCall{{
			Name:      "apply_patch",
			Type:      "custom",
			Arguments: json.RawMessage(arguments),
		}}, tools, "required")
		if len(valid) != 0 || len(rejected) != 1 {
			t.Fatalf("arguments=%s valid=%#v rejected=%#v", arguments, valid, rejected)
		}
	}
}

func TestValidateDetectedToolCallsChecksExecNestedToolName(t *testing.T) {
	tools := []map[string]any{{
		"type": "custom",
		"function": map[string]any{
			"name": "exec",
			"description": `Run JavaScript.
### apply_patch
Apply a patch.
### shell_command
Run a shell command.`,
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string"},
				},
				"required":             []any{"input"},
				"additionalProperties": false,
			},
		},
	}}

	aliased := []detectedToolCall{{Name: "exec", Type: "custom", Arguments: json.RawMessage(`{"input":"const r = await tools.exec_command({command: 'pwd'}); text(r);"}`)}}
	valid, rejected := validateDetectedToolCalls(aliased, tools, "required")
	if len(valid) != 1 || len(rejected) != 0 || !strings.Contains(string(valid[0].Arguments), "tools.shell_command") {
		t.Fatalf("declared shell alias was not repaired: valid=%#v rejected=%#v", valid, rejected)
	}
	unknown := []detectedToolCall{{Name: "exec", Type: "custom", Arguments: json.RawMessage(`{"input":"const r = await tools.unknown_tool({}); text(r);"}`)}}
	valid, rejected = validateDetectedToolCalls(unknown, tools, "required")
	if len(valid) != 0 || len(rejected) != 1 || rejected[0].Reason != "exec input references an unavailable nested tool" {
		t.Fatalf("unknown nested tool escaped: valid=%#v rejected=%#v", valid, rejected)
	}

	accepted := []detectedToolCall{{Name: "exec", Type: "custom", Arguments: json.RawMessage(`{"input":"const r = await tools.shell_command({command: 'pwd'}); text(r);"}`)}}
	valid, rejected = validateDetectedToolCalls(accepted, tools, "required")
	if len(valid) != 1 || len(rejected) != 0 {
		t.Fatalf("declared nested tool was rejected: valid=%#v rejected=%#v", valid, rejected)
	}
}

func TestValidateDetectedToolCallsAllowsActuallyDiscoveredDeferredTool(t *testing.T) {
	tools := []map[string]any{{
		"type": "custom",
		"function": map[string]any{
			"name":        "exec",
			"description": "Run JavaScript.\n### shell_command\nRun a command.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"input": map[string]any{"type": "string"},
				},
				"required":             []any{"input"},
				"additionalProperties": false,
			},
		},
	}}
	messages := []oaiMsg{
		{Role: "assistant", ToolCalls: []map[string]any{{
			"id": "call_discover", "type": "custom", "function": map[string]any{
				"name": "exec", "arguments": `{"input":"text(ALL_TOOLS.filter(tool => tool.name.includes('node_repl')));"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call_discover", Content: `[{"name":"mcp__node_repl__js","description":"persistent JavaScript runtime"}]`},
	}
	deferred := discoveredExecNestedToolNames(messages)
	calls := []detectedToolCall{{Name: "exec", Arguments: json.RawMessage(`{"input":"const r = await tools.mcp__node_repl__js({code: 'nodeRepl.write(42)'}); text(r);"}`)}}
	valid, rejected := validateDetectedToolCallsWithDeferred(calls, tools, "required", deferred)
	if len(valid) != 1 || len(rejected) != 0 {
		t.Fatalf("discovered deferred tool rejected: valid=%#v rejected=%#v deferred=%#v", valid, rejected, deferred)
	}
	unknown := []detectedToolCall{{Name: "exec", Arguments: json.RawMessage(`{"input":"const r = await tools.mcp__invented({}); text(r);"}`)}}
	valid, rejected = validateDetectedToolCallsWithDeferred(unknown, tools, "required", deferred)
	if len(valid) != 0 || len(rejected) != 1 {
		t.Fatalf("undiscovered deferred tool escaped: valid=%#v rejected=%#v", valid, rejected)
	}
}

func TestDiscoveredExecNestedToolNamesIgnoresUnrelatedToolResult(t *testing.T) {
	messages := []oaiMsg{
		{Role: "assistant", ToolCalls: []map[string]any{{
			"id": "call_read", "type": "custom", "function": map[string]any{
				"name": "exec", "arguments": `{"input":"text('ordinary result')"}`,
			},
		}}},
		{Role: "tool", ToolCallID: "call_read", Content: `[{"name":"mcp__invented"}]`},
	}
	if got := discoveredExecNestedToolNames(messages); len(got) != 0 {
		t.Fatalf("unrelated tool result was trusted: %#v", got)
	}
}

func TestParseNaturalToolDecisionRejectsBadSchema(t *testing.T) {
	calls, parsed := parseModelToolDecision(`CALL_TOOL: get_weather({"city":2})`, testTools(), "auto")
	if parsed || len(calls) != 0 {
		t.Fatalf("calls=%#v parsed=%v", calls, parsed)
	}
}
