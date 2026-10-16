package web

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFencedWorkspaceShellIsStructuredToolCall(t *testing.T) {
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "workspace_shell"}}}
	calls := fencedToolCalls("```workspace_shell\n{\"command\":\"find /workspace -type f -o -type d | sort\"}\n```", tools, "auto")
	if len(calls) != 1 {
		t.Fatalf("expected one structured tool call, got %d", len(calls))
	}
	if calls[0].Name != "workspace_shell" {
		t.Fatalf("unexpected tool name %q", calls[0].Name)
	}
	if string(calls[0].Arguments) != `{"command":"find /workspace -type f -o -type d | sort"}` {
		t.Fatalf("unexpected arguments: %s", calls[0].Arguments)
	}
}

func TestFencedBashNotConvertedWhenUndeclared(t *testing.T) {
	// Issue #12: no bash tool declared -> code blocks must stay as text.
	calls := fencedToolCalls("```powershell\nGet-Process\n```", nil, "auto")
	if len(calls) != 0 {
		t.Fatalf("undeclared shell conversion must not happen, got %d calls", len(calls))
	}
}

func TestFencedBashConvertedWhenDeclared(t *testing.T) {
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "bash"}}}
	calls := fencedToolCalls("```bash\nls -la\n```", tools, "auto")
	if len(calls) != 1 || calls[0].Name != "bash" {
		t.Fatalf("declared bash should convert, got %+v", calls)
	}
	if string(calls[0].Arguments) != `{"command":"ls -la"}` {
		t.Fatalf("unexpected arguments: %s", calls[0].Arguments)
	}
}

func TestFencedWriteFileMapsToCapitalizedWriteWithFilePath(t *testing.T) {
	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name": "Write",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string"},
					"content":   map[string]any{"type": "string"},
				},
				"required": []any{"file_path", "content"},
			},
		},
	}}
	reply := "下面是一个可直接运行的贪吃蛇游戏。\n\n```write_file\npath: snake.html\n\n<!DOCTYPE html>\n<html><body>Snake</body></html>\n```"
	raw := fencedToolCalls(reply, tools, "auto")
	valid, rejected := validateDetectedToolCalls(raw, tools, "auto")
	if len(rejected) != 0 || len(valid) != 1 {
		t.Fatalf("valid=%+v rejected=%+v", valid, rejected)
	}
	if valid[0].Name != "Write" {
		t.Fatalf("got name=%q, want Write", valid[0].Name)
	}
	var gotArgs map[string]string
	if err := json.Unmarshal(valid[0].Arguments, &gotArgs); err != nil {
		t.Fatal(err)
	}
	if gotArgs["file_path"] != "snake.html" || gotArgs["content"] != "<!DOCTYPE html>\n<html><body>Snake</body></html>" {
		t.Fatalf("unexpected arguments: %#v", gotArgs)
	}
}

func TestFlattenPromptMessagesMovesTrailingHarnessRemindersToSystem(t *testing.T) {
	msgs := []oaiMsg{
		{Role: "user", Content: "做一个贪吃蛇游戏"},
		{Role: "user", Content: "Current runtime context. This snapshot supersedes earlier runtime-context snapshots.\nCurrent DSH file policy: workspace-write."},
		{Role: "user", Content: "<system-reminder>\nA skill is a reusable set of task-specific instructions.\n</system-reminder>"},
	}
	flat, _ := flattenPromptMessages(msgs, nil)
	sysIdx := strings.Index(flat, "[system]")
	userIdx := strings.LastIndex(flat, "[user]\n做一个贪吃蛇游戏")
	if sysIdx < 0 || userIdx < 0 || userIdx < sysIdx {
		t.Fatalf("expected [user] task at the end after [system], got:\n%s", flat)
	}
	if start := incrementalSliceStart(msgs, 1); start != 0 {
		t.Fatalf("incrementalSliceStart=%d, want 0 so trailing reminders do not cut off the real user task", start)
	}
}

