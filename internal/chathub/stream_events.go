package chathub

import (
	"encoding/json"
	"strings"
)

// classifyUpdateMessages converts a ChatHub messages array into protocol-neutral
// events. It deliberately does not infer tools from ordinary prose.
func classifyUpdateMessages(messages []any) []StreamEvent {
	return classifyUpdateMessagesWithSeen(messages, nil)
}

// classifyUpdateMessagesWithSeen shares the stream-level tool identity set
// with extractToolEvents. A native call can appear both in the update envelope
// and in messages[]; emitting both copies makes clients execute it twice.
func classifyUpdateMessagesWithSeen(messages []any, seen map[string]bool) []StreamEvent {
	var out []StreamEvent
	for _, raw := range messages {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		text, _ := m["text"].(string)
		mt, _ := m["messageType"].(string)
		ct, _ := m["contentType"].(string)
		origin, _ := m["contentOrigin"].(string)
		messageID, _ := m["messageId"].(string)
		cot, _ := m["addToChainOfThought"].(bool)
		kind := "text"
		if mt == "Progress" || ct == "SearchResults" || ct == "Code" || ct == "ToolCall" {
			kind = "progress"
		}
		// ChatHub marks the multi-step reasoning transcript (ChainOfThought cards)
		// via contentOrigin and addToChainOfThought. Expose it separately so the
		// OpenAI-compatible layer can render it as reasoning_content.
		if origin == "ChainOfThoughtSummary" || cot || (mt == "Progress" && ct == "EarlyProgress") {
			kind = "reasoning"
		}
		name, args, callID := extractToolFields(m)
		if name != "" && len(args) > 0 {
			kind = "tool"
		}
		if text == "" && kind == "text" {
			continue
		}
		if kind == "tool" && seen != nil {
			key := toolEventKey(name, args, callID)
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, StreamEvent{Kind: kind, Text: text, MessageType: mt, ContentType: ct, ContentOrigin: origin, MessageID: messageID, ToolCallID: callID, ToolName: name, Arguments: args})
	}
	return out
}

func extractToolFields(m map[string]any) (string, json.RawMessage, string) {
	callID := ""
	for _, k := range []string{"callId", "call_id", "toolCallId", "tool_call_id", "messageId", "message_id"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			callID = strings.TrimSpace(v)
			break
		}
	}
	var name string
	for _, k := range []string{"name", "toolName", "pluginName", "functionName"} {
		if v, ok := m[k].(string); ok && v != "" {
			name = v
			break
		}
	}
	if name == "" {
		return "", nil, callID
	}
	for _, k := range []string{"arguments", "args", "parameters", "input", "functionArguments"} {
		if v, ok := m[k]; ok {
			b, err := json.Marshal(v)
			if err == nil && len(b) > 0 {
				return name, b, callID
			}
		}
	}
	return "", nil, callID
}

func eventRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func toolEventKey(name string, args json.RawMessage, callID string) string {
	if strings.TrimSpace(callID) != "" {
		return "id:" + strings.TrimSpace(callID)
	}
	var value any
	if json.Unmarshal(args, &value) == nil {
		if canonical, err := json.Marshal(value); err == nil {
			return name + "|args:" + string(canonical)
		}
	}
	return name + "|args:" + string(args)
}

// extractToolEvents walks the complete SignalR update argument. ChatHub often
// places native plugin calls outside messages[], so looking only at messages
// loses the call after the assistant's preamble.
func extractToolEvents(v any, seen map[string]bool) []StreamEvent {
	var out []StreamEvent
	var walk func(any)
	walk = func(x any) {
		switch z := x.(type) {
		case []any:
			for _, item := range z {
				walk(item)
			}
		case map[string]any:
			name, args, callID := extractToolFields(z)
			if name != "" && len(args) > 0 {
				key := toolEventKey(name, args, callID)
				if !seen[key] {
					seen[key] = true
					out = append(out, StreamEvent{Kind: "tool", ToolCallID: callID, ToolName: name, Arguments: args, Raw: eventRaw(z)})
				}
			}
			for _, child := range z {
				walk(child)
			}
		}
	}
	walk(v)
	return out
}
