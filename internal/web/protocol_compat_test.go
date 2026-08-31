package web

import (
	"fmt"
	"strings"
	"testing"
)

func TestResponsesToOpenAI(t *testing.T) {
	r := responsesRequest{Model: "m", Input: "what time", Tools: []map[string]any{{"type": "function", "name": "clock", "parameters": map[string]any{"type": "object"}}}}
	o, err := r.openAI()
	if err != nil || len(o.Messages) != 1 || len(o.Tools) != 1 {
		t.Fatalf("%+v %v", o, err)
	}
}

func TestResponsesCustomExecToOpenAI(t *testing.T) {
	r := responsesRequest{Model: "m", Input: "inspect", Tools: []map[string]any{{"type": "custom", "name": "exec", "description": "run a command", "format": map[string]any{"type": "grammar"}}}}
	o, err := r.openAI()
	if err != nil || len(o.Tools) != 1 || o.Tools[0].Type != "custom" {
		t.Fatalf("tools=%+v err=%v", o.Tools, err)
	}
	if string(o.Tools[0].Function) == "" || !containsJSON(o.Tools[0].Function, "input") {
		t.Fatalf("custom exec did not receive an input schema: %s", o.Tools[0].Function)
	}
}

func TestResponsesCustomApplyPatchToOpenAI(t *testing.T) {
	r := responsesRequest{
		Model: "m",
		Input: "在当前目录创建 1.txt，必须实际调用 apply_patch 工具。",
		Tools: []map[string]any{{
			"type": "custom", "name": "apply_patch", "description": "apply a patch",
			"format": map[string]any{"type": "grammar"},
		}},
	}
	o, err := r.openAI()
	if err != nil || len(o.Tools) != 1 || o.Tools[0].Type != "custom" {
		t.Fatalf("tools=%+v err=%v", o.Tools, err)
	}
	if !containsJSON(o.Tools[0].Function, "apply_patch") || !containsJSON(o.Tools[0].Function, "input") {
		t.Fatalf("custom apply_patch was not bridged: %s", o.Tools[0].Function)
	}
	if !o.ExplicitToolRequired {
		t.Fatal("explicit custom apply_patch request was not required")
	}
}

func TestResponsesAdditionalToolsItemToOpenAI(t *testing.T) {
	r := responsesRequest{Input: []any{
		map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{
			map[string]any{"type": "custom", "name": "exec", "description": "run a command", "format": map[string]any{"type": "grammar"}},
			map[string]any{"type": "custom", "name": "apply_patch", "description": "apply a patch", "format": map[string]any{"type": "grammar"}},
			map[string]any{"type": "function", "name": "wait", "parameters": map[string]any{"type": "object"}},
			map[string]any{"type": "namespace", "name": "collaboration", "tools": []any{}},
		}},
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "必须实际调用工具"}}},
	}}
	o, err := r.openAI()
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Tools) != 2 || o.Tools[0].Type != "custom" || o.Tools[1].Type != "custom" {
		t.Fatalf("tools=%#v, want inline custom exec and apply_patch tools", o.Tools)
	}
	if len(o.Messages) != 2 || o.Messages[1].Role != "user" {
		t.Fatalf("additional_tools leaked into messages: %#v", o.Messages)
	}
	if !o.ExplicitToolRequired {
		t.Fatal("explicit tool request was not preserved")
	}
}

func TestResponsesPreservesExplicitToolRequestBeforeInternalUserItem(t *testing.T) {
	r := responsesRequest{Input: []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "请读取 go.mod，必须实际调用工具。"}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "You have 100 weighted tokens left"}}},
	}}
	o, err := r.openAI()
	if err != nil {
		t.Fatal(err)
	}
	if !o.ExplicitToolRequired {
		t.Fatal("explicit tool requirement was lost after an internal user item")
	}
}

func TestResponsesPromotesDirectWorkspaceRequestBeforeInternalUserItem(t *testing.T) {
	r := responsesRequest{Tools: []map[string]any{{"type": "custom", "name": "exec", "description": "run a command"}}, Input: []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "在当前目录创建 1.txt 并写入 123214324"}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "You have 100 weighted tokens left"}}},
	}}
	o, err := r.openAI()
	if err != nil {
		t.Fatal(err)
	}
	if !o.ExplicitToolRequired {
		t.Fatal("direct workspace request was lost after an internal user item")
	}
}

