package web

import (
	"encoding/json"
	"regexp"
	"strings"
)

var fencedToolCall = regexp.MustCompile("(?s)```([A-Za-z0-9_-]+)\\s*\\n(.*?)\\n```")

// declaredShell returns the shell-ish or code-execution tool name the client
// actually declared (Bash/bash/sh/shell/powershell/cmd/exec/code...), or "" if none.
func declaredShell(allowed map[string]bool) string {
	candidates := []string{"bash", "Bash", "sh", "shell", "Shell", "powershell", "PowerShell", "cmd", "exec_command", "run_command", "execute_command", "run_shell_command", "exec", "Exec", "code", "Code", "terminal"}
	for _, n := range candidates {
		if allowed[n] {
			return n
		}
	}
	for declared := range allowed {
		low := strings.ToLower(declared)
		for _, n := range candidates {
			if low == strings.ToLower(n) {
				return declared
			}
		}
	}
	return ""
}

var shellLikeInfoStrings = map[string]bool{
	"bash": true, "sh": true, "shell": true, "powershell": true, "pwsh": true,
	"cmd": true, "exec": true, "exec_command": true, "run_command": true,
	"code": true, "python": true, "node": true, "javascript": true, "js": true,
	"typescript": true, "ts": true, "tsx": true, "jsx": true,
}

// isShellLikeInfoString reports whether a fenced-block info string names an
// executor or a runtime language that could act as a command body. Language tags
// (python/typescript/...) are only routed to the executor tool when the caller
// declared one, so they are treated as shell-like here and gated by name later.
func isShellLikeInfoString(lowName string) bool {
	return shellLikeInfoStrings[lowName]
}

// isDeclaredExecutorName reports whether a fence info string is the caller's
// declared executor name itself (bash, code, exec, powershell, ...), as opposed
// to a language tag that merely resembles one. Case variants match; a language
// tag such as "typescript" or "python" never matches.
func isDeclaredExecutorName(name string, allowed map[string]bool) bool {
	if declaredShell(allowed) == "" {
		return false
	}
	if allowed[name] && declaredShell(map[string]bool{name: true}) != "" {
		return true
	}
	for declared := range allowed {
		if strings.EqualFold(declared, name) && declaredShell(map[string]bool{declared: true}) != "" {
			return true
		}
	}
	return false
}

// looksLikeCommandBody reports whether a fenced block body is a real command
// (writes a file, runs a program, resolves a path) rather than a stub such as
// `return 'Create a basic Snake game implementation in the workspace';`.
func looksLikeCommandBody(body string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	if len(body) < 8 {
		return false
	}
	// A JSON object is always a candidate tool envelope.
	if strings.HasPrefix(body, "{") {
		return true
	}
	commandMarkers := []string{
		"cat >", "cat >>", "echo ", "printf ", "mkdir", "touch ",
		"cd ", "ls ", "pwd", "node ", "python", "npm ", "pip ",
		"curl", "wget", "git ", "tee ", "set -", "export ", "install ",
		"writefile", "fs.write", "write_file", "save(", "open(",
		">", "|", "&&",
	}
	for _, marker := range commandMarkers {
		if strings.Contains(body, marker) {
			return true
		}
	}
	// Multi-line code is usually a real body; single-line code rarely is.
	return strings.Contains(body, "\n")
}

// resolveDeclaredToolName maps a model-emitted fence info-string (such as
// "write_file", "write", "read_file", "edit_file", or case variants like
// "write" vs "Write") to the exact tool name declared by the client.
func resolveDeclaredToolName(name string, allowed map[string]bool) (string, bool) {
	if allowed[name] {
		return name, true
	}
	low := strings.ToLower(strings.TrimSpace(name))
	for declared := range allowed {
		if strings.ToLower(declared) == low {
			return declared, true
		}
	}
	aliasGroups := [][]string{
		{"write", "write_file", "create_file", "write_to_file", "save_file", "file_write"},
		{"read", "read_file", "view_file", "cat_file", "file_read"},
		{"edit", "edit_file", "str_replace", "replace_in_file", "file_edit"},
		{"glob", "find_files", "list_files", "list_dir"},
		{"grep", "search_files", "ripgrep"},
		{"skill", "load_skill", "use_skill"},
	}
	for _, group := range aliasGroups {
		inGroup := false
		for _, g := range group {
			if low == g {
				inGroup = true
				break
			}
		}
		if !inGroup {
			continue
		}
		for declared := range allowed {
			dLow := strings.ToLower(declared)
			for _, g := range group {
				if dLow == g {
					return declared, true
				}
			}
		}
	}
	return "", false
}

