package web

import (
	"encoding/json"
	"fmt"
	"strings"
)

func modelToolRouterPrompt(prompt string, tools []map[string]any, choice any) string {
	defs, _ := json.Marshal(tools)
	mode := normalizedToolChoiceMode(choice)
	rules := `- If a tool is needed, respond with: CALL_TOOL: tool_name({"arg1":"value1"})
- If no tool is needed, respond with: NO_TOOL_NEEDED
- Only use tools from the available list above
- Validate all arguments against the tool's schema
- Do not invent tools that are not in the list
- When the user asks you to implement, modify, enhance, fix, create, or inspect code/files (e.g. "去增强吧", "去干活", "去改代码", "go implement it"), you MUST emit a CALL_TOOL: line to inspect or modify the target files rather than returning NO_TOOL_NEEDED
- The caller's local environment executes the tool; do NOT run any cloud Python/bash sandbox or /mnt/data yourself, and never refuse local paths
- Output ONLY the CALL_TOOL: ... line(s) or NO_TOOL_NEEDED without extra explanation`
	// Multi-turn: completed tool evidence (tool[...], tool_calls:) was already
	// acted upon, so re-invoking those tools would duplicate work.
	if strings.Contains(prompt, "tool_calls:") || strings.Contains(prompt, "tool[call_") {
		rules += `
- Completed evidence must not be repeated: tool_calls/tool[call_x] rows are prior results already delivered to the user, never re-invoke them
- Only start a new tool call when fresh unfinished work remains on the current request`
	}
	return fmt.Sprintf(`You are a tool selection assistant. Based on the user request, decide which tool to call next.

Available tools: %s

MODE: %s

Rules:
%s

User request and evidence:
%s`, defs, mode, rules, prompt)
}

func parseModelToolDecision(text string, tools []map[string]any, choice any) ([]detectedToolCall, bool) {
	text = strings.TrimSpace(text)
	// Try the natural language format first: CALL_TOOL: name({...}) on any line.
	var callToolOut []detectedToolCall
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.Trim(strings.TrimSpace(rawLine), "`*")
		if !strings.HasPrefix(strings.ToUpper(line), "CALL_TOOL:") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		rest := strings.TrimSpace(parts[1])
		start := strings.Index(rest, "(")
		end := strings.LastIndex(rest, ")")
		if start > 0 && end > start {
			name := strings.TrimSpace(rest[:start])
			argsStr := strings.TrimSpace(rest[start+1 : end])
			if argsStr == "" {
				argsStr = "{}"
			}
			var args map[string]any
			if json.Unmarshal([]byte(argsStr), &args) == nil && toolChoiceAllows(choice, name) {
				fn := toolFunction(name, tools)
				if fn != nil && schemaValid(args, fn) == nil {
					b, _ := json.Marshal(args)
					callToolOut = append(callToolOut, detectedToolCall{ID: callID(name, string(b), len(callToolOut)), Type: toolType(name, tools), Name: name, Arguments: b})
				}
			}
		}
	}
	if len(callToolOut) > 0 {
		return callToolOut, true
	}
	if strings.Contains(strings.ToUpper(text), "NO_TOOL_NEEDED") {
		return nil, true
	}
	if fenced := fencedToolCalls(text, tools, choice); len(fenced) > 0 {
		return fenced, true
	}
	// Fallback: try the old JSON format
	if i := strings.Index(text, "```"); i >= 0 {
		text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(text[i+3:], "```"), "json"))
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, false
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal([]byte(text[start:end+1]), &probe) != nil {
		return nil, false
	}
	if _, ok := probe["calls"]; !ok {
		return nil, false
	}
	var envelope struct {
		Calls []struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"calls"`
	}
	if json.Unmarshal([]byte(text[start:end+1]), &envelope) != nil {
		return nil, false
	}
	out := make([]detectedToolCall, 0, len(envelope.Calls))
	for i, c := range envelope.Calls {
		fn := toolFunction(c.Name, tools)
		if fn == nil || c.Arguments == nil || !toolChoiceAllows(choice, c.Name) || schemaValid(c.Arguments, fn) != nil {
			continue
		}
		b, _ := json.Marshal(c.Arguments)
		out = append(out, detectedToolCall{ID: callID(c.Name, string(b), i), Type: toolType(c.Name, tools), Name: c.Name, Arguments: b})
	}
	return out, true
}
