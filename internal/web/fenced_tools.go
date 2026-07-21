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
		args := strings.TrimSpace(m[2])
		var v any
		_ = json.Unmarshal([]byte(args), &v)
		// Auto-convert bash/shell code blocks to tool calls
		if !allowed[name] && (name == "bash" || name == "sh" || name == "shell" || name == "powershell" || name == "cmd") {
			if m, ok := v.(map[string]any); ok {
				if cmd, hasCmd := m["command"]; hasCmd && cmd != "" {
					cmdBytes, _ := json.Marshal(map[string]any{"command": cmd, "timeout": m["timeout"], "workdir": m["workdir"]})
					out = append(out, detectedToolCall{ID: callID("bash", string(cmdBytes), len(out)), Type: "function", Name: "bash", Arguments: cmdBytes})
					continue
				}
			}
			if v == nil {
				cmdBytes, _ := json.Marshal(map[string]any{"command": args})
				out = append(out, detectedToolCall{ID: callID("bash", string(cmdBytes), len(out)), Type: "function", Name: "bash", Arguments: cmdBytes})
				continue
			}
			continue
		}
		if !allowed[name] || !toolChoiceAllows(choice, name) {
			continue
		}
		if v == nil {
			continue
		}
		b, _ := json.Marshal(v)
		out = append(out, detectedToolCall{ID: callID(name, string(b), len(out)), Type: toolType(name, tools), Name: name, Arguments: b})
	}
	// Also check for plain JSON objects with a "command" field (not in fenced blocks)
	if len(out) == 0 {
		for i := 0; i < len(text); i++ {
			if text[i] != '{' {
				continue
			}
			end := strings.Index(text[i:], "\n")
			if end < 0 {
				end = len(text) - i
			}
			line := text[i : i+end]
			braceEnd := strings.LastIndex(line, "}")
			if braceEnd < 0 {
				continue
			}
			if !strings.Contains(line[:braceEnd+1], `"command"`) {
				continue
			}
			var obj map[string]any
			if json.Unmarshal([]byte(line[:braceEnd+1]), &obj) != nil {
				continue
			}
			if cmd, hasCmd := obj["command"]; hasCmd && cmd != "" {
				cmdBytes, _ := json.Marshal(map[string]any{"command": cmd, "timeout": obj["timeout"], "workdir": obj["workdir"]})
				out = append(out, detectedToolCall{ID: callID("bash", string(cmdBytes), len(out)), Type: "function", Name: "bash", Arguments: cmdBytes})
				break
			}
		}
	}
	return out
}
