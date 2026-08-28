package web

import (
	"strings"
)

const streamToolJSONPrefixLimit = 64

func consumeStreamText(pending *strings.Builder, chunk string, emit func(string) error) error {
	pending.WriteString(chunk)
	v := pending.String()
	if len(v) > streamToolJSONPrefixLimit && strings.HasPrefix(v, "{") && !strings.Contains(v, "\n") {
		pending.Reset()
		return emit(v)
	}
	if strings.HasPrefix(v, "{\"co") || strings.HasPrefix(v, "```ba") || strings.HasPrefix(v, "{\"command") {
		return nil
	}
	pending.Reset()
	return emit(v)
}

func bufferFinalStreamText(text, pending *strings.Builder, final string) {
	if text.Len() != 0 || strings.TrimSpace(final) == "" {
		return
	}
	text.WriteString(final)
	pending.WriteString(final)
}
