package web

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"

	"m365-copilot2api/internal/chathub"
)

const (
	maxRouterDescriptionBytes     = 240
	maxExecRouterDescriptionBytes = 2048
	maxExecShellContractBytes     = 1200
	maxRouterPromptBytes          = 128 << 10
)

var (
	workspaceFilenamePattern       = regexp.MustCompile(`(?i)\b[[:alnum:]_.-]+\.[a-z0-9]{1,12}\b`)
	workspaceToolDescriptorPattern = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:exec(?:ute)?|shell|terminal|command|powershell|bash|files?|filesystem|workspace|apply[_ -]?patch|editor|directory)(?:$|[^a-z0-9])`)
	workspaceEnglishActionPattern  = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:create|write|save|modify|edit|update|delete|remove|move|rename|copy|read|inspect|list|search|find|open|launch|start|execute|run|use|control|send|click|type|interact|browse|test|verify|download|upload|convert)(?:s|d|ing)?(?:$|[^a-z])`)
	workspaceEnglishImperative     = regexp.MustCompile(`(?i)^\s*(?:please\s+)?(?:create|write|save|modify|edit|update|delete|remove|move|rename|copy|read|inspect|list|search|find|open|launch|start|execute|run|use|control|send|click|type|interact|browse|test|verify|download|upload|convert)\b`)
	workspaceEnglishTargetPattern  = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:files?|documents?|director(?:y|ies)|folders?|code|projects?|repositories|repos?|commands?|scripts?|workspace|desktop|downloads?|browsers?|websites?|pages?|apps?|applications?|windows?|messages?|skills?|plugins?|tools?|computer[ -]?use)(?:$|[^a-z])`)
	weightedTokenNoticePattern     = regexp.MustCompile(`(?i)^\s*you have [0-9]+ weighted tokens left\.?\s*$`)
	explicitToolRequirementPattern = regexp.MustCompile(`(?i)(?:必须|务必|一定要|请务必)\s*(?:实际\s*)?(?:调用|使用).{0,64}(?:工具|tool|apply[_ -]?patch|exec(?:ute)?|terminal|shell|终端)`)
	execNestedToolHeadingPattern   = regexp.MustCompile("(?m)^###\\s+`?([A-Za-z0-9_]+)`?\\s*$")
	execNestedToolCallPattern      = regexp.MustCompile(`\btools\.([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
	execDiscoveredToolNamePattern  = regexp.MustCompile(`"name"\s*:\s*"([A-Za-z_$][A-Za-z0-9_$]*)"`)
	execShellArgAliasPattern       = regexp.MustCompile(`(\btools\.shell_command\s*\(\s*\{\s*)(?:"cmd"|'cmd'|cmd|commandLine)(\s*:)`)
	execShellArgShorthandPattern   = regexp.MustCompile(`(\btools\.shell_command\s*\(\s*\{\s*)cmd(\s*[,}])`)
	execApplyPatchShortPattern     = regexp.MustCompile(`(\btools\.apply_patch\s*\(\s*)\{\s*(patch|input)\s*\}(\s*\))`)
	execApplyPatchNamedPattern     = regexp.MustCompile(`(\btools\.apply_patch\s*\(\s*)\{\s*(?:"patch"|'patch'|patch|input)\s*:\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\}(\s*\))`)
	execApplyPatchDoublePattern    = regexp.MustCompile(`(?s)(\btools\.apply_patch\s*\(\s*)\{\s*(?:"patch"|'patch'|patch|input)\s*:\s*("(?:\\.|[^"\\])*")\s*\}(\s*\))`)
	execApplyPatchSinglePattern    = regexp.MustCompile(`(?s)(\btools\.apply_patch\s*\(\s*)\{\s*(?:"patch"|'patch'|patch|input)\s*:\s*('(?:\\.|[^'\\])*')\s*\}(\s*\))`)
	execApplyPatchTemplatePattern  = regexp.MustCompile("(?s)(\\btools\\.apply_patch\\s*\\(\\s*)\\{\\s*(?:\"patch\"|'patch'|patch|input)\\s*:\\s*(`(?:\\\\.|[^`\\\\])*`)\\s*\\}(\\s*\\))")
	skillRootLinePattern           = regexp.MustCompile("(?m)^-\\s+`?(r[0-9]+)`?\\s*=\\s*`?([^`\\r\\n]+)`?\\s*$")
	skillCatalogLinePattern        = regexp.MustCompile(`(?m)^-\s+([A-Za-z0-9][A-Za-z0-9:._-]*):\s+\(file:\s+([^\r\n)]+SKILL\.md)\)\s*$`)
)

const declaredSkillRouteMarker = "DECLARED_SKILL_ROUTE_FOR_CURRENT_REQUEST"

type declaredSkillRoute struct {
	Name string
	Path string
}

func requestedDeclaredSkills(messages []oaiMsg) []declaredSkillRoute {
	userText := ""
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			candidate := contentToString(messages[i].Content)
			if !weightedTokenNoticePattern.MatchString(candidate) {
				userText = candidate
				break
			}
		}
	}
	userPhrase := normalizeSkillPhrase(userText)
	if userPhrase == "" {
		return nil
	}

	var catalog strings.Builder
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role == "system" || role == "developer" {
			catalog.WriteString(contentToString(message.Content))
			catalog.WriteByte('\n')
		}
	}
	text := catalog.String()
	roots := make(map[string]string)
	for _, match := range skillRootLinePattern.FindAllStringSubmatch(text, -1) {
		if len(match) >= 3 {
			roots[strings.TrimSpace(match[1])] = strings.TrimRight(strings.TrimSpace(match[2]), "/\\")
		}
	}

	routes := make([]declaredSkillRoute, 0, 2)
	seen := make(map[string]struct{})
	for _, match := range skillCatalogLinePattern.FindAllStringSubmatch(text, -1) {
		if len(match) < 3 || !skillNameMentioned(userPhrase, match[1]) {
			continue
		}
		skillPath := strings.TrimSpace(match[2])
		slashPath := strings.ReplaceAll(skillPath, "\\", "/")
		if split := strings.IndexByte(slashPath, '/'); split > 0 {
			if root, ok := roots[slashPath[:split]]; ok {
				slashPath = strings.TrimRight(strings.ReplaceAll(root, "\\", "/"), "/") + "/" + slashPath[split+1:]
			}
		}
		slashPath = path.Clean(slashPath)
		key := strings.ToLower(match[1] + "\x00" + slashPath)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		routes = append(routes, declaredSkillRoute{Name: match[1], Path: slashPath})
		if len(routes) == 4 {
			break
		}
	}
	return routes
}

