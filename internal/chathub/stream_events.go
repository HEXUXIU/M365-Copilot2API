package chathub

import "encoding/json"

// classifyUpdateMessages converts a ChatHub messages array into protocol-neutral
// events. It deliberately does not infer tools from ordinary prose.
func classifyUpdateMessages(messages []any) []StreamEvent {
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
		name, args := extractToolFields(m)
		if name != "" && len(args) > 0 {
			kind = "tool"
		}
		if text == "" && kind == "text" {
			continue
		}
		out = append(out, StreamEvent{Kind: kind, Text: text, MessageType: mt, ContentType: ct, ToolName: name, Arguments: args})
	}
	return out
}

func extractToolFields(m map[string]any) (string, json.RawMessage) {
	var name string
	for _, k := range []string{"name", "toolName", "pluginName", "functionName"} {
		if v, ok := m[k].(string); ok && v != "" {
			name = v
			break
		}
	}
	if name == "" {
		return "", nil
	}
	for _, k := range []string{"arguments", "args", "parameters", "input", "functionArguments"} {
		if v, ok := m[k]; ok {
			b, err := json.Marshal(v)
			if err == nil && len(b) > 0 {
				return name, b
			}
		}
	}
	return "", nil
}

func eventRaw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

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
			name, args := extractToolFields(z)
			if name != "" && len(args) > 0 {
				key := name + "|" + string(args)
				if !seen[key] {
					seen[key] = true
					out = append(out, StreamEvent{Kind: "tool", ToolName: name, Arguments: args, Raw: eventRaw(z)})
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
