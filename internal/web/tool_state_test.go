package web

import "testing"

func TestValidateToolConversationMultiRound(t *testing.T) {
	msgs := []oaiMsg{
		{Role: "user", Content: "find then save"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "a"}, {"id": "b"}}},
		{Role: "tool", ToolCallID: "a", Content: "x"},
		{Role: "tool", ToolCallID: "b", Content: "y"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "c"}}},
		{Role: "tool", ToolCallID: "c", Content: "z"},
		{Role: "assistant", Content: "done"},
	}
	if err := validateToolConversation(msgs); err != nil {
		t.Fatal(err)
	}
}

func TestValidateToolConversationRejectsDuplicateCallIDInSameTurn(t *testing.T) {
	msgs := []oaiMsg{
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "dup"}, {"id": "dup"}}},
		{Role: "tool", ToolCallID: "dup", Content: "ok"},
	}
	if err := validateToolConversation(msgs); err == nil {
		t.Fatal("duplicate call id in same assistant turn accepted")
	}
}

func TestValidateToolConversationRejectsDuplicateResult(t *testing.T) {
	msgs := []oaiMsg{
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "a"}}},
		{Role: "tool", ToolCallID: "a", Content: "one"},
		{Role: "tool", ToolCallID: "a", Content: "two"},
	}
	if err := validateToolConversation(msgs); err == nil {
		t.Fatal("duplicate result accepted")
	}
}

func TestValidateToolConversationRejectsMissing(t *testing.T) {
	if err := validateToolConversation([]oaiMsg{{Role: "assistant", ToolCalls: []map[string]any{{"id": "a"}}}}); err == nil {
		t.Fatal("missing result accepted")
	}
	if err := validateToolConversation([]oaiMsg{{Role: "assistant", ToolCalls: []map[string]any{{"id": "a"}}}, {Role: "tool", ToolCallID: "b"}}); err == nil {
		t.Fatal("wrong result accepted")
	}
	if err := validateToolConversation([]oaiMsg{{Role: "tool", ToolCallID: "a"}}); err == nil {
		t.Fatal("orphan result accepted")
	}
}
