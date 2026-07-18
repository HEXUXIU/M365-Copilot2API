package web

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompactToolResultKeepsHeadTailAndError(t *testing.T) {
	s := "start\n" + strings.Repeat("progress line\n", 1000) + "ERROR: build failed\nexit code 1"
	got := compactToolResult(s, 800)
	if len(got) > 900 || !strings.Contains(got, "start") || !strings.Contains(got, "ERROR: build failed") || !strings.Contains(got, "exit code 1") || !strings.Contains(got, "truncated") {
		t.Fatalf("bad compact result: %d %q", len(got), got)
	}
}

func TestAgentLedgerDetectsRepeatedFailure(t *testing.T) {
	msgs := []oaiMsg{
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "c1", "type": "function", "function": map[string]any{"name": "run", "arguments": "{\"cmd\":\"build\"}"}}}},
		{Role: "tool", ToolCallID: "c1", Content: "exit code 1: failed"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "c2", "type": "function", "function": map[string]any{"name": "run", "arguments": "{\"cmd\":\"build\"}"}}}},
		{Role: "tool", ToolCallID: "c2", Content: "exit code 1: failed"},
	}
	l := buildAgentLedger(msgs)
	if !l.RepeatedFailure {
		t.Fatalf("expected repeated failure: %+v", l)
	}
	if !strings.Contains(l.RouterContext(), "change strategy") {
		t.Fatal(l.RouterContext())
	}
}

func TestAgentLedgerEvidenceAndUniqueCallIDs(t *testing.T) {
	a := scopedCallID("run", "{}", 0, "turn-a")
	b := scopedCallID("run", "{}", 0, "turn-b")
	if a == b {
		t.Fatal("call IDs collide across turns")
	}
	l := buildAgentLedger([]oaiMsg{{Role: "assistant", ToolCalls: []map[string]any{{"id": "c1", "type": "function", "function": map[string]any{"name": "create", "arguments": "{}"}}}}, {Role: "tool", ToolCallID: "c1", Content: "created"}})
	if len(l.Completed) != 1 || !strings.Contains(l.RouterContext(), "c1") {
		t.Fatalf("missing evidence: %+v", l)
	}
}

func TestAgentLedgerDetectsRepeatedCallAndRoundLimit(t *testing.T) {
	var msgs []oaiMsg
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("c%d", i)
		msgs = append(msgs, oaiMsg{Role: "assistant", ToolCalls: []map[string]any{{"id": id, "type": "function", "function": map[string]any{"name": "poll", "arguments": "{\"id\":1}"}}}}, oaiMsg{Role: "tool", ToolCallID: id, Content: "still pending"})
	}
	l := buildAgentLedger(msgs)
	if !l.RepeatedCall || l.ToolRounds != 4 {
		t.Fatalf("loop not detected: %+v", l)
	}
	if err := l.CanContinue(3); err == nil {
		t.Fatal("expected round limit")
	}
}

func TestCompletionGuardRejectsPendingAndUnsupportedSuccess(t *testing.T) {
	l := buildAgentLedger([]oaiMsg{
		{Role: "assistant", ToolCalls: []map[string]any{
			{"id": "p1", "type": "function", "function": map[string]any{"name": "deploy", "arguments": "{}"}},
		}},
	})
	if completionEvidenceAllows("Deployment completed successfully", l) {
		t.Fatal("pending action allowed as complete")
	}
}

func TestCompletionGuardRejectsUnsupportedSuccess(t *testing.T) {
	if completionEvidenceAllows("Installed, started, and verified successfully", buildAgentLedger(nil)) {
		t.Fatal("unsupported success allowed")
	}
	if !completionEvidenceAllows("I cannot confirm completion because no tool results were returned.", buildAgentLedger(nil)) {
		t.Fatal("honest incomplete response rejected")
	}
}