func normalizeSkillPhrase(value string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func skillNameMentioned(userPhrase, catalogName string) bool {
	for _, part := range strings.Split(catalogName, ":") {
		candidate := normalizeSkillPhrase(part)
		if len(candidate) >= 4 && strings.Contains(userPhrase, candidate) {
			return true
		}
	}
	return false
}

func declaredSkillRoutingGuard(routes []declaredSkillRoute) string {
	if len(routes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(declaredSkillRouteMarker)
	b.WriteString(": the caller explicitly named these catalog skills. Use one exact path below; aliases have already been expanded. Do not guess or shorten the path.\n")
	for _, route := range routes {
		fmt.Fprintf(&b, "- skill=%q exact_skill_md=%q\n", route.Name, route.Path)
	}
	b.WriteString("If the matching SKILL.md has not been returned yet, the next exec call must read the complete exact_skill_md file through the caller-declared shell. Do not inspect MCP, ALL_TOOLS, or the workspace first.")
	return b.String()
}

func callUsesWrongDeclaredSkillPath(call detectedToolCall, routes []declaredSkillRoute) bool {
	if !strings.EqualFold(call.Name, "exec") || len(routes) == 0 {
		return false
	}
	var arguments map[string]any
	if json.Unmarshal(call.Arguments, &arguments) != nil {
		return false
	}
	input := strings.ReplaceAll(strings.ToLower(fmt.Sprint(arguments["input"])), "\\", "/")
	if !strings.Contains(input, "skill.md") {
		return false
	}
	for _, route := range routes {
		if strings.Contains(input, strings.ToLower(strings.ReplaceAll(route.Path, "\\", "/"))) {
			return false
		}
	}
	return true
}

func callOmitsNodeReplDocumentationOutput(call detectedToolCall) bool {
	if !strings.EqualFold(call.Name, "exec") {
		return false
	}
	var arguments map[string]any
	if json.Unmarshal(call.Arguments, &arguments) != nil {
		return false
	}
	input := strings.ToLower(fmt.Sprint(arguments["input"]))
	if !strings.Contains(input, "mcp__node_repl__js") || !strings.Contains(input, "sky.documentation") {
		return false
	}
	return !strings.Contains(input, "noderepl.write") && !strings.Contains(input, "noderepl.emitimage")
}

func modelToolRouterPrompt(prompt string, tools []map[string]any, choice any) string {
	return modelToolRouterPromptWithIntent(prompt, tools, choice, false)
}

// modelToolRouterPromptWithIntent gives the planner a positive execution
// signal without falsely changing the caller's auto choice into required.
// This matters for ordinary file/search/browser tasks: the model should call
// a compatible local tool, but an imperfect first routing response must not
// trigger account failover or make the request fail as a 502.
func modelToolRouterPromptWithIntent(prompt string, tools []map[string]any, choice any, executionRequested bool) string {
	defs, _ := json.Marshal(compactRouterTools(tools))
	mode := normalizedToolChoiceMode(choice)
	rules := `- If a tool is needed, respond with: CALL_TOOL: tool_name({"arg1":"value1"})
- If no tool is needed, respond with: NO_TOOL_NEEDED
- Only use tools from the available list above
- Validate all arguments against the tool's schema
- Request instructions and system/developer blocks are authoritative; follow them before the user request when they differ
- Preserve exact tool names, argument values, paths, commands, literals, quoting, and separators supplied by those instructions
- Never replace an explicitly supplied argument with an equivalent value, probe, default, or inferred value
- Do not invent tools that are not in the list`
	if toolChoiceRequiresCall(choice) {
		rules += `
- MODE requires a tool call. You must select at least one available tool; never respond with NO_TOOL_NEEDED`
	}
	if executionRequested && !toolChoiceRequiresCall(choice) {
		rules += `
- The current user requested a real local or external action and it is still unfinished, or the latest tool attempt failed. Advance it with at least one compatible declared top-level tool now; do not return NO_TOOL_NEEDED while exec or another action-capable tool is available.
- The top-level exec tool is the caller's local orchestration bridge. Use it for its listed nested tools; it is not a remote shell.
- Inside exec, use the exact nested name from the available catalog. If a runtime example says exec_command but shell_command is declared, call shell_command.
- Skills are instruction bundles, not callable tool names. If the request names a skill or plugin, resolve its root alias from the supplied skill catalog and make the next exec call read that exact complete SKILL.md. The catalog is already the authoritative path map: do not query MCP resources, ALL_TOOLS, or the workspace to locate a listed skill. Read any required referenced resources, then follow the skill's runtime instructions. Invoke the resulting host tools through exec; never invent tools.<skill-name> or replace the skill with a directory listing.
- Only after reading a skill, when it requires a deferred nested tool, inspect the exec runtime's ALL_TOOLS array of {name, description} entries to locate the exact declared name and contract before calling it. Search with ALL_TOOLS.filter(tool => ...tool.name...).
- Reading SKILL.md, querying documentation, listing ALL_TOOLS, and inspecting the workspace are preparation only. They do not complete a requested file, browser, desktop, send, or interaction task. On the following turn, invoke the discovered runtime and perform the requested action.
- For mcp__node_repl__js, its code must emit textual return values with nodeRepl.write(value) or images with await nodeRepl.emitImage(value). A bare final JavaScript expression is not returned by this runtime. In particular, write the results of sky.documentation before continuing.
- Runtime capabilities are caller-specific. Computer Use is typically available in Codex Desktop but absent from CLI environments. Use Browser or Computer Use only when the current skill catalog or ALL_TOOLS actually declares it; otherwise use only the CLI tools that are declared and never invent a desktop runtime.
- For browser or desktop-UI work with a declared Browser or Computer Use runtime, follow that runtime. Never substitute codex_app__navigate_to_codex_page, which only navigates among Codex tasks, and never treat a workspace listing as UI execution.
- If the latest tool result failed, correct the arguments, syntax, or tool selection and retry with a changed strategy. Do not stop after the first recoverable error.
- If no declared tool can advance the action, do not invent a tool or claim the action happened`
	}
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

// compactRouterTools keeps the argument structure needed for selection while
// dropping documentation-only schema fields. The full declarations are still
// used by validateDetectedToolCalls before anything reaches the client.
func compactRouterTools(tools []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		if function == nil {
			out = append(out, tool)
			continue
		}
		compact := map[string]any{}
		for _, key := range []string{"name", "strict"} {
			if value, ok := function[key]; ok {
				compact[key] = value
			}
		}
		if description, _ := function["description"].(string); description != "" {
			if name, _ := function["name"].(string); tool["type"] == "custom" && name == "exec" {
				compact["description"] = compactExecRouterDescription(description)
			} else {
				compact["description"] = compactToolResult(description, maxRouterDescriptionBytes)
			}
		}
		if parameters, ok := function["parameters"]; ok {
			compact["parameters"] = compactRouterSchema(parameters, 0)
		}
		entry := map[string]any{"function": compact}
		if typ, ok := tool["type"]; ok {
			entry["type"] = typ
		}
		out = append(out, entry)
	}
	return out
}

func execNestedToolNames(description string) []string {
	matches := execNestedToolHeadingPattern.FindAllStringSubmatch(description, -1)
	names := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 || match[1] == "" {
			continue
		}
		if _, ok := seen[match[1]]; ok {
			continue
		}
		seen[match[1]] = struct{}{}
		names = append(names, match[1])
	}
	return names
}

func execNestedToolSection(description, target string) string {
	matches := execNestedToolHeadingPattern.FindAllStringSubmatchIndex(description, -1)
	for i, match := range matches {
		if len(match) < 4 || !strings.EqualFold(description[match[2]:match[3]], target) {
			continue
		}
		end := len(description)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		return strings.TrimSpace(description[match[1]:end])
	}
	return ""
}

func compactExecRouterDescription(description string) string {
	names := execNestedToolNames(description)
	if len(names) == 0 {
		return compactToolResult(description, maxRouterDescriptionBytes)
	}
	available := make(map[string]bool, len(names))
	for _, name := range names {
		available[name] = true
	}
	var summary strings.Builder
	summary.WriteString("Caller-local exec orchestration bridge. Run async-module JavaScript; call only listed await tools.<name>(...) functions and emit results with text(result). Use exact catalog names; map stale exec_command examples to declared shell_command. Skills are instructions, not tools: resolve the catalog alias and read the complete SKILL.md through shell_command; do not query MCP, ALL_TOOLS, or the workspace to locate a listed skill. After reading it, inspect the ALL_TOOLS array of {name, description} only for deferred runtime tools and then invoke them. SKILL.md, documentation, ALL_TOOLS, and workspace metadata are preparation, not completion of a file, browser, desktop, send, or interaction task. mcp__node_repl__js must emit text with nodeRepl.write(value) or images with await nodeRepl.emitImage(value); explicitly write sky.documentation results. Computer Use is typically present in Codex Desktop but absent from CLI; use it only when declared. Never substitute a workspace listing or codex_app__navigate_to_codex_page for UI work. Correct and retry recoverable tool failures with a changed strategy. Available nested tools: ")
	summary.WriteString(strings.Join(names, ", "))
	summary.WriteString(".")
	if available["apply_patch"] {
		summary.WriteString(" For workspace edits, pass the complete patch text to tools.apply_patch.")
	}
	if available["shell_command"] {
		summary.WriteString(" Call tools.shell_command({command: COMMAND}); construct COMMAND exactly from the authoritative caller-provided shell_command contract. Preserve its shell, operating-system, path, quoting, and command-separator rules. A command error does not prove that the caller environment changed. ")
		contract := execNestedToolSection(description, "shell_command")
		if contract == "" {
			summary.WriteString("The caller shell environment is unspecified; do not assume one or invent shell-specific commands.")
		} else {
			summary.WriteString("Caller shell_command contract:\n")
			summary.WriteString(compactToolResult(contract, maxExecShellContractBytes))
		}
	}
	return compactToolResult(summary.String(), maxExecRouterDescriptionBytes)
}

func execInputReferencesUnavailableTool(input, description string) bool {
	return execInputReferencesUnavailableToolWithDeferred(input, description, nil)
}

func execInputReferencesUnavailableToolWithDeferred(input, description string, deferred map[string]bool) bool {
	names := execNestedToolNames(description)
	if len(names) == 0 && len(deferred) == 0 {
		return false
	}
	available := make(map[string]struct{}, len(names)+len(deferred))
	for _, name := range names {
		available[name] = struct{}{}
	}
	for name, allowed := range deferred {
		if allowed {
			available[name] = struct{}{}
		}
	}
	for _, match := range execNestedToolCallPattern.FindAllStringSubmatch(input, -1) {
		if len(match) < 2 {
			continue
		}
		if _, ok := available[match[1]]; !ok {
			return true
		}
	}
	return false
}

// Deferred exec tools are absent from the initial compact tool catalog. Trust
// one only after the caller returned that exact name from a preceding
// model-authored ALL_TOOLS discovery call in the same request history.
func discoveredExecNestedToolNames(messages []oaiMsg) map[string]bool {
	discoveryCalls := make(map[string]bool)
	discovered := make(map[string]bool)
	for _, message := range messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "assistant") {
			for _, raw := range message.ToolCalls {
				id, _ := raw["id"].(string)
				function, _ := raw["function"].(map[string]any)
				name, _ := function["name"].(string)
				arguments := fmt.Sprint(function["arguments"])
				if id == "" || !strings.EqualFold(strings.TrimSpace(name), "exec") {
					continue
				}
				var decoded map[string]any
				if json.Unmarshal([]byte(arguments), &decoded) != nil {
					continue
				}
				input, _ := decoded["input"].(string)
				if strings.Contains(input, "ALL_TOOLS") {
					discoveryCalls[id] = true
				}
			}
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(message.Role), "tool") || !discoveryCalls[message.ToolCallID] {
			continue
		}
		result := contentToString(message.Content)
		for _, match := range execDiscoveredToolNamePattern.FindAllStringSubmatch(result, 256) {
			if len(match) >= 2 {
				discovered[match[1]] = true
			}
		}
	}
	return discovered
}