// A code-execution continuation turn must not lose the user request that owns
// the tool result: the harness sends [user task][assistant tool_call][tool result
// + reminders], and slicing from the stored history length alone leaves the
// upstream model with an orphan result, which it answers by reciting the
// formatting spec instead of finishing the task.
func TestIncrementalSliceKeepsOwningUserTaskForToolResult(t *testing.T) {
	msgs := []oaiMsg{
		{Role: "user", Content: "做一个贪吃蛇游戏"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "call_1", "type": "function", "function": map[string]any{"name": "code", "arguments": "{\"code\":\"mkdir -p demo\"}"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "Error: code run failed (worker-exit): SetNamedSecurityInfoW failed (Win32 5): grantWrite(D:\\backup\\snake)"},
		{Role: "user", Content: "Current runtime context. This snapshot supersedes earlier runtime-context snapshots.\nCurrent DSH file policy: workspace-write."},
	}
	// historyLen=2 records the cloud conversation that held turn 1 only.
	if start := incrementalSliceStart(msgs, 2); start != 0 {
		t.Fatalf("incrementalSliceStart=%d, want 0 so the model still sees the Snake game request", start)
	}
	flat, _ := flattenPromptMessages(msgs, nil)
	if !strings.Contains(flat, "做一个贪吃蛇游戏") {
		t.Fatalf("flattened continuation lost the user task:\n%s", flat)
	}
	if !strings.Contains(flat, "[tool result id=call_1]") {
		t.Fatalf("flattened continuation lost the tool result:\n%s", flat)
	}
	if !strings.Contains(flat, "[continuation]") {
		t.Fatalf("flattened continuation is missing the continuation prompt:\n%s", flat)
	}
}

// A fresh user turn in the tail is self-contained: the cloud conversation
// already carries the prior turns, so the slice must not re-send them.
func TestIncrementalSliceKeepsFreshUserTurnAtHistoryLength(t *testing.T) {
	msgs := []oaiMsg{
		{Role: "user", Content: "第一轮问题"},
		{Role: "assistant", Content: "第一轮回答"},
		{Role: "user", Content: "第二轮问题"},
	}
	if start := incrementalSliceStart(msgs, 2); start != 2 {
		t.Fatalf("incrementalSliceStart=%d, want 2 for a fresh user turn", start)
	}
}

// ```typescript blocks are language samples, not tool calls. Routing a stub such
// as `return 'Create a basic Snake game implementation in the workspace';` to
// the executor ran a no-op command, so the harness reported a file that was
// never created.
func TestFencedLanguageStubIsNotAToolCall(t *testing.T) {
	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name": "code",
			"parameters": map[string]any{
				"type":       "object",
				"properties": map[string]any{"code": map[string]any{"type": "string"}},
				"required":   []any{"code"},
			},
		},
	}}
	stub := "下面是一个完整的单文件 HTML 贪吃蛇游戏。\n\n```typescript\nreturn 'Create a basic Snake game implementation in the workspace';\n```"
	if calls := fencedToolCalls(stub, tools, "auto"); len(calls) != 0 {
		t.Fatalf("language stub must not become a tool call, got %+v", calls)
	}
	// A real command body in the same language tag still converts.
	real := "```typescript\nimport fs from 'node:fs'\nfs.writeFileSync('index.html', '<html>hi</html>')\n```"
	calls := fencedToolCalls(real, tools, "auto")
	if len(calls) != 1 || calls[0].Name != "code" {
		t.Fatalf("real command body should convert to the declared executor, got %+v", calls)
	}
}

func TestFencedNestedCodeBlocksAndToolCallJSONEnvelope(t *testing.T) {
	tools := []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name": "code",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"code": map[string]any{"type": "string"},
					},
					"required": []any{"code"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name": "skill",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
					"required": []any{"name"},
				},
			},
		},
	}
	envReply := "我将先修复权限。\n```tool_call\n{\"name\":\"skill\",\"arguments\":{\"name\":\"diagnose-windows-sandbox-acl\"}}\n```"
	calls := fencedToolCalls(envReply, tools, "auto")
	if len(calls) != 1 || calls[0].Name != "skill" || string(calls[0].Arguments) != `{"name":"diagnose-windows-sandbox-acl"}` {
		t.Fatalf("tool_call envelope parse failed: %+v", calls)
	}
	codeReply := "生成游戏：\n```code\ncat > index.html <<'EOF'\n<!DOCTYPE html>\nEOF\n```"
	calls2 := fencedToolCalls(codeReply, tools, "auto")
	if len(calls2) != 1 || calls2[0].Name != "code" {
		t.Fatalf("code fence parse failed: %+v", calls2)
	}
}