// scanTopLevelFences extracts outermost ```info\n...\n``` blocks with nesting
// awareness (ported from cramt/m365-copilot-proxy fenced.ts), so a tool block
// whose file/code body itself contains ``` fences is not prematurely cut off.
type scannedFence struct {
	info string
	body string
}

func scanTopLevelFences(text string) []scannedFence {
	var out []scannedFence
	lines := strings.Split(text, "\n")
	depth := 0
	curInfo := ""
	var bodyLines []string
	for _, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			rest := strings.TrimSpace(trimmed[3:])
			if depth == 0 {
				if !strings.Contains(rest, "`") {
					depth = 1
					curInfo = strings.Fields(rest + " _")[0]
					if curInfo == "_" {
						curInfo = ""
					}
					bodyLines = nil
					continue
				}
			} else if rest == "" {
				depth--
				if depth == 0 {
					out = append(out, scannedFence{info: curInfo, body: strings.Join(bodyLines, "\n")})
					curInfo = ""
					bodyLines = nil
					continue
				}
			} else if !strings.Contains(rest, "`") && len(strings.Fields(rest)) == 1 {
				depth++
			}
		}
		if depth > 0 {
			bodyLines = append(bodyLines, line)
		}
	}
	return out
}

// coerceToolCallEnvelope extracts a tool call from a JSON envelope of shape
// {"name": "<tool>", "arguments": {...}} (used by ```tool_call / ```json blocks).
func coerceToolCallEnvelope(obj map[string]any, tools []map[string]any, allowed map[string]bool, choice any, idx int) (detectedToolCall, bool) {
	if obj == nil {
		return detectedToolCall{}, false
	}
	var rawName string
	for _, k := range []string{"name", "tool", "tool_name", "function"} {
		if s, ok := obj[k].(string); ok && strings.TrimSpace(s) != "" {
			rawName = strings.TrimSpace(s)
			break
		}
	}
	if rawName == "" {
		return detectedToolCall{}, false
	}
	shell := declaredShell(allowed)
	resolved, ok := resolveDeclaredToolName(rawName, allowed)
	if !ok {
		low := strings.ToLower(rawName)
		if (low == "bash" || low == "sh" || low == "shell" || low == "powershell" || low == "cmd" || low == "exec" || low == "code") && shell != "" {
			resolved = shell
			ok = true
		}
	}
	if !ok || !toolChoiceAllows(choice, resolved) {
		return detectedToolCall{}, false
	}
	var argsVal any
	for _, k := range []string{"arguments", "parameters", "input", "args"} {
		if v, exists := obj[k]; exists && v != nil {
			argsVal = v
			break
		}
	}
	var argsMap map[string]any
	switch av := argsVal.(type) {
	case map[string]any:
		argsMap = av
	case string:
		if json.Unmarshal([]byte(strings.TrimSpace(av)), &argsMap) != nil {
			b, typ := buildShellToolArgs(resolved, tools, av, nil)
			return detectedToolCall{ID: callID(resolved, string(b), idx), Type: typ, Name: resolved, Arguments: b}, true
		}
	default:
		argsMap = map[string]any{}
		for k, v := range obj {
			if k != "name" && k != "tool" && k != "tool_name" && k != "function" && k != "type" && k != "id" {
				argsMap[k] = v
			}
		}
	}
	if fn := toolFunction(resolved, tools); fn != nil {
		normalizeToolArgsForSchema(argsMap, fn)
	}
	b, _ := json.Marshal(argsMap)
	return detectedToolCall{ID: callID(resolved, string(b), idx), Type: toolType(resolved, tools), Name: resolved, Arguments: b}, true
}

