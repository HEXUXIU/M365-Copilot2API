package web

import "testing"

// TestUnbindExplicitReverseIndex verifies that dropping a session removes all
// explicit bindings pointing at it via the reverse index (no stale entries).
func TestUnbindExplicitReverseIndex(t *testing.T) {
	sr := openSessionResolver()
	defer func() {
		sr.mu.Lock()
		sr.mu.Unlock()
	}()

	sr.mu.Lock()
	sr.reindexExplicitLocked("sess-1", "exp-a")
	sr.reindexExplicitLocked("sess-1", "exp-b")
	sr.reindexExplicitLocked("sess-2", "exp-c")
	if len(sr.byExplicit) != 3 || len(sr.explicitBySession) != 2 {
		sr.mu.Unlock()
		t.Fatalf("index setup wrong: byExplicit=%d explicitBySession=%d", len(sr.byExplicit), len(sr.explicitBySession))
	}

	// Drop sess-1: exp-a and exp-b must go, exp-c must survive.
	sr.unbindExplicitForSessionLocked("sess-1")
	_, a := sr.byExplicit["exp-a"]
	_, b := sr.byExplicit["exp-b"]
	c, cOK := sr.byExplicit["exp-c"]
	sr.mu.Unlock()

	if a || b {
		t.Fatalf("stale explicit entries after unbind: exp-a=%v exp-b=%v", a, b)
	}
	if !cOK || c != "sess-2" {
		t.Fatalf("unrelated binding lost or wrong: exp-c=%q ok=%v", c, cOK)
	}
}
