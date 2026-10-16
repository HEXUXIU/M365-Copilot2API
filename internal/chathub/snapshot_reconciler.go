package chathub

import (
	"strings"
	"unicode/utf8"
)

// snapshotReconciler reconciles ChatHub's writeAtCursor deltas and cumulative
// DeepLeo text snapshots without duplicating text or dropping multi-message
// segments.
type snapshotReconciler struct {
	last    string
	prev    string
	prevLen int
	seen    bool
	msgID   string
}

// ApplyDelta consumes a writeAtCursor delta for the active bot message and
// advances `last` so subsequent cumulative snapshots for the same message only
// emit any unseen tail beyond what writeAtCursor already delivered.
func (r *snapshotReconciler) ApplyDelta(delta string) (string, bool) {
	if delta == "" {
		return "", false
	}
	if !r.seen {
		r.seen = true
		r.last = delta
		return delta, true
	}
	r.prev = r.last
	r.prevLen = len(r.last)
	r.last += delta
	return delta, true
}

// ApplyForMessage switches segment tracking when ChatHub starts a new bot
// message ID within the same turn (for example, a one-line intro message
// followed by a separate code-block message).
func (r *snapshotReconciler) ApplyForMessage(msgID, snapshot string) (string, bool) {
	if msgID != "" && r.msgID != "" && msgID != r.msgID {
		r.msgID = msgID
		r.prev = r.last
		r.prevLen = len(r.last)
		r.last = snapshot
		if snapshot == "" {
			return "", false
		}
		if r.prev != "" && !strings.HasSuffix(r.prev, "\n") && !strings.HasPrefix(snapshot, "\n") {
			return "\n" + snapshot, true
		}
		return snapshot, true
	}
	if msgID != "" && r.msgID == "" {
		r.msgID = msgID
	}
	return r.Apply(snapshot)
}

// Apply returns only text not already emitted from the accepted snapshot
// sequence. A shorter or non-prefix snapshot is an out-of-order/rewrite frame
// and is ignored unless it clearly starts a new fenced block segment.
func (r *snapshotReconciler) Apply(snapshot string) (string, bool) {
	if snapshot == "" {
		return "", false
	}
	// Reject a snapshot whose trailing bytes split a multi-byte UTF-8 rune.
	if !utf8.ValidString(snapshot) {
		return "", false
	}
	if !r.seen {
		r.seen = true
		r.last = snapshot
		return snapshot, true
	}
	if strings.HasPrefix(snapshot, r.last) {
		suffix := snapshot[len(r.last):]
		r.prev = r.last
		r.prevLen = len(r.last)
		r.last = snapshot
		return suffix, suffix != ""
	}
	// When ChatHub starts a new fenced block segment after an intro sentence
	// without changing messageId (snapshot resets to "```..."), reset segment
	// tracking so the new block streams instead of being skipped.
	if strings.HasPrefix(strings.TrimSpace(snapshot), "```") && !strings.HasPrefix(strings.TrimSpace(r.last), "```") {
		sep := ""
		if r.last != "" && !strings.HasSuffix(r.last, "\n") {
			sep = "\n"
		}
		r.prev = r.last
		r.prevLen = len(r.last)
		r.last = snapshot
		return sep + snapshot, true
	}
	return "", false
}