// normalizeExecNestedToolInput repairs narrow compatibility mismatches seen in
// Codex runtimes. Rewrites are only enabled when the destination nested tool is
// present in the caller's exec catalog, and the resulting top-level call is
// still checked by validateDetectedToolCalls.
func normalizeExecNestedToolInput(input, description string) (string, bool) {
	if input == "" || description == "" {
		return input, false
	}
	names := execNestedToolNames(description)
	declared := make(map[string]bool, len(names))
	for _, name := range names {
		declared[strings.ToLower(name)] = true
	}
	aliases := map[string]string{
		"exec_command": "shell_command",
		"execCommand":  "shell_command",
		"shellCommand": "shell_command",
		"applyPatch":   "apply_patch",
		"viewImage":    "view_image",
	}
	changed := false
	for _, name := range names {
		// M365 sometimes drops the tools namespace while preserving the exact
		// declared nested name. Restrict recovery to awaited declared calls so
		// local JavaScript helpers and unknown functions remain untouched.
		bareAwait := regexp.MustCompile(`(\bawait\s+)` + regexp.QuoteMeta(name) + `(\s*\()`)
		if normalized := bareAwait.ReplaceAllString(input, `${1}tools.`+name+`${2}`); normalized != input {
			input = normalized
			changed = true
		}
	}
	for alias, target := range aliases {
		if !declared[strings.ToLower(target)] || !strings.Contains(input, "tools."+alias) {
			continue
		}
		input = strings.ReplaceAll(input, "tools."+alias, "tools."+target)
		changed = true
	}
	if declared["shell_command"] {
		if normalized := execShellArgAliasPattern.ReplaceAllString(input, `${1}command${2}`); normalized != input {
			input = normalized
			changed = true
		}
		if normalized := execShellArgShorthandPattern.ReplaceAllString(input, `${1}command: cmd${2}`); normalized != input {
			input = normalized
			changed = true
		}
	}
	if declared["apply_patch"] {
		if normalized := execApplyPatchShortPattern.ReplaceAllString(input, `${1}${2}${3}`); normalized != input {
			input = normalized
			changed = true
		}
		if normalized := execApplyPatchNamedPattern.ReplaceAllString(input, `${1}${2}${3}`); normalized != input {
			input = normalized
			changed = true
		}
		for _, pattern := range []*regexp.Regexp{execApplyPatchDoublePattern, execApplyPatchSinglePattern, execApplyPatchTemplatePattern} {
			if normalized := unwrapExecApplyPatchLiteral(input, pattern); normalized != input {
				input = normalized
				changed = true
			}
		}
	}
	if normalized, repaired := normalizeExecOutputHelper(input); repaired {
		input = normalized
		changed = true
	}
	if normalized, escaped := escapeInvalidJSLiteralNewlines(input); escaped {
		input = normalized
		changed = true
	}
	return input, changed
}