func declaredWriteTool(tools []map[string]any) (string, string, string) {
	for _, t := range tools {
		fn, _ := t["function"].(map[string]any)
		if fn == nil {
			continue
		}
		name, _ := fn["name"].(string)
		low := strings.ToLower(name)
		if low != "write_file" && low != "write" && low != "create_file" && low != "write_to_file" && low != "save_file" {
			continue
		}
		params, _ := fn["parameters"].(map[string]any)
		props, _ := params["properties"].(map[string]any)
		pathKey, contentKey := "", ""
		for _, pk := range []string{"path", "file_path", "filePath", "filepath", "filename", "file", "target_file"} {
			if _, ok := props[pk]; ok {
				pathKey = pk
				break
			}
		}
		for _, ck := range []string{"content", "contents", "text", "body", "code", "data"} {
			if _, ok := props[ck]; ok {
				contentKey = ck
				break
			}
		}
		if pathKey != "" && contentKey != "" {
			return name, pathKey, contentKey
		}
	}
	return "", "", ""
}

var filenameHintPattern = regexp.MustCompile(`(?i)\b([A-Za-z0-9_./\\-]+\.(?:html|htm|js|ts|tsx|jsx|py|css|json|md|txt))\b`)

var headerFileLinePattern = regexp.MustCompile(`^(?:path|file_path|filepath|filename|file):[ \t]*([^\r\n]+)\r?\n\r?\n([\s\S]+)$`)

// buildShellToolArgs constructs the JSON arguments for a shell-like or custom
// execution tool according to the parameter names declared in its schema
// ("input" for Codex custom exec, "cmd" for exec_command, or "command" by default).
func buildShellToolArgs(toolName string, tools []map[string]any, cmd string, extra map[string]any) ([]byte, string) {
	if m := headerFileLinePattern.FindStringSubmatch(strings.TrimSpace(cmd)); len(m) == 3 {
		targetFile := strings.Trim(strings.TrimSpace(m[1]), `"'`)
		fileBody := strings.TrimRight(m[2], "\r\n")
		if targetFile != "" && fileBody != "" && !strings.ContainsAny(targetFile, "\"'`$&|;<>") {
			delim := "EOF_M365_FILE"
			if dir := strings.LastIndexAny(targetFile, "/\\"); dir > 0 {
				dirPath := strings.ReplaceAll(targetFile[:dir], "\\", "/")
				cmd = "mkdir -p \"" + dirPath + "\" && cat > \"" + strings.ReplaceAll(targetFile, "\\", "/") + "\" <<'" + delim + "'\n" + fileBody + "\n" + delim
			} else {
				cmd = "cat > \"" + targetFile + "\" <<'" + delim + "'\n" + fileBody + "\n" + delim
			}
		}
	}
	fn := toolFunction(toolName, tools)
	typ := toolType(toolName, tools)
	key := "command"
	allowExtra := true
	if fn != nil {
		if params, ok := fn["parameters"].(map[string]any); ok {
			if ap, ok := params["additionalProperties"].(bool); ok && !ap {
				allowExtra = false
			}
			if props, ok := params["properties"].(map[string]any); ok {
				for _, cand := range []string{"command", "cmd", "input", "code", "script", "body", "text"} {
					if _, exists := props[cand]; exists {
						key = cand
						break
					}
				}
				if len(props) == 1 {
					for k := range props {
						key = k
					}
				}
			}
		}
	}
	payload := map[string]any{key: cmd}
	if allowExtra && extra != nil && key == "command" {
		if extra["timeout"] != nil {
			payload["timeout"] = extra["timeout"]
		}
		if extra["workdir"] != nil {
			payload["workdir"] = extra["workdir"]
		}
	}
	if fn != nil {
		normalizeToolArgsForSchema(payload, fn)
	}
	b, _ := json.Marshal(payload)
	return b, typ
}

