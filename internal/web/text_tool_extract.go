package web

// extractTextToolCalls recovers tool calls that M365 emits as free-form text
// (XML / inline JSON / natural language) instead of structured ChatHub tool
// events. The extraction layer only discovers call intent; the existing
// validateDetectedToolCalls boundary still enforces the declared-name/schema
// trust rules before anything reaches the client.

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"
)

type textToolCall struct {
	ID        string
	Type      string
	Name      string
	Arguments string
}

var (
	textToolCallFencePattern  = regexp.MustCompile("(?s)```(?:json|xml)?\\s*(.*?)\\s*```")
	textFunctionCallPattern   = regexp.MustCompile(`(?s)调用函数\s*[：:]\s*([\w\-\.]+)\s*(?:参数|arguments)[：:]\s*(\{.*?\})`)
	textFunctionInvokePattern = regexp.MustCompile(`(?s)\b([\w\-\.]+)\s*\(\s*(\{.*?\})\s*\)`)
	textXMLBlockPattern       = regexp.MustCompile(`(?is)<tool_calls>\s*(.*?)\s*</tool_calls>`)
	textXMLItemPattern        = regexp.MustCompile(`(?is)<tool_call(?:\s+id="([^"]+)")?>(.*?)</tool_call>`)
	textXMLNamePattern        = regexp.MustCompile(`(?is)<name>\s*([^<]+?)\s*</name>`)
	textXMLArgsPattern        = regexp.MustCompile(`(?is)<arguments>\s*(.*?)\s*</arguments>`)
)

