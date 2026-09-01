package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMergeResponsesContinuationKeepsToolOutputAdjacent(t *testing.T) {
	policy := oaiMsg{Role: "system", Content: customExecEffectiveInstruction}
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

func TestResponseToolProgressDoesNotBreakAffinityPrefix(t *testing.T) {
	policy := oaiMsg{Role: "system", Content: "caller-local tool policy"}
	calls := []map[string]any{{
		"id": "call_1", "type": "function",
		"function": map[string]any{"name": "inspect", "arguments": `{"path":"/etc/os-release"}`},
	}}
	base := []oaiMsg{policy, {Role: "user", Content: "inspect the runtime"}}
	r := carryResponsesAdapter(httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	assistant := responseAffinityAssistantHistory(r, "inspect the runtime", oaiMsg{Role: "assistant", ToolCalls: calls})
	bindingHistory := affinityBindingHistory(base, assistant)
	stored := appendResponsesAssistantHistory(base, toolProgressText("inspect the runtime", []detectedToolCall{{Name: "inspect"}}), calls)
	if got, want := contentToString(stored[len(stored)-1].Content), contentToString(assistant.Content); got != want {
		t.Fatalf("stored progress=%q, affinity progress=%q", got, want)
	}
	continued := mergeResponsesContinuation(stored, []oaiMsg{
		policy,
		{Role: "tool", ToolCallID: "call_1", Content: "PRETTY_NAME=Ubuntu"},
	})
	binding := affinityBinding{HistoryCount: len(bindingHistory), HistoryDigest: historyDigest(bindingHistory)}
	if got := contextPrefixCountForBinding(binding, continued); got != len(bindingHistory) {
		t.Fatalf("tool continuation prefix=%d, want %d", got, len(bindingHistory))
	}
}

func TestResponseAffinityProgressKeepsLiteralPublicProgress(t *testing.T) {
	r := carryResponsesAdapter(httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	assistant := responseAffinityAssistantHistory(r, "internal router framing", oaiMsg{
		Role:      "assistant",
		Content:   "\u6211\u5148\u5904\u7406\u8fd9\u4e00\u6b65\uff0c\u5e76\u6838\u5bf9\u8fd4\u56de\u7ed3\u679c\u3002",
		ToolCalls: []map[string]any{{"id": "call_1", "type": "function", "function": map[string]any{"name": "inspect", "arguments": `{}`}}},
	})
	if got, want := contentToString(assistant.Content), "\u6211\u5148\u5904\u7406\u8fd9\u4e00\u6b65\uff0c\u5e76\u6838\u5bf9\u8fd4\u56de\u7ed3\u679c\u3002"; got != want {
		t.Fatalf("public progress changed: got=%q want=%q", got, want)
	}
}