var bodyParamOrder = []string{
	"content", "contents", "command", "cmd", "code", "body", "script", "text", "input", "patch", "query", "data",
}

// matchDeclaredPropKey resolves a header key written by the model (e.g. "path")
// to the property name declared in the tool's schema (e.g. "file_path").
func matchDeclaredPropKey(key string, props map[string]any, bodyParam string) (string, bool) {
	if _, ok := props[key]; ok && key != bodyParam {
		return key, true
	}
	low := strings.ToLower(key)
	for k := range props {
		if k != bodyParam && strings.ToLower(k) == low {
			return k, true
		}
	}
	pathAliases := map[string]bool{"path": true, "file_path": true, "filepath": true, "filename": true, "file": true, "target_file": true}
	if pathAliases[low] {
		for _, cand := range []string{"file_path", "path", "filePath", "filepath", "filename", "file", "target_file"} {
			if _, ok := props[cand]; ok && cand != bodyParam {
				return cand, true
			}
		}
	}
	return "", false
}

// parseHeaderBodyToolArgs parses the escaping-free fenced tool format:
//
//	path: D:\test_projects\m365\snake\snake.html
//
//	<!DOCTYPE html>
//	...
//
// according to the tool's declared JSON schema properties.
func parseHeaderBodyToolArgs(toolName string, tools []map[string]any, raw string) (map[string]any, bool) {
	fn := toolFunction(toolName, tools)
	if fn == nil {
		return nil, false
	}
	params, _ := fn["parameters"].(map[string]any)
	if params == nil {
		return nil, false
	}
	props, _ := params["properties"].(map[string]any)
	if len(props) == 0 {
		return nil, false
	}
	var bodyParam string
	for _, cand := range bodyParamOrder {
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
	raw = strings.TrimPrefix(raw, "\r\n")
	raw = strings.TrimPrefix(raw, "\n")
	lines := strings.Split(raw, "\n")
	out := map[string]any{}
	i := 0
	for ; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if strings.TrimSpace(line) == "" {
			i++
			break
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			break
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		mappedKey, ok := matchDeclaredPropKey(key, props, bodyParam)
		if !ok {
			break
		}
		val = strings.Trim(val, `"'`)
		out[mappedKey] = val
	}
	rest := strings.TrimRight(strings.Join(lines[i:], "\n"), "\r\n")
	if bodyParam != "" && (len(out) > 0 || len(props) == 1) {
		out[bodyParam] = rest
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func fencedToolCalls(text string, tools []map[string]any, choice any) []detectedToolCall {
	allowed := allowedToolNames(tools)
	shell := declaredShell(allowed)
	var out []detectedToolCall
	fences := scanTopLevelFences(text)
	if len(fences) == 0 {
		for _, m := range fencedToolCall.FindAllStringSubmatch(text, -1) {
			fences = append(fences, scannedFence{info: m[1], body: m[2]})
		}
	}
	for _, f := range fences {
		name := f.info
		args := strings.TrimSpace(f.body)
		var v any
		_ = json.Unmarshal([]byte(args), &v)
		lowName := strings.ToLower(name)
		// 1) Universal ```tool_call or ```json envelope: {"name": "<tool>", "arguments": {...}}
		if lowName == "tool_call" || lowName == "json" || lowName == "" {
			if obj, ok := v.(map[string]any); ok {
				if call, ok := coerceToolCallEnvelope(obj, tools, allowed, choice, len(out)); ok {
					out = append(out, call)
					continue
				}
			}
			continue
		}
		// 2) Auto-convert bash/shell/exec/code blocks to the declared shell/code tool.
		if isShellLikeInfoString(lowName) {
			// A language-tagged block (```typescript, ```python, ```javascript, ...)
			// is an ordinary code sample, not a tool call: the model often answers a
			// build request with a stub such as `return 'Create a basic Snake game'`.
			// Converting those runs a no-op command and the harness reports a file
			// that was never created, so only a block that the model wrote as the
			// tool name (exact or unambiguous executor alias), or one carrying a real
			// command body, is converted.
			namedExecutor := allowed[name]
			if !namedExecutor {
				namedExecutor = isDeclaredExecutorName(name, allowed)
			}
			if !namedExecutor && !looksLikeCommandBody(args) {
				continue
			}
			converted := name
			if !namedExecutor {
				if shell == "" {
					continue
				}
				converted = shell
			}
			if obj, ok := v.(map[string]any); ok {
				if call, ok := coerceToolCallEnvelope(obj, tools, allowed, choice, len(out)); ok {
					out = append(out, call)
					continue
				}
				for _, k := range []string{"command", "cmd", "input", "code", "script"} {
					if cmd, ok := obj[k].(string); ok && strings.TrimSpace(cmd) != "" {
						cmdBytes, typ := buildShellToolArgs(converted, tools, cmd, obj)
						out = append(out, detectedToolCall{ID: callID(converted, string(cmdBytes), len(out)), Type: typ, Name: converted, Arguments: cmdBytes})
						break
					}
				}
				continue
			}
			if v == nil && args != "" {
				if hb, ok := parseHeaderBodyToolArgs(converted, tools, args); ok {
					if fn := toolFunction(converted, tools); fn != nil {
						normalizeToolArgsForSchema(hb, fn)
					}
					b, _ := json.Marshal(hb)
					out = append(out, detectedToolCall{ID: callID(converted, string(b), len(out)), Type: toolType(converted, tools), Name: converted, Arguments: b})
					continue
				}
				cmdBytes, typ := buildShellToolArgs(converted, tools, args, nil)
				out = append(out, detectedToolCall{ID: callID(converted, string(cmdBytes), len(out)), Type: typ, Name: converted, Arguments: cmdBytes})
				continue
			}
			continue
		}
		// 3) Tool-named fenced block (exact, case-insensitive, or synonymous match).
		resolvedName, isAllowed := resolveDeclaredToolName(name, allowed)
		if !isAllowed || !toolChoiceAllows(choice, resolvedName) {
			continue
		}
		name = resolvedName
		if v == nil {
			if hb, ok := parseHeaderBodyToolArgs(name, tools, f.body); ok {
				if fn := toolFunction(name, tools); fn != nil {
					normalizeToolArgsForSchema(hb, fn)
				}
				b, _ := json.Marshal(hb)
				out = append(out, detectedToolCall{ID: callID(name, string(b), len(out)), Type: toolType(name, tools), Name: name, Arguments: b})
				continue
			}
			if toolType(name, tools) == "custom" && args != "" {
				b, _ := json.Marshal(map[string]any{"input": args})
				out = append(out, detectedToolCall{ID: callID(name, string(b), len(out)), Type: "custom", Name: name, Arguments: b})
			}
			continue
		}
		if obj, ok := v.(map[string]any); ok {
			if call, ok := coerceToolCallEnvelope(obj, tools, allowed, choice, len(out)); ok {
				out = append(out, call)
				continue
			}
			if fn := toolFunction(name, tools); fn != nil {
				normalizeToolArgsForSchema(obj, fn)
				v = obj
			}
		}
		b, _ := json.Marshal(v)
		out = append(out, detectedToolCall{ID: callID(name, string(b), len(out)), Type: toolType(name, tools), Name: name, Arguments: b})
	}
	// Also check for plain JSON objects with a "command" field (not in fenced blocks)
	if len(out) == 0 && shell != "" {
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
			if cmd, ok := obj["command"].(string); ok && cmd != "" {
				cmdBytes, typ := buildShellToolArgs(shell, tools, cmd, obj)
				out = append(out, detectedToolCall{ID: callID(shell, string(cmdBytes), len(out)), Type: typ, Name: shell, Arguments: cmdBytes})
				break
			}
		}
	}
	return out
}
