package chathub

import (
	"strings"
	"testing"
)

func TestChatWithHandlersUsesPoolOnlyForFreshRequests(t *testing.T) {
	// Continuation requests must never take from or return to the pool:
	// the pooled socket is bound to random URL IDs and a continuation would
	// complete immediately with no text (community PR #62/#86).
	if poolEligibleFor(Request{ConversationID: "conv-1"}) {
		t.Fatal("continuation with conversation id must not be pool eligible")
	}
	if poolEligibleFor(Request{SessionID: "sess-1"}) {
		t.Fatal("continuation with session id must not be pool eligible")
	}
	if !poolEligibleFor(Request{}) {
		t.Fatal("fresh request must be pool eligible")
	}
	if poolEligibleFor(Request{ConversationID: "c", SessionID: "s"}) {
		t.Fatal("continuation with both ids must not be pool eligible")
	}
}

func TestSnapshotReconcilerIntegrationMonotonicStream(t *testing.T) {
	var r snapshotReconciler
	var out strings.Builder
	for _, snap := range []string{"A", "AB", "ABC", "ABCD"} {
		suffix, ok := r.Apply(snap)
		if ok {
			out.WriteString(suffix)
		}
	}
	if out.String() != "ABCD" {
		t.Fatalf("reconstructed=%q", out.String())
	}
}