func TestResponsesClearsExplicitToolRequestAfterToolOutput(t *testing.T) {
	r := responsesRequest{Input: []any{
		map[string]any{"role": "user", "content": "必须实际调用工具"},
		map[string]any{"type": "custom_tool_call", "call_id": "call_exec", "name": "exec", "input": "pwd"},
		map[string]any{"type": "custom_tool_call_output", "call_id": "call_exec", "output": "C:/project"},
	}}
	o, err := r.openAI()
	if err != nil {
		t.Fatal(err)
	}
	if o.ExplicitToolRequired {
		t.Fatal("completed tool output kept the required-call flag")
	}
}

func TestResponsesCustomExecKeepsLocalCustomToolsAndExcludesNativeFunctions(t *testing.T) {
	r := responsesRequest{Input: "edit the project", Tools: []map[string]any{
		{"type": "custom", "name": "exec", "description": "local execution"},
		{"type": "custom", "name": "apply_patch", "description": "apply a patch"},
		{"type": "function", "name": "m365_search", "description": "native search"},
	}}
	o, err := r.openAI()
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Tools) != 2 || o.Tools[0].Type != "custom" || o.Tools[1].Type != "custom" {
		t.Fatalf("tools=%#v, want exec and apply_patch custom tools", o.Tools)
	}
	if !containsJSON(o.Tools[0].Function, "exec") || !containsJSON(o.Tools[1].Function, "apply_patch") {
		t.Fatalf("custom tools were not preserved in order: %#v", o.Tools)
	}
	if !strings.Contains(fmt.Sprint(o.Messages[0].Content), "Never use") {
		t.Fatalf("missing native-tool prohibition: %#v", o.Messages)
	}
}

func TestResponsesInstructionsAndCustomExecPolicyAreSystemMessages(t *testing.T) {
	r := responsesRequest{
		Instructions: "Use the repository selected by the caller.",
		Input:        "inspect the repository",
		Tools:        []map[string]any{{"type": "custom", "name": "exec", "description": "run a command"}},
	}
	o, err := r.openAI()
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Messages) != 3 {
		t.Fatalf("messages=%#v", o.Messages)
	}
	if o.Messages[0].Role != "system" || o.Messages[0].Content != customExecWorkspaceInstruction {
		t.Fatalf("missing custom exec policy: %#v", o.Messages[0])
	}
	if o.Messages[1].Role != "system" || o.Messages[1].Content != r.Instructions {
		t.Fatalf("instructions not preserved: %#v", o.Messages[1])
	}
	if o.Messages[2].Role != "user" || o.Messages[2].Content != r.Input {
		t.Fatalf("input ordering changed: %#v", o.Messages[2])
	}
	policy := fmt.Sprint(o.Messages[0].Content)
	for _, want := range []string{"caller-provided shell contract is authoritative", "does not mean", "remote container"} {
		if !strings.Contains(policy, want) {
			t.Fatalf("custom exec policy missing %q: %q", want, policy)
		}
	}
}

func TestResponsesCustomToolOutputToOpenAI(t *testing.T) {
	r := responsesRequest{Input: []any{
		map[string]any{"type": "custom_tool_call", "call_id": "call_exec", "name": "exec", "input": "uname -s"},
		map[string]any{"type": "custom_tool_call_output", "call_id": "call_exec", "output": "Linux"},
	}}
	o, err := r.openAI()
	if err != nil || len(o.Messages) != 2 || o.Messages[0].Role != "assistant" || o.Messages[0].ToolCalls[0]["type"] != "custom" || o.Messages[1].Role != "tool" || o.Messages[1].ToolCallID != "call_exec" {
		t.Fatalf("messages=%+v err=%v", o.Messages, err)
	}
	if err := validateToolConversation(o.Messages); err != nil {
		t.Fatalf("custom tool continuation rejected: %v", err)
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
