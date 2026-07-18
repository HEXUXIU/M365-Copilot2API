package web

import (
	"encoding/json"
	"regexp"
	"strings"
)

var fencedToolCall = regexp.MustCompile("(?s)```([A-Za-z0-9_-]+)\\s*\\n(.*?)\\n```")

func fencedToolCalls(text string, tools []map[string]any, choice any) []detectedToolCall {
	allowed := allowedToolNames(tools)
	var out []detectedToolCall
	for _, m := range fencedToolCall.FindAllStringSubmatch(text, -1) {
		name := m[1]
		if !allowed[name] || !toolChoiceAllows(choice, name) {
			continue
		}
		args := strings.TrimSpace(m[2])
		var v any
		if json.Unmarshal([]byte(args), &v) != nil {
			continue
		}
		b, _ := json.Marshal(v)
		out = append(out, detectedToolCall{ID: callID(name, string(b), len(out)), Type: toolType(name, tools), Name: name, Arguments: b})
	}
	return out
}
