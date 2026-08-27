package web

import (
	"encoding/json"
	"strings"
	"testing"
)

func textTestTools() []map[string]any {
	return []map[string]any{
		{"type": "function", "function": map[string]any{
			"name":        "bash",
			"description": "run a shell command",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string"},
				},
				"required": []any{"command"},
			},
		}},
		{"type": "function", "function": map[string]any{
			"name":        "read_file",
			"description": "read a file",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
				"required": []any{"path"},
			},
		}},
	}
}

func TestZaiExtractXMLToolCalls(t *testing.T) {
	text := `I will help you with that.

<tool_calls>
<tool_call id="call_1">
<name>bash</name>
<arguments><![CDATA[{"command":"ls -la"}]]></arguments>
</tool_call>
</tool_calls>`
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if !ok {
		t.Fatal("expected tool call extraction")
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "bash" {
		t.Fatalf("expected bash, got %s", calls[0].Name)
	}
	var args map[string]any
	if err := json.Unmarshal(calls[0].Arguments, &args); err != nil {
		t.Fatalf("bad arguments: %v", err)
	}
	if args["command"] != "ls -la" {
		t.Fatalf("unexpected command arg: %v", args)
	}
	if calls[0].ID == "" {
		t.Fatal("missing call id")
	}
}

func TestZaiExtractXMLWithoutBlock(t *testing.T) {
	text := `<tool_call><name>read_file</name><arguments>{"path":"/etc/hosts"}</arguments></tool_call>`
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if !ok {
		t.Fatal("expected extraction from bare tool_call")
	}
	if len(calls) != 1 || calls[0].Name != "read_file" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestZaiExtractFencedJSON(t *testing.T) {
	text := "Running now:\n\n```json\n{\"tool_calls\":[{\"id\":\"1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"command\\\":\\\"whoami\\\"}\"}}]}\n```"
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if !ok {
		t.Fatal("expected extraction from fenced json")
	}
	if len(calls) != 1 || calls[0].Name != "bash" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestZaiExtractInlineToolCalls(t *testing.T) {
	text := `result: {"tool_calls":[{"id":"a","name":"bash","arguments":{"command":"pwd"}}]} done`
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if !ok {
		t.Fatal("expected extraction from inline json")
	}
	if len(calls) != 1 || calls[0].Name != "bash" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestZaiExtractNamedFunctionObject(t *testing.T) {
	text := `calling {"name":"read_file","arguments":{"path":"/tmp/x"}} now`
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if !ok {
		t.Fatal("expected extraction from named function object")
	}
	if len(calls) != 1 || calls[0].Name != "read_file" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestZaiExtractFunctionInvoke(t *testing.T) {
	text := "let me bash({\"command\":\"df -h\"}) for you"
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if !ok {
		t.Fatal("expected extraction from function invoke")
	}
	if len(calls) != 1 || calls[0].Name != "bash" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestZaiExtractNaturalLanguage(t *testing.T) {
	text := `调用函数：bash 参数：{"command":"echo hi"}`
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if !ok {
		t.Fatal("expected extraction from natural language")
	}
	if len(calls) != 1 || calls[0].Name != "bash" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestZaiExtractRejectsUndeclaredTool(t *testing.T) {
	text := `<tool_calls><tool_call><name>unknown_tool</name><arguments><![CDATA[{"x":1}]]></arguments></tool_call></tool_calls>`
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if ok {
		t.Fatalf("undeclared tool should be rejected, got %+v", calls)
	}
	if len(calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(calls))
	}
}

func TestZaiExtractToolChoiceNone(t *testing.T) {
	text := `<tool_calls><tool_call><name>bash</name><arguments><![CDATA[{"command":"ls"}]]></arguments></tool_call></tool_calls>`
	calls, ok := extractTextToolCalls(text, textTestTools(), "none")
	if ok {
		t.Fatalf("tool_choice=none should disable extraction, got %+v", calls)
	}
	if len(calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(calls))
	}
}

func TestZaiExtractNoTools(t *testing.T) {
	text := "This is just a normal answer without any tools."
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if ok || len(calls) != 0 {
		t.Fatalf("expected no tool calls, got %+v ok=%v", calls, ok)
	}
}

func TestZaiExtractFencedXML(t *testing.T) {
	text := "```xml\n<tool_calls>\n<tool_call>\n<name>bash</name>\n<arguments><![CDATA[{\"command\":\"ls\"}]]></arguments>\n</tool_call>\n</tool_calls>\n```"
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected fenced xml extraction, got %+v ok=%v", calls, ok)
	}
	if calls[0].Name != "bash" {
		t.Fatalf("unexpected name: %s", calls[0].Name)
	}
}

func TestZaiExtractSingleQuotedArgs(t *testing.T) {
	text := `<tool_calls><tool_call><name>bash</name><arguments><![CDATA[{'command':'echo ok'}]]></arguments></tool_call></tool_calls>`
	calls, ok := extractTextToolCalls(text, textTestTools(), nil)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected extraction with single-quoted args, got %+v ok=%v", calls, ok)
	}
	if !strings.Contains(string(calls[0].Arguments), "echo ok") {
		t.Fatalf("unexpected args: %s", calls[0].Arguments)
	}
}

func TestZaiRemoveToolContent(t *testing.T) {
	text := "Let me check.\n\n<tool_calls>\n<tool_call>\n<name>bash</name>\n<arguments><![CDATA[{\"command\":\"ls\"}]]></arguments>\n</tool_call>\n</tool_calls>\n\nDone."
	cleaned := removeTextToolContent(text)
	if strings.Contains(cleaned, "<tool_calls>") || strings.Contains(cleaned, "tool_call") {
		t.Fatalf("tool payload not removed: %q", cleaned)
	}
	if !strings.Contains(cleaned, "Let me check.") {
		t.Fatalf("leading text lost: %q", cleaned)
	}
}
