package web

import "testing"

func TestNormalizeEmptyToolResultsKeepsMissingOutputPending(t *testing.T) {
	messages := []oaiMsg{
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "call_1"}}},
		{Role: "tool", ToolCallID: "call_1", Content: ""},
	}
	normalizeEmptyToolResults(messages)
	if messages[1].Content != emptyToolOutputPlaceholder {
		t.Fatalf("empty output=%#v", messages[1].Content)
	}
	ledger := buildAgentLedger(messages)
	if len(ledger.Completed) != 0 || len(ledger.Pending) != 1 {
		t.Fatalf("ledger=%#v", ledger)
	}
}
