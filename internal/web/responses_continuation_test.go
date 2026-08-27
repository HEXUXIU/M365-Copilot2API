package web

import "testing"

func TestMergeResponsesContinuationKeepsToolOutputAdjacent(t *testing.T) {
	policy := oaiMsg{Role: "system", Content: customExecWorkspaceInstruction}
	parent := []oaiMsg{
		policy,
		{Role: "user", Content: "create a file"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "call_1", "type": "custom", "function": map[string]any{"name": "exec", "arguments": `{"input":"pwd"}`}}}},
	}
	current := []oaiMsg{
		policy,
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}

	merged := mergeResponsesContinuation(parent, current)
	if len(merged) != 4 {
		t.Fatalf("merged messages=%#v", merged)
	}
	if merged[2].Role != "assistant" || merged[3].Role != "tool" || merged[3].ToolCallID != "call_1" {
		t.Fatalf("tool output is not adjacent to its call: %#v", merged)
	}
	if _, err := normalizeResponsesToolHistory(merged); err != nil {
		t.Fatalf("normalized continuation: %v", err)
	}
}

func TestMergeResponsesContinuationMovesNewPolicyBeforePendingCall(t *testing.T) {
	parent := []oaiMsg{
		{Role: "user", Content: "run"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "call_2", "type": "function", "function": map[string]any{"name": "exec", "arguments": `{}`}}}},
	}
	current := []oaiMsg{
		{Role: "system", Content: "new request policy"},
		{Role: "tool", ToolCallID: "call_2", Content: "done"},
	}

	merged := mergeResponsesContinuation(parent, current)
	if len(merged) != 4 || merged[0].Role != "system" || merged[2].Role != "assistant" || merged[3].Role != "tool" {
		t.Fatalf("unexpected merge order: %#v", merged)
	}
	if _, err := normalizeResponsesToolHistory(merged); err != nil {
		t.Fatalf("normalized continuation: %v", err)
	}
}

func TestMergeResponsesContinuationPreservesPolicyOrder(t *testing.T) {
	merged := mergeResponsesContinuation(nil, []oaiMsg{
		{Role: "system", Content: "first"},
		{Role: "system", Content: "second"},
		{Role: "tool", ToolCallID: "call_3", Content: "done"},
	})
	if len(merged) != 3 || contentToString(merged[0].Content) != "first" || contentToString(merged[1].Content) != "second" {
		t.Fatalf("policy order changed: %#v", merged)
	}
}