// extractTextToolCalls is the combined entry point: XML payload, fenced code
// blocks, inline JSON tool_calls, named function objects, natural-language and
// function-invoke forms. Returns true when at least one candidate was recovered.
func extractTextToolCalls(text string, tools []map[string]any, choice any) ([]detectedToolCall, bool) {
	if text == "" {
		return nil, false
	}
	calls := extractAllTextToolCalls(text)
	if len(calls) == 0 {
		return nil, false
	}
	out := make([]detectedToolCall, 0, len(calls))
	for i, c := range calls {
		if c.Name == "" {
			continue
		}
		if !textToolAllowed(c.Name, tools, choice) {
			continue
		}
		if c.ID == "" {
			c.ID = callID(c.Name, c.Arguments, i)
		}
		typ := c.Type
		if typ == "" {
			typ = toolType(c.Name, tools)
		}
		args := normalizeArguments(c.Arguments)
		out = append(out, detectedToolCall{ID: c.ID, Type: typ, Name: c.Name, Arguments: json.RawMessage(args)})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func extractAllTextToolCalls(text string) []textToolCall {
	if c := parseTextXML(text); len(c) > 0 {
		return c
	}
	for _, m := range textToolCallFencePattern.FindAllStringSubmatch(text, -1) {
		if len(m) <= 1 {
			continue
		}
		payload := strings.TrimSpace(m[1])
		if c := parseTextXML(payload); len(c) > 0 {
			return c
		}
		if c := parseTextToolCallsJSON(payload); len(c) > 0 {
			return c
		}
		if c := parseTextNamedFunctionObject(payload); len(c) > 0 {
			return c
		}
	}
	if c := extractInlineTextToolCalls(text); len(c) > 0 {
		return c
	}
	if c := extractSingleTextFunctionCall(text); len(c) > 0 {
		return c
	}
	for _, m := range textFunctionInvokePattern.FindAllStringSubmatch(text, -1) {
		if len(m) < 3 {
			continue
		}
		name := strings.TrimSpace(m[1])
		args := strings.TrimSpace(m[2])
		if name != "" && json.Valid([]byte(args)) {
			return []textToolCall{{Type: "function", Name: name, Arguments: args}}
		}
	}
	if m := textFunctionCallPattern.FindStringSubmatch(text); len(m) > 2 {
		name := strings.TrimSpace(m[1])
		args := strings.TrimSpace(m[2])
		if json.Valid([]byte(args)) {
			return []textToolCall{{Type: "function", Name: name, Arguments: args}}
		}
	}
	return nil
}

func textToolAllowed(name string, tools []map[string]any, choice any) bool {
	if len(tools) == 0 {
		return true
	}
	if !allowedToolNames(tools)[name] {
		return false
	}
	return toolChoiceAllows(choice, name)
}

func parseTextXML(text string) []textToolCall {
	blocks := textXMLBlockPattern.FindAllStringSubmatch(text, -1)
	for _, block := range blocks {
		if len(block) < 2 {
			continue
		}
		if c := textXMLItems(block[1]); len(c) > 0 {
			return c
		}
	}
	return textXMLItems(text)
}

func textXMLItems(body string) []textToolCall {
	items := textXMLItemPattern.FindAllStringSubmatch(body, -1)
	if len(items) == 0 {
		return nil
	}
	out := make([]textToolCall, 0, len(items))
	for _, item := range items {
		if len(item) < 3 {
			continue
		}
		nameMatch := textXMLNamePattern.FindStringSubmatch(item[2])
		if len(nameMatch) < 2 {
			continue
		}
		args := "{}"
		if argsMatch := textXMLArgsPattern.FindStringSubmatch(item[2]); len(argsMatch) >= 2 {
			args = strings.TrimSpace(argsMatch[1])
			if strings.HasPrefix(args, "<![CDATA[") && strings.HasSuffix(args, "]]>") {
				args = args[len("<![CDATA["):]
				args = args[:len(args)-len("]]>")]
			}
			args = strings.TrimSpace(html.UnescapeString(args))
			if args == "" {
				args = "{}"
			}
		}
		out = append(out, textToolCall{
			ID:        strings.TrimSpace(item[1]),
			Type:      "function",
			Name:      strings.TrimSpace(html.UnescapeString(nameMatch[1])),
			Arguments: args,
		})
	}
	return out
}

func parseTextToolCallsJSON(jsonStr string) []textToolCall {
	var data struct {
		ToolCalls []struct {
			ID        string      `json:"id"`
			Type      string      `json:"type"`
			Name      string      `json:"name"`
			Arguments interface{} `json:"arguments"`
			Function  interface{} `json:"function"`
		} `json:"tool_calls"`
	}
	if json.Unmarshal([]byte(jsonStr), &data) != nil || len(data.ToolCalls) == 0 {
		return nil
	}
	out := make([]textToolCall, 0, len(data.ToolCalls))
	for _, tc := range data.ToolCalls {
		call := textToolCall{ID: tc.ID, Type: tc.Type}
		if fn, ok := tc.Function.(map[string]any); ok {
			call.Name, _ = fn["name"].(string)
			if args, ok := fn["arguments"]; ok {
				call.Arguments = normalizeArguments(fmtArguments(args))
			}
		}
		if call.Name == "" {
			call.Name = tc.Name
		}
		if call.Arguments == "" {
			if tc.Arguments != nil {
				call.Arguments = normalizeArguments(fmtArguments(tc.Arguments))
			} else {
				call.Arguments = "{}"
			}
		}
		out = append(out, call)
	}
	return out
}

func parseTextNamedFunctionObject(jsonStr string) []textToolCall {
	var raw struct {
		ID        string      `json:"id"`
		Type      string      `json:"type"`
		Name      string      `json:"name"`
		Arguments interface{} `json:"arguments"`
		Tool      string      `json:"tool"`
		Args      interface{} `json:"args"`
		Input     interface{} `json:"input"`
		Function  *struct {
			Name      string      `json:"name"`
			Arguments interface{} `json:"arguments"`
		} `json:"function"`
	}
	if json.Unmarshal([]byte(jsonStr), &raw) != nil {
		return nil
	}
	name := raw.Name
	args := raw.Arguments
	if raw.Function != nil {
		if name == "" {
			name = raw.Function.Name
		}
		if args == nil {
			args = raw.Function.Arguments
		}
	}
	if name == "" && raw.Tool != "" {
		name = raw.Tool
	}
	if args == nil && raw.Args != nil {
		args = raw.Args
	}
	if args == nil && raw.Input != nil {
		args = raw.Input
	}
	if name == "" {
		return nil
	}
	return []textToolCall{{
		ID:        raw.ID,
		Type:      raw.Type,
		Name:      name,
		Arguments: normalizeArguments(fmtArguments(args)),
	}}
}

func extractInlineTextToolCalls(text string) []textToolCall {
	if !strings.Contains(text, `"tool_calls"`) {
		return nil
	}
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		end := findMatchingBrace(text, i)
		if end == -1 {
			continue
		}
		jsonStr := text[i:end]
		if strings.Contains(jsonStr, `"tool_calls"`) {
			if c := parseTextToolCallsJSON(jsonStr); len(c) > 0 {
				return c
			}
		}
		i = end - 1
	}
	return nil
}

func extractSingleTextFunctionCall(text string) []textToolCall {
	searchStart := 0
	for {
		idx := strings.Index(text[searchStart:], `"name"`)
		if idx == -1 {
			break
		}
		idx += searchStart
		braceStart := -1
		for k := idx - 1; k >= 0; k-- {
			ch := text[k]
			if ch == '{' {
				braceStart = k
				break
			}
			if ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' {
				break
			}
		}
		if braceStart == -1 {
			searchStart = idx + 1
			continue
		}
		end := findMatchingBrace(text, braceStart)
		if end == -1 {
			searchStart = idx + 1
			continue
		}
		if c := parseTextNamedFunctionObject(text[braceStart:end]); len(c) > 0 {
			return c
		}
		searchStart = idx + 1
	}
	return nil
}

