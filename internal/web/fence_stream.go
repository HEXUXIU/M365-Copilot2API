package web

import "strings"

// fenceStream decides how much of the assistant text can be released to the
// client right now and what has to be held back for the next fragment.
//
// A closed ``` fence may turn out to be a tool call rather than prose, so it is
// only held while that is still undecided. `hold` reports whether a given
// fence is a tool call: those stay buffered for fencedToolCalls to consume at
// the end of the stream, everything else is released immediately and in order.
//
// Releasing in order is what keeps a reply intact (issue #102). Holding a fence
// after text that followed it would emit that text first and the fence at the
// end, so two fences end up adjacent as a run of six backticks and the client
// renders garbage.
//
// `tail` is how many trailing runes are kept so a fence split across fragments
// is still recognised.
func fenceStream(pending, fragment string, tail int, hold func(fence string) bool) (emitted, next string) {
	v := pending + fragment
	if i := strings.Index(v, "```"); i >= 0 {
		after := v[i+3:]
		if j := strings.Index(after, "```"); j >= 0 {
			closeIdx := i + 3 + j + 3
			fence := v[i:closeIdx]
			if hold != nil && hold(fence) {
				// Keep the tail too: it belongs after the tool call, and dropping
				// it here would truncate the reply at the tool call.
				return v[:i] + v[closeIdx:], fence
			}
			return v[:i] + fence + v[closeIdx:], ""
		}
		return v[:i], v[i:]
	}
	if cut := len(v) - tail; cut > 0 {
		return v[:cut], v[cut:]
	}
	return v, ""
}
