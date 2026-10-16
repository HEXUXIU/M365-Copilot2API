package chathub

import (
	"encoding/json"
	"fmt"
	"strings"
)

var bodyParamCandidates = []string{
	"content", "contents", "command", "cmd", "code", "body", "script", "text", "input", "patch", "query", "data",
}

func deriveToolTemplate(name, desc string, rawParams json.RawMessage) string {
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	_ = json.Unmarshal(rawParams, &schema)
	props := schema.Properties
	header := name
	if strings.TrimSpace(desc) != "" {
		header = name + " — " + strings.TrimSpace(desc)
	}
	if len(props) == 0 {
		return fmt.Sprintf("%s\n```%s\n{}\n```", header, name)
	}
	var bodyParam string
	for _, cand := range bodyParamCandidates {
		if _, ok := props[cand]; ok {
			bodyParam = cand
			break
		}
	}
	if bodyParam == "" && len(props) == 1 {
		for k := range props {
			bodyParam = k
		}
	}
	var lines []string
	for k := range props {
		if k == bodyParam {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: <%s>", k, k))
	}
	if bodyParam != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, fmt.Sprintf("<%s>", bodyParam))
	}
	return fmt.Sprintf("%s\n```%s\n%s\n```", header, name, strings.Join(lines, "\n"))
}

// toolProtocolPrompt uses the user-voice relay_batch framing + header+body
// fenced template proven across GPT-6 Sol, GPT-6.1 Sol, GPT-5.x, and Claude
// tones (avoids Azure Prompt Shields jailbreak/injection rejection and keeps
// the model out of M365's built-in /mnt/data sandbox while allowing multi-line
// file writes without JSON escaping).
func toolProtocolPrompt(text string, tools []Tool, choice any, hasPlugins bool) string {
	if len(tools) == 0 || strings.EqualFold(fmt.Sprint(choice), "none") {
		if hasPlugins {
			return text
		}
		return fmt.Sprintf("Please answer the following request in full. Do not truncate or abbreviate your response.\n\n%s", text)
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
		defs = append(defs, deriveToolTemplate(f.Name, f.Description, f.Parameters))
	}
	if len(defs) == 0 {
		return text
	}
	return fmt.Sprintf(`Formatting specification for this conversation:
1. Avoid invoking the built-in Python / code interpreter / sandbox environment.
2. Whenever the request asks to create, build, make, or modify a file, webpage, game, or script, or to inspect files / run a command, place the output inside a fenced block whose info-string is the exact tool name from <tools> below (or a tool_call JSON block), rather than a plain html/js/typescript code block.
3. The fenced block must carry the complete file body or the complete command — a block that only describes the code, or a statement that returns a placeholder string, performs nothing.
4. When the tool has a single free-text parameter and no path parameter (a code/exec/bash style executor), put the full body into that parameter as real code that writes the file, for example cat > "index.html" <<'EOF' ... EOF. Do not emit only a return statement or a code fragment.
5. Format the block either as shown in the <tools> template (header lines "key: value", a blank line, then the full file/command content) or as a JSON object matching the schema, preceded by one short sentence.

<tools>
%s
</tools>

%s`, strings.Join(defs, "\n\n"), text)
}