// The outer exec isolate emits values with text(...). Models occasionally copy
// nodeRepl.write(...) from the nested mcp__node_repl__js contract, or use
// console.log(...). Repair only executable outer code; quoted nested Node REPL
// programs, templates, and comments must remain byte-for-byte unchanged.
func normalizeExecOutputHelper(input string) (string, bool) {
	var out strings.Builder
	out.Grow(len(input))
	changed := false
	var quote byte
	inTemplate := false
	inLineComment := false
	inBlockComment := false

	for i := 0; i < len(input); {
		c := input[i]
		if quote != 0 {
			out.WriteByte(c)
			i++
			if c == '\\' && i < len(input) {
				out.WriteByte(input[i])
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if inTemplate {
			out.WriteByte(c)
			i++
			if c == '\\' && i < len(input) {
				out.WriteByte(input[i])
				i++
			} else if c == '`' {
				inTemplate = false
			}
			continue
		}
		if inLineComment {
			out.WriteByte(c)
			i++
			if c == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			out.WriteByte(c)
			i++
			if c == '*' && i < len(input) && input[i] == '/' {
				out.WriteByte('/')
				i++
				inBlockComment = false
			}
			continue
		}

		if c == '/' && i+1 < len(input) {
			switch input[i+1] {
			case '/':
				out.WriteString("//")
				i += 2
				inLineComment = true
				continue
			case '*':
				out.WriteString("/*")
				i += 2
				inBlockComment = true
				continue
			}
		}
		if c == '\'' || c == '"' {
			quote = c
			out.WriteByte(c)
			i++
			continue
		}
		if c == '`' {
			inTemplate = true
			out.WriteByte(c)
			i++
			continue
		}

		replaced := false
		for _, helper := range []string{"nodeRepl.write", "console.log"} {
			if !strings.HasPrefix(input[i:], helper) || (i > 0 && isJSIdentifierByte(input[i-1])) {
				continue
			}
			end := i + len(helper)
			for end < len(input) && (input[end] == ' ' || input[end] == '\t' || input[end] == '\r' || input[end] == '\n') {
				end++
			}
			if end >= len(input) || input[end] != '(' {
				continue
			}
			out.WriteString("text")
			i += len(helper)
			changed = true
			replaced = true
			break
		}
		if replaced {
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String(), changed
}

func isJSIdentifierByte(c byte) bool {
	return c == '_' || c == '$' || c == '.' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func unwrapExecApplyPatchLiteral(input string, pattern *regexp.Regexp) string {
	if input == "" || pattern == nil {
		return input
	}
	return pattern.ReplaceAllStringFunc(input, func(match string) string {
		parts := pattern.FindStringSubmatch(match)
		if len(parts) != 4 {
			return match
		}
		value := parts[2]
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') {
			body := value[1 : len(value)-1]
			body = strings.ReplaceAll(body, "\r", `\r`)
			body = strings.ReplaceAll(body, "\n", `\n`)
			value = value[:1] + body + value[len(value)-1:]
		}
		return parts[1] + value + parts[3]
	})
}

// JavaScript single- and double-quoted strings cannot contain literal CR/LF.
// Tool-planning JSON occasionally decodes patch "\\n" sequences into real
// newlines before the custom exec input reaches the caller. Escape only those
// invalid in-string newlines; keep statement, comment, and template-literal
// newlines unchanged.
func escapeInvalidJSLiteralNewlines(input string) (string, bool) {
	var out strings.Builder
	out.Grow(len(input))
	var quote byte
	inTemplate := false
	inLineComment := false
	inBlockComment := false
	changed := false
	for i := 0; i < len(input); i++ {
		c := input[i]
		if quote != 0 {
			switch c {
			case '\\':
				out.WriteByte(c)
				if i+1 < len(input) {
					i++
					out.WriteByte(input[i])
				}
			case quote:
				out.WriteByte(c)
				quote = 0
			case '\r':
				out.WriteString(`\r`)
				changed = true
			case '\n':
				out.WriteString(`\n`)
				changed = true
			default:
				out.WriteByte(c)
			}
			continue
		}
		if inTemplate {
			out.WriteByte(c)
			if c == '\\' && i+1 < len(input) {
				i++
				out.WriteByte(input[i])
			} else if c == '`' {
				inTemplate = false
			}
			continue
		}
		if inLineComment {
			out.WriteByte(c)
			if c == '\n' {
				inLineComment = false
			}
			continue
		}
		if inBlockComment {
			out.WriteByte(c)
			if c == '*' && i+1 < len(input) && input[i+1] == '/' {
				i++
				out.WriteByte('/')
				inBlockComment = false
			}
			continue
		}
		if c == '/' && i+1 < len(input) {
			switch input[i+1] {
			case '/':
				out.WriteString("//")
				i++
				inLineComment = true
				continue
			case '*':
				out.WriteString("/*")
				i++
				inBlockComment = true
				continue
			}
		}
		switch c {
		case '\'', '"':
			quote = c
		case '`':
			inTemplate = true
		}
		out.WriteByte(c)
	}
	if !changed {
		return input, false
	}
	return out.String(), true
}

func compactRouterSchema(value any, depth int) any {
	if depth > 16 {
		return map[string]any{"type": "object"}
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any)
		for _, key := range []string{
			"type", "$ref", "format", "pattern", "required", "enum", "const",
			"additionalProperties", "minItems", "maxItems", "minLength", "maxLength",
			"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
		} {
			if item, ok := typed[key]; ok {
				out[key] = item
			}
		}
		if description, _ := typed["description"].(string); description != "" {
			out["description"] = compactToolResult(description, maxRouterDescriptionBytes)
		}
		for _, key := range []string{"properties", "$defs", "definitions"} {
			if children, ok := typed[key].(map[string]any); ok {
				mapped := make(map[string]any, len(children))
				for name, child := range children {
					mapped[name] = compactRouterSchema(child, depth+1)
				}
				out[key] = mapped
			}
		}
		if item, ok := typed["items"]; ok {
			out["items"] = compactRouterSchema(item, depth+1)
		}
		for _, key := range []string{"oneOf", "anyOf", "allOf", "prefixItems"} {
			if variants, ok := typed[key].([]any); ok {
				mapped := make([]any, 0, len(variants))
				for _, variant := range variants {
					mapped = append(mapped, compactRouterSchema(variant, depth+1))
				}
				out[key] = mapped
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, compactRouterSchema(item, depth+1))
		}
		return out
	default:
		return value
	}
}

func explicitToolRequest(messages []oaiMsg) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role == "tool" || role == "assistant" {
			return false
		}
		if role != "user" {
			continue
		}
		rawText := contentToString(messages[i].Content)
		if weightedTokenNoticePattern.MatchString(rawText) {
			continue
		}
		text := strings.ToLower(strings.TrimSpace(rawText))
		if explicitToolRequirementPattern.MatchString(text) {
			return true
		}
		patterns := []string{
			"必须实际调用工具", "必须调用工具", "务必调用工具", "必须使用工具",
			"使用终端工具", "调用终端工具", "实际调用终端", "实际使用终端",
			"must actually call", "must call the tool", "must use the tool",
			"must use a tool", "use the terminal tool", "use a terminal tool",
		}
		for _, pattern := range patterns {
			if strings.Contains(text, pattern) {
				return true
			}
		}
		return false
	}
	return false
}

// workspaceToolRequest catches direct execution requests from clients that
// leave tool_choice on auto. Some Codex clients phrase these as plain tasks
// (for example, "create 1.txt") without explicitly saying "use a tool".
// Only promote the request when the client actually declared a workspace tool.
func workspaceToolRequest(messages []oaiMsg, tools []chathub.Tool) bool {
	if !hasWorkspaceTool(tools) {
		return false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role == "tool" || role == "assistant" {
			return false
		}
		if role == "user" {
			text := contentToString(messages[i].Content)
			if weightedTokenNoticePattern.MatchString(text) {
				continue
			}
			return workspaceActionRequestText(text)
		}
	}
	return false
}

// executionToolRequestPending carries a real action across its tool-result
// continuation without turning a successful action into another forced call.
// This keeps auto recoverable while making one failed local/browser attempt
// eligible for a corrected call on the same account.
func executionToolRequestPending(messages []oaiMsg, tools []chathub.Tool, ledger agentLedger) bool {
	if !hasWorkspaceTool(tools) || !latestUserActionRequest(messages) {
		return false
	}
	if len(ledger.Completed) == 0 {
		return true
	}
	latest := ledger.Completed[len(ledger.Completed)-1]
	return latest.Failed || preparatoryToolEvidence(latest, latestUserRequestText(messages))
}

// A successful skill/documentation/catalog read only prepares an action-capable
// runtime; it is not evidence that the requested file, browser, or desktop
// action happened. Keep routing active so the next turn performs the real
// operation instead of stopping after discovery.
func preparatoryToolEvidence(e toolEvidence, request string) bool {
	if e.Failed || !strings.EqualFold(strings.TrimSpace(e.Name), "exec") || !requestNeedsActionBeyondPreparation(request) {
		return false
	}
	input := strings.ToLower(e.Arguments)
	for _, marker := range []string{
		"skill.md", "all_tools", "sky.documentation", "list_mcp_resources",
		"list_mcp_resource_templates", "get-location", "get-childitem", "test-path",
	} {
		if strings.Contains(input, marker) {
			return true
		}
	}
	return false
}

func latestUserRequestText(messages []oaiMsg) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			continue
		}
		text := contentToString(messages[i].Content)
		if !weightedTokenNoticePattern.MatchString(text) {
			return text
		}
	}
	return ""
}

