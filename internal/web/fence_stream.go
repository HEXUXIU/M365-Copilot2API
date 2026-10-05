package web

import "strings"

// fenceStream decides how much of the assistant text can be released to the
// client right now and what has to be held back for the next fragment.
//
// A complete ``` fence is held back because it may turn out to be a tool call
// rather than prose, but only the fence is held: the text that follows it is
// ordinary answer text and must be released, otherwise every reply is truncated
// at its first code block (issue #102). `tail` is how many trailing runes are
// kept so a fence split across fragments is still recognised.
func fenceStream(pending, fragment string, tail int) (emitted, next string) {
	v := pending + fragment
	if i := strings.Index(v, "```"); i >= 0 {
		after := v[i+3:]
		if j := strings.Index(after, "```"); j >= 0 {
			closeIdx := i + 3 + j + 3
			return v[:i] + v[closeIdx:], v[i:closeIdx]
		}
		return v[:i], v[i:]
	}
	if cut := len(v) - tail; cut > 0 {
		return v[:cut], v[cut:]
	}
	return v, ""
}
