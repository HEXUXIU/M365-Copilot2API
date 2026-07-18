package chathub

import (
	"encoding/json"
	"fmt"
	"strings"
)

// toolProtocolPrompt follows the community-compatible M365 convention:
// definitions are wrapped in <tools>, and calls are emitted as a fenced block
// whose info string is the exact tool name.
func toolProtocolPrompt(text string, tools []Tool, choice any) string {
	if len(tools) == 0 || strings.EqualFold(fmt.Sprint(choice), "none") {
		return text
	}
	var defs []string
	for _, t := range tools {
		var f struct {
			Name, Description string
			Parameters        json.RawMessage `json:"parameters"`
		}
		if json.Unmarshal(t.Function, &f) != nil || f.Name == "" {
			continue
		}
		params := strings.TrimSpace(string(f.Parameters))
		if params == "" || params == "null" {
			params = "{}"
		}
		defs = append(defs, fmt.Sprintf("%s — %s\n```%s\n%s\n```", f.Name, f.Description, f.Name, params))
	}
	if len(defs) == 0 {
		return text
	}
	return fmt.Sprintf(`You are an execution agent. The tools below are real tools exposed by the caller, not hypothetical M365 plugins.
When the user's request requires a tool, call it by emitting ONLY one fenced block whose info string is the exact tool name and whose body is a JSON object of arguments. Do not say that the tool is unavailable. Do not wrap the call in XML or Markdown prose. Wait for the tool result before claiming completion.

<tools>
%s
</tools>

User request:
%s`, strings.Join(defs, "\n\n"), text)
}