func requestNeedsActionBeyondPreparation(raw string) bool {
	text := strings.ToLower(raw)
	for _, marker := range []string{
		"创建", "新建", "写入", "修改", "编辑", "更新", "删除", "移动", "重命名", "打开", "启动", "执行", "运行",
		"发送", "点击", "输入", "操作", "交互", "试玩", "部署", "安装", "修复", "转换", "上传", "下载",
		"create", "write", "modify", "edit", "update", "delete", "move", "rename", "open", "launch", "start", "execute", "run",
		"send", "click", "type", "interact", "deploy", "install", "fix", "convert", "upload", "download",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func latestUserActionRequest(messages []oaiMsg) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) != "user" {
			continue
		}
		text := contentToString(messages[i].Content)
		if weightedTokenNoticePattern.MatchString(text) {
			continue
		}
		return workspaceActionRequestText(text)
	}
	return false
}

func hasWorkspaceTool(tools []chathub.Tool) bool {
	for _, tool := range tools {
		var definition struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if json.Unmarshal(tool.Function, &definition) != nil {
			continue
		}
		descriptor := definition.Name + " " + definition.Description
		if workspaceToolDescriptorPattern.MatchString(descriptor) {
			return true
		}
	}
	return false
}

func workspaceActionRequestText(raw string) bool {
	text := strings.ToLower(strings.TrimSpace(raw))
	if text == "" {
		return false
	}

	// Exclude questions, quotations, and explicit negative requests. Keep the
	// exclusions tied to the action so phrases such as "不要只解释，直接创建"
	// remain executable.
	for _, phrase := range []string{
		"不要创建", "不要新建", "不要写入", "不要修改", "不要删除", "不要执行", "不要运行", "不要打开", "不要发送", "不要操作", "不要搜索",
		"别创建", "别新建", "别写入", "别修改", "别删除", "别执行", "别运行", "别打开", "别发送", "别操作", "别搜索",
		"无需创建", "无需写入", "无需修改", "无需执行", "不必创建", "不必写入", "不必执行",
		"do not create", "do not write", "do not edit", "do not modify", "do not delete", "do not run", "do not execute", "do not open", "do not send", "do not search", "do not interact",
		"don't create", "don't write", "don't edit", "don't delete", "don't run", "don't execute", "don't open", "don't send", "don't search", "don't interact",
	} {
		if strings.Contains(text, phrase) {
			return false
		}
	}
	for _, phrase := range []string{
		"如何创建", "怎么创建", "怎样创建", "如何写入", "怎么写入", "如何修改", "怎么修改", "如何执行", "怎么执行", "如何打开", "怎么打开", "如何搜索", "怎么搜索", "如何使用", "怎么使用",
		"是什么意思", "请解释", "解释一下", "举例", "示例", "教程",
		"how to create", "how to write", "how to edit", "how to modify", "how to run", "how to execute", "how to open", "how to search", "how to use",
		"explain how", "what does", "example of", "tutorial",
	} {
		if strings.Contains(text, phrase) {
			return false
		}
	}

	for _, phrase := range []string{
		"直接创建", "立即创建", "马上创建", "现在创建", "倒是创建", "赶紧创建",
		"直接写", "立即写", "马上写", "现在写", "倒是写", "赶紧写",
		"直接执行", "立即执行", "马上执行", "现在执行", "倒是执行", "赶紧执行",
		"do it now", "create it now", "write it now", "run it now", "execute it now",
	} {
		if strings.Contains(text, phrase) {
			return true
		}
	}

	actions := []string{
		"创建", "新建", "写入", "写到", "保存", "修改", "编辑", "更新", "删除", "移除", "移动", "重命名", "复制",
		"读取", "查看", "检查", "列出", "搜索", "查找", "打开", "启动", "执行", "运行", "使用", "调用", "发送", "点击", "输入", "操作", "交互", "试玩", "验证", "整理", "转换", "生成", "下载", "上传",
		"作成", "書き込", "編集", "削除", "読み取", "実行",
		"생성", "작성", "수정", "삭제", "읽기", "실행",
	}
	targets := []string{
		"文件", "文档", "目录", "文件夹", "代码", "项目", "仓库", "命令", "脚本", "当前目录", "工作区", "桌面", "下载",
		"浏览器", "网页", "网站", "页面", "网上", "联网", "互联网", "资料", "信息", "本地服务", "应用", "程序", "窗口", "微信", "消息", "文件传输助手", "技能", "工具", "插件", "computer use", "computer-use", "browser",
		"ファイル", "ディレクトリ", "フォルダ", "コード", "コマンド",
		"파일", "디렉터리", "폴더", "코드", "명령",
	}
	hasAction := workspaceEnglishActionPattern.MatchString(text)
	for _, action := range actions {
		if strings.Contains(text, action) {
			hasAction = true
			break
		}
	}
	if !hasAction {
		return false
	}
	if workspaceFilenamePattern.MatchString(text) {
		return true
	}
	if workspaceEnglishImperative.MatchString(text) {
		return true
	}
	if workspaceEnglishTargetPattern.MatchString(text) {
		return true
	}
	for _, target := range targets {
		if strings.Contains(text, target) {
			return true
		}
	}
	return false
}

