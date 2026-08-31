package web

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

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
	workspaceEnglishActionPattern  = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:create|write|save|modify|edit|update|delete|remove|move|rename|copy|read|inspect|list|search|find|open|execute|run)(?:s|d|ing)?(?:$|[^a-z])`)
	workspaceEnglishTargetPattern  = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:files?|director(?:y|ies)|folders?|code|projects?|repositories|repos?|commands?|scripts?|workspace)(?:$|[^a-z])`)
	weightedTokenNoticePattern     = regexp.MustCompile(`(?i)^\s*you have [0-9]+ weighted tokens left\.?\s*$`)
	execNestedToolHeadingPattern   = regexp.MustCompile("(?m)^###\\s+`?([A-Za-z0-9_]+)`?\\s*$")
	execNestedToolCallPattern      = regexp.MustCompile(`\btools\.([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`)
)

func modelToolRouterPrompt(prompt string, tools []map[string]any, choice any) string {
	defs, _ := json.Marshal(compactRouterTools(tools))
	mode := normalizedToolChoiceMode(choice)
	rules := `- If a tool is needed, respond with: CALL_TOOL: tool_name({"arg1":"value1"})
- If no tool is needed, respond with: NO_TOOL_NEEDED
- Only use tools from the available list above
- Validate all arguments against the tool's schema
- Do not invent tools that are not in the list`
	if toolChoiceRequiresCall(choice) {
		rules += `
- MODE requires a tool call. You must select at least one available tool; never respond with NO_TOOL_NEEDED`
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
	summary.WriteString("Run raw JavaScript in an async module. Call only listed nested tools as await tools.<name>(...). Emit returned results with text(result). Available nested tools: ")
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
	names := execNestedToolNames(description)
	if len(names) == 0 {
		return false
	}
	available := make(map[string]struct{}, len(names))
	for _, name := range names {
		available[name] = struct{}{}
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
		"不要创建", "不要新建", "不要写入", "不要修改", "不要删除", "不要执行", "不要运行",
		"别创建", "别新建", "别写入", "别修改", "别删除", "别执行", "别运行",
		"无需创建", "无需写入", "无需修改", "无需执行", "不必创建", "不必写入", "不必执行",
		"do not create", "do not write", "do not edit", "do not modify", "do not delete", "do not run", "do not execute",
		"don't create", "don't write", "don't edit", "don't delete", "don't run", "don't execute",
	} {
		if strings.Contains(text, phrase) {
			return false
		}
	}
	for _, phrase := range []string{
		"如何创建", "怎么创建", "怎样创建", "如何写入", "怎么写入", "如何修改", "怎么修改", "如何执行", "怎么执行",
		"是什么意思", "请解释", "解释一下", "举例", "示例", "教程",
		"how to create", "how to write", "how to edit", "how to modify", "how to run", "how to execute",
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
		"读取", "查看", "检查", "列出", "搜索", "查找", "打开", "执行", "运行",
		"作成", "書き込", "編集", "削除", "読み取", "実行",
		"생성", "작성", "수정", "삭제", "읽기", "실행",
	}
	targets := []string{
		"文件", "目录", "文件夹", "代码", "项目", "仓库", "命令", "脚本", "当前目录", "工作区",
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

func toolChoiceRequiresCall(choice any) bool {
	mode := normalizedToolChoiceMode(choice)
	return mode == "required" || strings.HasPrefix(mode, "named:")
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