func findMatchingBrace(text string, start int) int {
	if start >= len(text) || text[start] != '{' {
		return -1
	}
	braceCount := 1
	inString := false
	escapeNext := false
	j := start + 1
	for j < len(text) && braceCount > 0 {
		ch := text[j]
		if escapeNext {
			escapeNext = false
			j++
			continue
		}
		switch ch {
		case '\\':
			if inString {
				escapeNext = true
			}
		case '"':
			inString = !inString
		case '{':
			if !inString {
				braceCount++
			}
		case '}':
			if !inString {
				braceCount--
			}
		}
		j++
	}
	if braceCount != 0 {
		return -1
	}
	return j
}

func fmtArguments(args any) string {
	switch v := args.(type) {
	case string:
		return v
	case nil:
		return "{}"
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	}
	return "{}"
}

func normalizeArguments(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return "{}"
	}
	if json.Valid([]byte(args)) {
		return args
	}
	fixed := strings.ReplaceAll(args, "'", "\"")
	if json.Valid([]byte(fixed)) {
		return fixed
	}
	return args
}

// removeTextToolContent strips tool-call payloads from assistant text so they
// are not leaked to the client as ordinary content.
func removeTextToolContent(text string) string {
	result := textXMLBlockPattern.ReplaceAllString(text, "")
	result = textXMLItemPattern.ReplaceAllString(result, "")
	result = textToolCallFencePattern.ReplaceAllStringFunc(result, func(match string) string {
		submatch := textToolCallFencePattern.FindStringSubmatch(match)
		if len(submatch) > 1 {
			payload := strings.TrimSpace(submatch[1])
			if len(parseTextXML(payload)) > 0 || len(parseTextToolCallsJSON(payload)) > 0 || len(parseTextNamedFunctionObject(payload)) > 0 {
				return ""
			}
		}
		return match
	})
	result = removeInlineTextToolCallJSON(result)
	result = removeInlineTextSingleFunctionCallJSON(result)
	return strings.TrimSpace(result)
}

func removeInlineTextToolCallJSON(text string) string {
	if !strings.Contains(text, `"tool_calls"`) {
		return text
	}
	var result strings.Builder
	result.Grow(len(text))
	i := 0
	for i < len(text) {
		if text[i] != '{' {
			result.WriteByte(text[i])
			i++
			continue
		}
		end := findMatchingBrace(text, i)
		if end == -1 {
			result.WriteByte(text[i])
			i++
			continue
		}
		jsonStr := text[i:end]
		if strings.Contains(jsonStr, `"tool_calls"`) {
			var data map[string]any
			if json.Unmarshal([]byte(jsonStr), &data) == nil {
				if _, ok := data["tool_calls"]; ok {
					i = end
					continue
				}
			}
		}
		result.WriteByte(text[i])
		i++
	}
	return result.String()
}

func removeInlineTextSingleFunctionCallJSON(text string) string {
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		end := findMatchingBrace(text, i)
		if end == -1 {
			continue
		}
		if len(parseTextNamedFunctionObject(text[i:end])) > 0 {
			return strings.TrimSpace(text[:i] + text[end:])
		}
		i = end - 1
	}
	return text
}