func modelToolRepairPrompt(prompt, invalid string, tools []map[string]any, choice any) string {
	defs, _ := json.Marshal(tools)
	return fmt.Sprintf(`Repair the invalid tool-routing output. Return JSON only with shape {"calls":[{"name":"function_name","arguments":{}}]}.
Use only FUNCTION_DEFINITIONS and validate every argument against its schema. Respect TOOL_CHOICE. Use {"calls":[]} only when the application request does not need a tool.

TOOL_CHOICE: %s
APPLICATION_REQUEST_AND_EVIDENCE:
%s

FUNCTION_DEFINITIONS:
%s

INVALID_ROUTER_OUTPUT:
%s`, normalizedToolChoiceMode(choice), prompt, defs, compactToolResult(invalid, 6000))
}

func modelToolExecutionRepairPrompt(prompt, invalid string, tools []map[string]any, choice any) string {
	defs, _ := json.Marshal(compactRouterTools(tools))
	return fmt.Sprintf(`The previous routing answer failed to advance an unfinished real action. Return JSON only with shape {"calls":[{"name":"function_name","arguments":{}}]}.
Select at least one valid declared top-level tool now. The exec tool is the caller-local orchestration bridge for its nested tools. If a listed skill is named and its instructions have not been returned yet, the next call must resolve the catalog alias and read that exact SKILL.md; do not query MCP resources, ALL_TOOLS, or the workspace to locate it. After reading the skill, follow its returned instructions and inspect the ALL_TOOLS array only for deferred runtime tools. Skill reads, documentation, ALL_TOOLS listings, and workspace inspection are preparatory evidence, not completion of the requested action; use the discovered runtime now. If a prior tool failed, change the arguments, syntax, or tool selection. Never use codex_app__navigate_to_codex_page for a URL or desktop application. Do not return an empty calls array.

TOOL_CHOICE: %s
APPLICATION_REQUEST_AND_EVIDENCE:
%s

FUNCTION_DEFINITIONS:
%s

PREVIOUS_ROUTER_OUTPUT:
%s`, normalizedToolChoiceMode(choice), prompt, defs, compactToolResult(invalid, 6000))
}

