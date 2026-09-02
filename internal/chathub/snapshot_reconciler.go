package chathub

import "strings"

// snapshotReconciler consumes ChatHub's cumulative DeepLeo text snapshots.
// The snapshot stream is authoritative; writeAtCursor is a redundant channel
// for the same text and must never be concatenated with these values.
type snapshotReconciler struct {
	last string
	seen bool
}

// Apply returns only text not already emitted from the accepted snapshot
// sequence. A shorter or non-prefix snapshot is an out-of-order/rewrite frame
// and is ignored until a later authoritative snapshot or final result arrives.
func (r *snapshotReconciler) Apply(snapshot string) (string, bool) {
	if snapshot == "" {
		return "", false
	}
	if !r.seen {
		r.seen = true
		r.last = snapshot
		return snapshot, true
	}
	if strings.HasPrefix(snapshot, r.last) {
		suffix := snapshot[len(r.last):]
		r.last = snapshot
		return suffix, suffix != ""
	}
	// ChatHub snapshots are monotonic in the verified protocol captures. Do
	// not attempt byte-level overlap matching: it can split UTF-8 and, more
	// importantly, can turn a rewrite into duplicated public text.
	return "", false
}
