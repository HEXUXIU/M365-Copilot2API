package web

import (
	"encoding/json"
	"m365-native/internal/chathub"
)

func xmlToolCalls(text string, tools []map[string]any, choice any) []detectedToolCall {
	calls, _ := extractToolCalls(text, tools, choice)
	return calls
}

// Keep this conversion isolated so XML and native ChatHub events share the same
// OpenAI response shape. The event payload remains available under m365.
func toolCallMaps(calls []detectedToolCall) []any {
	out := make([]any, 0, len(calls))
	for _, c := range calls {
		out = append(out, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(c.Arguments)}})
	}
	return out
}

var _ = json.RawMessage{}
var _ chathub.Tool
