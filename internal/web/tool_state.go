package web

import "fmt"

// validateToolConversation enforces the OpenAI tool protocol without making
// assumptions about what a tool does. Every assistant call must be followed by
// exactly one matching tool result before another model turn is requested.
func validateToolConversation(messages []oaiMsg) error {
	pending := map[string]bool{}
	completed := map[string]bool{}
	for i, m := range messages {
		switch m.Role {
		case "assistant":
			if len(pending) > 0 {
				return fmt.Errorf("tool results missing before assistant message at index %d", i)
			}
			for _, call := range m.ToolCalls {
				id, _ := call["id"].(string)
				if id == "" {
					return fmt.Errorf("assistant tool call missing id at index %d", i)
				}
				if pending[id] || completed[id] {
					return fmt.Errorf("duplicate tool call id: %s", id)
				}
				pending[id] = true
			}
		case "tool":
			if m.ToolCallID == "" {
				return fmt.Errorf("tool_call_id required at index %d", i)
			}
			if !pending[m.ToolCallID] {
				return fmt.Errorf("unexpected tool result: %s", m.ToolCallID)
			}
			delete(pending, m.ToolCallID)
			completed[m.ToolCallID] = true
		}
	}
	if len(pending) > 0 {
		for id := range pending {
			return fmt.Errorf("missing tool result for tool_call_id: %s", id)
		}
	}
	return nil
}

// shouldUseToolRouter selects the legacy XML translator only for an explicit
// tool turn. A client may send its default tool catalog on every request; that
// alone must not turn ordinary text into a planner round-trip.
func shouldUseToolRouter(messages []oaiMsg, tools []map[string]any, choice any) bool {
	if len(tools) == 0 {
		return false
	}
	mode := normalizedToolChoiceMode(choice)
	if mode == "none" {
		return false
	}
	if mode == "required" || len(mode) > len("named:") && mode[:len("named:")] == "named:" {
		return true
	}
	for _, m := range messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			return true
		}
		if m.Role == "tool" {
			return true
		}
	}
	return false
}
