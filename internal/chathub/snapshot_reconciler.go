package chathub

import "strings"

// snapshotReconciler consumes ChatHub's cumulative DeepLeo text snapshots.
// The snapshot stream is authoritative; writeAtCursor is a redundant channel
// for the same text and must never be concatenated with these values.
// Ported from community PR #86 (commit 7ddf077) by 2164312714-svg.
type snapshotReconciler struct {
	// last mirrors the most recent accepted snapshot. Each frame the upstream
	// resends the full accumulated text, so storing every frame as a distinct
	// string retains O(n^2) bytes across a long answer; the slice below keeps
	// only the two most recent frames (previous + current) for comparison.
	last    string
	prev    string
	prevLen int
	seen    bool
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
		// Release the previous frame so only the last two snapshots are
		// retained; upstream resends the full text every frame and a long
		// answer would otherwise accumulate O(n^2) bytes.
		r.prev = r.last
		r.prevLen = len(r.last)
		r.last = snapshot
		return suffix, suffix != ""
	}
	// ChatHub snapshots are monotonic in the verified protocol captures. Do
	// not attempt byte-level overlap matching: it can split UTF-8 and, more
	// importantly, can turn a rewrite into duplicated public text.
	return "", false
}