func toolChoiceRequiresCall(choice any) bool {
	mode := normalizedToolChoiceMode(choice)
	return mode == "required" || strings.HasPrefix(mode, "named:")
}

// effectiveToolChoiceForTurn scopes a generic required choice to the turn that
// requested it. Several clients resend the first-turn choice with the tool
// result; forcing another call at that point prevents the model from producing
// the final answer and can create a duplicate planning loop. A fresh user
// message still starts a new required turn, while named and none choices remain
// explicit caller constraints.
func effectiveToolChoiceForTurn(messages []oaiMsg, choice any) any {
	if normalizedToolChoiceMode(choice) != "required" {
		return choice
	}
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "system", "developer":
			continue
		case "user":
			if weightedTokenNoticePattern.MatchString(contentToString(message.Content)) {
				continue
			}
			return choice
		case "tool":
			return "auto"
		default:
			return choice
		}
	}
	return choice
}

func parseModelToolDecision(text string, tools []map[string]any, choice any) ([]detectedToolCall, bool) {
	text = strings.TrimSpace(text)
	// Try the new natural language format first: CALL_TOOL: name({...})
	if strings.HasPrefix(text, "CALL_TOOL:") || strings.HasPrefix(text, "call_tool:") {
		parts := strings.SplitN(text, ":", 2)
		if len(parts) == 2 {
			rest := strings.TrimSpace(parts[1])
			start := strings.Index(rest, "(")
			end := strings.LastIndex(rest, ")")
			if start > 0 && end > start {
				name := strings.TrimSpace(rest[:start])
				argsStr := rest[start+1 : end]
				var args map[string]any
				if json.Unmarshal([]byte(argsStr), &args) == nil && toolChoiceAllows(choice, name) {
					fn := toolFunction(name, tools)
					if fn != nil && schemaValid(args, fn) == nil {
						b, _ := json.Marshal(args)
						return []detectedToolCall{{ID: callID(name, string(b), 0), Type: toolType(name, tools), Name: name, Arguments: b}}, true
					}
				}
			}
		}
	}
	if strings.Contains(text, "NO_TOOL_NEEDED") || strings.Contains(text, "no_tool_needed") {
		return nil, true
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
