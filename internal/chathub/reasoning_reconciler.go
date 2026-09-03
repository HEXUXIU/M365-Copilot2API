package chathub

import "strings"

// reasoningReconciler consumes cumulative ChainOfThoughtSummary snapshots.
// ChatHub may resend the same card on several update frames; only the unseen
// suffix is exposed to the public adapters.
type reasoningReconciler struct {
	last string
}

func (r *reasoningReconciler) Apply(snapshot string) string {
	if r == nil || snapshot == "" {
		return ""
	}
	if r.last == "" {
		r.last = snapshot
		return snapshot
	}
	if snapshot == r.last {
		return ""
	}
	if strings.HasPrefix(snapshot, r.last) {
		suffix := snapshot[len(r.last):]
		r.last = snapshot
		return suffix
	}
	// A shorter or rewritten snapshot is stale evidence. Waiting for the next
	// monotonic frame prevents duplicated or contradictory public prose.
	if len(snapshot) <= len(r.last) {
		return ""
	}
	return ""
}
