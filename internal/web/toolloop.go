package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type detectedToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func allowedToolNames(tools []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, t := range tools {
		if f, ok := t["function"].(map[string]any); ok {
			if n, ok := f["name"].(string); ok && n != "" {
				out[n] = true
			}
		}
	}
	return out
}

func toolChoiceAllows(choice any, name string) bool {
	if choice == nil {
		return true
	}
	if s, ok := choice.(string); ok {
		return s != "none" && (s != "required" || name != "")
	}
	if m, ok := choice.(map[string]any); ok {
		if f, ok := m["function"].(map[string]any); ok {
			n, _ := f["name"].(string)
			return n == name
		}
		if n, ok := m["name"].(string); ok {
			return n == name
		}
	}
	return true
}

func callID(name, args string, index int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", index, name, args)))
	return "call_" + hex.EncodeToString(h[:8])
}

func extractToolCalls(text string, tools []map[string]any, choice any) ([]detectedToolCall, bool) {
	start := strings.Index(text, "<m365-tool-call>")
	end := strings.Index(text, "</m365-tool-call>")
	if start < 0 || end <= start {
		return nil, false
	}
	var raw any
	if json.Unmarshal([]byte(text[start+len("<m365-tool-call>"):end]), &raw) != nil {
		return nil, false
	}
	items := []any{raw}
	if arr, ok := raw.([]any); ok {
		items = arr
	}
	allowed := allowedToolNames(tools)
	out := make([]detectedToolCall, 0, len(items))
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		n, _ := m["name"].(string)
		if !allowed[n] || !toolChoiceAllows(choice, n) {
			continue
		}
		a, _ := json.Marshal(m["arguments"])
		out = append(out, detectedToolCall{ID: callID(n, string(a), i), Name: n, Arguments: a})
	}
	return out, len(out) > 0
}

func validateToolResult(messages []oaiMsg, known map[string]bool) error {
	for _, m := range messages {
		if m.Role == "tool" {
			if m.ToolCallID == "" {
				return fmt.Errorf("tool_call_id required")
			}
			if len(known) > 0 && !known[m.ToolCallID] {
				return fmt.Errorf("unknown tool_call_id: %s", m.ToolCallID)
			}
		}
	}
	return nil
}
