package web

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"m365-copilot2api/internal/chathub"
)

func workspaceTestTools(names ...string) []chathub.Tool {
	tools := make([]chathub.Tool, 0, len(names))
	for _, name := range names {
		definition, _ := json.Marshal(map[string]any{"name": name, "description": "test tool", "parameters": map[string]any{"type": "object"}})
		tools = append(tools, chathub.Tool{Type: "function", Function: definition})
	}
	return tools
}

func TestExplicitToolRequestOnlyChecksLatestUserMessage(t *testing.T) {
	if !explicitToolRequest([]oaiMsg{{Role: "user", Content: "请使用终端工具读取 go.mod，必须实际调用工具。"}}) {
		t.Fatal("explicit Chinese tool request was not detected")
	}
	if !explicitToolRequest([]oaiMsg{{Role: "user", Content: "You must actually call the terminal tool."}}) {
		t.Fatal("explicit English tool request was not detected")
	}
	if !explicitToolRequest([]oaiMsg{{Role: "user", Content: "在当前目录创建 1.txt，必须实际调用 apply_patch 工具。"}}) {
		t.Fatal("explicit named tool request was not detected")
	}
	if explicitToolRequest([]oaiMsg{{Role: "user", Content: "必须调用工具"}, {Role: "assistant", Content: "ok"}, {Role: "user", Content: "现在直接回答问题"}}) {
		t.Fatal("stale tool request affected the latest user turn")
	}
	if explicitToolRequest([]oaiMsg{{Role: "user", Content: "必须调用工具"}, {Role: "assistant", ToolCalls: []map[string]any{{"id": "call_1"}}}, {Role: "tool", ToolCallID: "call_1", Content: "done"}}) {
		t.Fatal("completed tool result reactivated the original tool request")
	}
	if !explicitToolRequest([]oaiMsg{{Role: "user", Content: "必须调用工具"}, {Role: "user", Content: "You have 100 weighted tokens left"}}) {
		t.Fatal("Codex token notice hid the explicit tool request")
	}
}

func TestToolResultContinuationReleasesGenericRequiredChoice(t *testing.T) {
	completed := []oaiMsg{
		{Role: "user", Content: "请实际检查当前环境"},
		{Role: "assistant", ToolCalls: []map[string]any{{
			"id": "call_env", "type": "function",
			"function": map[string]any{"name": "shell_command", "arguments": `{"command":"pwd"}`},
		}}},
		{Role: "tool", ToolCallID: "call_env", Content: "/home/ubuntu"},
	}
	if got := effectiveToolChoiceForTurn(completed, "required"); got != "auto" {
		t.Fatalf("completed tool continuation choice=%#v, want auto", got)
	}

	withNotice := append(append([]oaiMsg(nil), completed...), oaiMsg{Role: "user", Content: "You have 100 weighted tokens left"})
	if got := effectiveToolChoiceForTurn(withNotice, "required"); got != "auto" {
		t.Fatalf("transport notice reactivated required choice: %#v", got)
	}
}

func TestToolResultContinuationKeepsFreshRequiredRequest(t *testing.T) {
	messages := []oaiMsg{
		{Role: "assistant", ToolCalls: []map[string]any{{
			"id": "call_env", "type": "function",
			"function": map[string]any{"name": "shell_command", "arguments": `{"command":"pwd"}`},
		}}},
		{Role: "tool", ToolCallID: "call_env", Content: "/home/ubuntu"},
		{Role: "user", Content: "现在再读取 /etc/os-release，必须实际调用工具"},
	}
	if got := effectiveToolChoiceForTurn(messages, "required"); got != "required" {
		t.Fatalf("fresh user request choice=%#v, want required", got)
	}
}

func TestToolResultContinuationPreservesNamedAndNoneChoices(t *testing.T) {
	messages := []oaiMsg{
		{Role: "assistant", ToolCalls: []map[string]any{{
			"id": "call_env", "type": "function",
			"function": map[string]any{"name": "shell_command", "arguments": `{"command":"pwd"}`},
		}}},
		{Role: "tool", ToolCallID: "call_env", Content: "/home/ubuntu"},
	}
	named := map[string]any{"type": "function", "function": map[string]any{"name": "shell_command"}}
	if got := effectiveToolChoiceForTurn(messages, named); !reflect.DeepEqual(got, named) {
		t.Fatalf("named choice changed: got=%#v want=%#v", got, named)
	}
	if got := effectiveToolChoiceForTurn(messages, "none"); got != "none" {
		t.Fatalf("none choice changed: %#v", got)
	}
}

func TestWorkspaceToolRequestPromotesDirectExecution(t *testing.T) {
	tools := workspaceTestTools("shell_command")
	for _, request := range []string{
		"在当前目录创建一个 1.txt 文件，里面写 123214324",
		"你倒是创建啊",
		"Read config.json and check its contents.",
		"現在、config.jsonファイルを作成してください",
	} {
		if !workspaceToolRequest([]oaiMsg{{Role: "user", Content: request}}, tools) {
			t.Fatalf("direct workspace request was not detected: %q", request)
		}
	}
}

func TestWorkspaceToolRequestUnderstandsCodexSkillsAndHostActions(t *testing.T) {
	tools := workspaceTestTools("exec")
	for _, request := range []string{
		"你使用 computer use 技能去打开我的微信，给文件传输助手发消息。",
		"你给他跑起来做一次全量验证。用你的内置浏览器，你自己去操作。",
		"去网上搜索 Rick Astley 的官方资料并核实作者。",
		"使用 documents:documents 技能整理这个文档。",
		"请用 frontend-design 技能修改当前网页。",
		"Use the browser skill to interact with the local page.",
		"Open Notepad and type a meaningful test note.",
	} {
		if !workspaceToolRequest([]oaiMsg{{Role: "user", Content: request}}, tools) {
			t.Fatalf("Codex skill/host action was not detected: %q", request)
		}
	}
	for _, request := range []string{
		"怎么打开微信？",
		"请解释一下 computer-use 技能。",
		"What does the browser skill do?",
		"不要打开微信，只解释步骤。",
	} {
		if workspaceToolRequest([]oaiMsg{{Role: "user", Content: request}}, tools) {
			t.Fatalf("skill question was mistaken for execution: %q", request)
		}
	}
}

func TestRequestedDeclaredSkillsExpandsCallerCatalogAlias(t *testing.T) {
	messages := []oaiMsg{
		{Role: "system", Content: `## Skills
### Skill roots
- ` + "`r0` = `C:/Users/Test/.codex/skills`" + `
- ` + "`r22` = `C:/Users/Test/.codex/plugins/openai-bundled`" + `
### Available skills
- computer-use:computer-use: (file: r22/computer-use/26.727.51351/skills/computer-use/SKILL.md)
- documents:documents: (file: r0/documents/SKILL.md)`},
		{Role: "user", Content: "你使用 computer use 技能打开桌面应用"},
	}
	routes := requestedDeclaredSkills(messages)
	if len(routes) != 1 {
		t.Fatalf("routes=%#v", routes)
	}
	if routes[0].Name != "computer-use:computer-use" || routes[0].Path != "C:/Users/Test/.codex/plugins/openai-bundled/computer-use/26.727.51351/skills/computer-use/SKILL.md" {
		t.Fatalf("route=%#v", routes[0])
	}
	guard := declaredSkillRoutingGuard(routes)
	for _, want := range []string{declaredSkillRouteMarker, routes[0].Path, "aliases have already been expanded", "Do not inspect MCP"} {
		if !strings.Contains(guard, want) {
			t.Fatalf("guard missing %q: %s", want, guard)
		}
	}
}

func TestRequestedDeclaredSkillsUsesCallerSpecificRoots(t *testing.T) {
	windows := []oaiMsg{
		{Role: "system", Content: "- `r5` = `C:/Codex/Desktop`\n- computer-use:computer-use: (file: r5/computer-use/SKILL.md)"},
		{Role: "user", Content: "use computer-use now"},
	}
	linux := []oaiMsg{
		{Role: "system", Content: "- `r2` = `/opt/codex-cli`\n- computer-use:computer-use: (file: r2/computer-use/SKILL.md)"},
		{Role: "user", Content: "use computer-use now"},
	}
	if got := requestedDeclaredSkills(windows); len(got) != 1 || got[0].Path != "C:/Codex/Desktop/computer-use/SKILL.md" {
		t.Fatalf("windows routes=%#v", got)
	}
	if got := requestedDeclaredSkills(linux); len(got) != 1 || got[0].Path != "/opt/codex-cli/computer-use/SKILL.md" {
		t.Fatalf("linux routes=%#v", got)
	}
	withoutSkill := []oaiMsg{{Role: "system", Content: "- `r2` = `/opt/codex-cli`"}, {Role: "user", Content: "use computer-use now"}}
	if got := requestedDeclaredSkills(withoutSkill); len(got) != 0 {
		t.Fatalf("undeclared CLI skill was invented: %#v", got)
	}
}

func TestDeclaredSkillPathValidationRejectsGuessedAlias(t *testing.T) {
	routes := []declaredSkillRoute{{Name: "computer-use:computer-use", Path: "C:/Codex/plugins/computer-use/SKILL.md"}}
	call := func(input string) detectedToolCall {
		return detectedToolCall{Name: "exec", Arguments: json.RawMessage(mustJSON(map[string]any{"input": input}))}
	}
	if !callUsesWrongDeclaredSkillPath(call(`await tools.shell_command({command:"Get-Content -Raw 'r0/computer-use/SKILL.md'"})`), routes) {
		t.Fatal("guessed alias path was accepted")
	}
	if callUsesWrongDeclaredSkillPath(call(`await tools.shell_command({command:"Get-Content -Raw 'C:/Codex/plugins/computer-use/SKILL.md'"})`), routes) {
		t.Fatal("exact catalog path was rejected")
	}
	if callUsesWrongDeclaredSkillPath(call(`await tools.shell_command({command:"Get-Location"})`), routes) {
		t.Fatal("non-skill exec call was rejected by the path guard")
	}
}

func TestNodeReplDocumentationRequiresExplicitOutput(t *testing.T) {
	call := func(input string) detectedToolCall {
		return detectedToolCall{Name: "exec", Arguments: json.RawMessage(mustJSON(map[string]any{"input": input}))}
	}
	bare := call(`const r = await tools.mcp__node_repl__js({code: "const guidance = await sky.documentation('guidance'); ({guidance});"}); text(r);`)
	if !callOmitsNodeReplDocumentationOutput(bare) {
		t.Fatal("bare Node REPL documentation expression was accepted")
	}
	written := call(`const r = await tools.mcp__node_repl__js({code: "const guidance = await sky.documentation('guidance'); nodeRepl.write(guidance);"}); text(r);`)
	if callOmitsNodeReplDocumentationOutput(written) {
		t.Fatal("explicit nodeRepl.write was rejected")
	}
	unrelated := call(`const r = await tools.mcp__node_repl__js({code: "nodeRepl.write(42);"}); text(r);`)
	if callOmitsNodeReplDocumentationOutput(unrelated) {
		t.Fatal("non-documentation Node REPL call was rejected")
	}
}

func TestNormalizeExecNestedToolInputRepairsShellAlias(t *testing.T) {
	description := "Run JavaScript.\n### shell_command\nRun a command.\n### apply_patch\nApply a patch."
	input := `const r = await tools.exec_command({command: "Get-Location"}); text(r);`
	got, changed := normalizeExecNestedToolInput(input, description)
	if !changed || !strings.Contains(got, "tools.shell_command") || strings.Contains(got, "tools.exec_command") {
		t.Fatalf("shell alias was not normalized: changed=%t input=%q", changed, got)
	}
	unchanged, changed := normalizeExecNestedToolInput(`const r = await tools.unknown_tool({}); text(r);`, description)
	if changed || unchanged == "" {
		t.Fatalf("unknown nested tool was unexpectedly rewritten: changed=%t input=%q", changed, unchanged)
	}
}

func TestNormalizeExecNestedToolInputRepairsRuntimeArgumentShapes(t *testing.T) {
	description := "Run JavaScript.\n### shell_command\nRun a command.\n### apply_patch\nApply a patch."
	tests := []struct {
		input string
		want  string
	}{
		{`const r = await tools.shell_command({cmd: "Get-Location"}); text(r);`, `tools.shell_command({command: "Get-Location"})`},
		{`const r = await tools.shell_command({commandLine: "pwd"}); text(r);`, `tools.shell_command({command: "pwd"})`},
		{`const cmd = "pwd"; const r = await tools.shell_command({cmd}); text(r);`, `tools.shell_command({command: cmd})`},
		{`const r = await shell_command({command: "Get-Location"}); text(r);`, `await tools.shell_command({command: "Get-Location"})`},
		{`const patch = "*** Begin Patch"; const r = await tools.apply_patch({patch}); text(r);`, `tools.apply_patch(patch)`},
		{`const patch = "*** Begin Patch"; const r = await apply_patch({patch}); text(r);`, `await tools.apply_patch(patch)`},
		{`const patchText = "*** Begin Patch"; const r = await tools.apply_patch({patch: patchText}); text(r);`, `tools.apply_patch(patchText)`},
		{"const r = await tools.apply_patch({patch:\"*** Begin Patch\n*** Add File: proof.txt\n+OK\n*** End Patch\"}); text(r);", `tools.apply_patch("*** Begin Patch\n*** Add File: proof.txt\n+OK\n*** End Patch")`},
		{"const r = await tools.apply_patch({input:'*** Begin Patch\n*** Add File: proof.txt\n+OK\n*** End Patch'}); text(r);", `tools.apply_patch('*** Begin Patch\n*** Add File: proof.txt\n+OK\n*** End Patch')`},
		{"const r = await tools.apply_patch({patch:`*** Begin Patch\n*** Add File: proof.txt\n+OK\n*** End Patch`}); text(r);", "tools.apply_patch(`*** Begin Patch\n*** Add File: proof.txt\n+OK\n*** End Patch`)"},
	}
	for _, tc := range tests {
		got, changed := normalizeExecNestedToolInput(tc.input, description)
		if !changed || !strings.Contains(got, tc.want) {
			t.Fatalf("runtime arguments were not normalized: input=%q got=%q want=%q", tc.input, got, tc.want)
		}
	}

	withoutPatch := "Run JavaScript.\n### shell_command\nRun a command."
	input := `const r = await tools.apply_patch({patch}); text(r);`
	if got, changed := normalizeExecNestedToolInput(input, withoutPatch); changed || got != input {
		t.Fatalf("undeclared apply_patch was rewritten: changed=%t got=%q", changed, got)
	}
	unknown := `const r = await unknown_tool({}); text(r);`
	if got, changed := normalizeExecNestedToolInput(unknown, description); changed || got != unknown {
		t.Fatalf("unknown bare call was rewritten: changed=%t got=%q", changed, got)
	}
}

func TestNormalizeExecNestedToolInputRepairsOuterOutputHelpers(t *testing.T) {
	description := "Run JavaScript.\n### shell_command\nRun a command."
	input := `nodeRepl.write(JSON.stringify(ALL_TOOLS)); console.log("done");`
	got, changed := normalizeExecNestedToolInput(input, description)
	if !changed || got != `text(JSON.stringify(ALL_TOOLS)); text("done");` {
		t.Fatalf("outer output helpers were not normalized: changed=%t got=%q", changed, got)
	}
}

func TestNormalizeExecOutputHelperPreservesNestedProgramsAndComments(t *testing.T) {
	input := "const r = await tools.mcp__node_repl__js({code: \"nodeRepl.write('ok'); console.log('debug');\"}); " +
		"const note = `nodeRepl.write('template')`; // nodeRepl.write('comment')\n" +
		"/* console.log('block') */ text(r);"
	got, changed := normalizeExecOutputHelper(input)
	if changed || got != input {
		t.Fatalf("nested output helpers changed: changed=%t got=%q", changed, got)
	}
}

func TestEscapeInvalidJSLiteralNewlines(t *testing.T) {
	input := "const patch = \"*** Begin Patch\n+OK\n*** End Patch\";\n" +
		"const single = 'line one\r\nline two';\n" +
		"const template = `line one\nline two`; // comment \" stays literal\n" +
		"text(patch + single + template);"
	got, changed := escapeInvalidJSLiteralNewlines(input)
	if !changed {
		t.Fatal("literal newlines were not escaped")
	}
	for _, want := range []string{`"*** Begin Patch\n+OK\n*** End Patch"`, `'line one\r\nline two'`, "`line one\nline two`", "// comment \" stays literal\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized JavaScript missing %q: %q", want, got)
		}
	}
	if strings.Count(got, "\n") != 4 {
		t.Fatalf("statement/template/comment newlines changed: %q", got)
	}
}

func TestExecutionToolRequestPendingTracksFailedContinuation(t *testing.T) {
	tools := workspaceTestTools("exec")
	failed := []oaiMsg{
		{Role: "user", Content: "用内置浏览器打开本地网页并完成交互验证"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "call_1", "type": "function", "function": map[string]any{"name": "exec", "arguments": `{"input":"bad"}`}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "Script failed: invalid browser arguments"},
	}
	if !executionToolRequestPending(failed, tools, buildAgentLedger(activeMessages(failed))) {
		t.Fatal("failed real action did not remain pending")
	}
	succeeded := append([]oaiMsg(nil), failed...)
	succeeded[len(succeeded)-1].Content = "Script completed: browser interaction verified"
	if executionToolRequestPending(succeeded, tools, buildAgentLedger(activeMessages(succeeded))) {
		t.Fatal("successful real action remained pending")
	}
}

func TestExecutionToolRequestPendingContinuesAfterPreparation(t *testing.T) {
	tools := workspaceTestTools("exec")
	for _, arguments := range []string{
		`{"input":"const r = await tools.shell_command({command: 'Get-Content computer-use/SKILL.md'}); text(r);"}`,
		`{"input":"text(ALL_TOOLS.filter(tool => tool.name.includes('computer')));"}`,
		`{"input":"const r = await tools.shell_command({command: 'Get-Location; Get-ChildItem'}); text(r);"}`,
	} {
		messages := []oaiMsg{
			{Role: "user", Content: "用 Computer Use 打开微信并发送消息"},
			{Role: "assistant", ToolCalls: []map[string]any{{"id": "call_prepare", "type": "custom", "function": map[string]any{"name": "exec", "arguments": arguments}}}},
			{Role: "tool", ToolCallID: "call_prepare", Content: "Script completed: documentation returned"},
		}
		if !executionToolRequestPending(messages, tools, buildAgentLedger(activeMessages(messages))) {
			t.Fatalf("preparation was mistaken for completed UI work: %s", arguments)
		}
	}

	completed := []oaiMsg{
		{Role: "user", Content: "用 Computer Use 打开微信并发送消息"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "call_action", "type": "custom", "function": map[string]any{"name": "exec", "arguments": `{"input":"const r = await tools.mcp__node_repl__js({code: 'await computer.click(sendButton)'}); text(r);"}`}}}},
		{Role: "tool", ToolCallID: "call_action", Content: "Message sent and verified"},
	}
	if executionToolRequestPending(completed, tools, buildAgentLedger(activeMessages(completed))) {
		t.Fatal("verified Computer Use action remained pending")
	}

	listed := []oaiMsg{
		{Role: "user", Content: "请列出当前目录里的文件"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "call_list", "type": "custom", "function": map[string]any{"name": "exec", "arguments": `{"input":"const r = await tools.shell_command({command: 'Get-ChildItem'}); text(r);"}`}}}},
		{Role: "tool", ToolCallID: "call_list", Content: "go.mod\ninternal"},
	}
	if executionToolRequestPending(listed, tools, buildAgentLedger(activeMessages(listed))) {
		t.Fatal("a completed directory listing was treated as preparation")
	}
}

func TestWorkspaceToolRequestAvoidsQuestionsAndNegation(t *testing.T) {
	tools := workspaceTestTools("apply_patch")
	for _, request := range []string{
		"请解释如何创建一个 1.txt 文件",
		"不要创建 1.txt，只告诉我它是否存在",
		"What does create file mean?",
		"How to write a file in Go?",
		"I already know the file exists.",
	} {
		if workspaceToolRequest([]oaiMsg{{Role: "user", Content: request}}, tools) {
			t.Fatalf("non-execution request was promoted: %q", request)
		}
	}
	if workspaceToolRequest([]oaiMsg{{Role: "user", Content: "创建 1.txt 并写入内容"}}, workspaceTestTools("get_weather")) {
		t.Fatal("request was promoted without a workspace-capable tool")
	}
	if workspaceToolRequest([]oaiMsg{{Role: "user", Content: "创建 1.txt 并写入内容"}}, workspaceTestTools("get_profile")) {
		t.Fatal("profile tool was mistaken for a file tool")
	}
}

func TestWorkspaceToolRequestOnlyChecksLatestTurn(t *testing.T) {
	tools := workspaceTestTools("exec")
	if !workspaceToolRequest([]oaiMsg{
		{Role: "user", Content: "创建 1.txt"},
		{Role: "user", Content: "You have 100 weighted tokens left"},
	}, tools) {
		t.Fatal("Codex token notice hid the workspace action")
	}
	if workspaceToolRequest([]oaiMsg{
		{Role: "user", Content: "创建 1.txt"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "解释一下刚才的结果"},
	}, tools) {
		t.Fatal("stale workspace action affected a later question")
	}
	if workspaceToolRequest([]oaiMsg{
		{Role: "user", Content: "创建 1.txt"},
		{Role: "assistant", ToolCalls: []map[string]any{{"id": "call_1"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}, tools) {
		t.Fatal("completed workspace action was promoted again")
	}
}

func TestParseModelToolDecisionAutoAndParallel(t *testing.T) {
	calls, ok := parseModelToolDecision(`{"calls":[{"name":"get_weather","arguments":{"city":"Beijing"}},{"name":"get_time","arguments":{"city":"Beijing"}}]}`, testTools(), "auto")
	if !ok || len(calls) != 2 {
		t.Fatalf("calls=%v ok=%v", calls, ok)
	}
}

func TestParseModelToolDecisionNormalizesNullForSingleDeclaredTool(t *testing.T) {
	tools := []map[string]any{{
		"type": "custom",
		"function": map[string]any{
			"name": "exec",
			"parameters": map[string]any{
				"type":     "object",
				"required": []any{"input"},
				"properties": map[string]any{
					"input": map[string]any{"type": "string"},
				},
			},
		},
	}}
	for _, marker := range []string{"null", "undefined"} {
		t.Run(marker, func(t *testing.T) {
			calls, parsed := parseModelToolDecision(`CALL_TOOL: `+marker+`({"input":"pwd"})`, tools, "auto")
			if !parsed || len(calls) != 1 || calls[0].Name != "exec" || calls[0].Type != "custom" {
				t.Fatalf("parsed=%t calls=%#v", parsed, calls)
			}
		})
	}
}

func TestParseModelToolDecisionDoesNotGuessNullWithMultipleTools(t *testing.T) {
	tools := []map[string]any{
		{"type": "function", "function": map[string]any{"name": "first", "parameters": map[string]any{"type": "object"}}},
		{"type": "function", "function": map[string]any{"name": "second", "parameters": map[string]any{"type": "object"}}},
	}
	for _, marker := range []string{"null", "undefined"} {
		calls, parsed := parseModelToolDecision(`CALL_TOOL: `+marker+`({})`, tools, "auto")
		if len(calls) != 0 {
			t.Fatalf("marker=%s parsed=%t calls=%#v", marker, parsed, calls)
		}
	}
}

func TestParseModelToolDecisionNoCall(t *testing.T) {
	calls, ok := parseModelToolDecision(`{"calls":[]}`, testTools(), "auto")
	if !ok || len(calls) != 0 {
		t.Fatalf("calls=%v ok=%v", calls, ok)
	}
}
func TestModelToolRouterPromptMarksCompletedResults(t *testing.T) {
	p := modelToolRouterPrompt(`assistant tool_calls: [...]
tool[call_x]: 2026-07-18`, testTools(), "auto")
	if !strings.Contains(p, "Completed evidence must not be repeated") || !strings.Contains(p, "tool[call_x]: 2026-07-18") || !strings.Contains(p, "unfinished work remains") {
		t.Fatalf("missing multi-turn evidence constraint: %s", p)
	}
}

func TestModelToolRouterPromptWithExecutionIntentKeepsAutoRecoverable(t *testing.T) {
	p := modelToolRouterPromptWithIntent("[user] 在桌面创建 report.txt", testTools(), "auto", true)
	for _, want := range []string{
		"MODE: auto",
		"requested a real local or external action",
		"at least one compatible declared top-level tool",
		"Skills are instruction bundles, not callable tool names",
		"catalog is already the authoritative path map",
		"ALL_TOOLS array of {name, description}",
		"nodeRepl.write(value)",
		"Computer Use is typically available in Codex Desktop but absent from CLI environments",
		"do not invent a tool or claim the action happened",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("router prompt missing execution-intent rule %q: %s", want, p)
		}
	}
	if strings.Contains(p, "MODE requires a tool call") {
		t.Fatalf("implicit execution intent became required: %s", p)
	}
}

func TestExecutionRepairRequiresProgressWithoutChangingChoice(t *testing.T) {
	p := modelToolExecutionRepairPrompt("[user] 使用 browser 技能操作页面", "NO_TOOL_NEEDED", testTools(), "auto")
	for _, want := range []string{"TOOL_CHOICE: auto", "unfinished real action", "at least one valid declared top-level tool", "read that exact SKILL.md", "ALL_TOOLS array", "Do not return an empty calls array"} {
		if !strings.Contains(p, want) {
			t.Fatalf("execution repair prompt missing %q: %s", want, p)
		}
	}
}

func TestModelToolRouterPromptPreservesExactInstructionArguments(t *testing.T) {
	p := modelToolRouterPrompt(`[request instructions]
[developer]
Call run_environment_probe with command exactly "pwd && uname -s" and environment exactly "Ubuntu Bash".

[user]
Confirm the environment.`, testTools(), map[string]any{"function": map[string]any{"name": "run_environment_probe"}})
	for _, want := range []string{
		"Request instructions and system/developer blocks are authoritative",
		"Preserve exact tool names, argument values, paths, commands, literals, quoting, and separators",
		"Never replace an explicitly supplied argument with an equivalent value",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("router prompt missing exact-argument rule %q: %s", want, p)
		}
	}
}

func TestCompactRouterToolsPreservesValidationShape(t *testing.T) {
	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "write_file",
			"description": strings.Repeat("long function description ", 200),
			"parameters": map[string]any{
				"type":     "object",
				"required": []any{"path", "mode"},
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": strings.Repeat("path details ", 200)},
					"mode": map[string]any{"type": "string", "enum": []any{"create", "append"}},
				},
				"examples": []any{strings.Repeat("unused", 1000)},
			},
		},
	}}
	compact := compactRouterTools(tools)
	raw, _ := json.Marshal(compact)
	if len(raw) >= 3000 {
		t.Fatalf("router schema remained oversized: %d", len(raw))
	}
	function := compact[0]["function"].(map[string]any)
	parameters := function["parameters"].(map[string]any)
	properties := parameters["properties"].(map[string]any)
	mode := properties["mode"].(map[string]any)
	if function["name"] != "write_file" || len(parameters["required"].([]any)) != 2 || len(mode["enum"].([]any)) != 2 {
		t.Fatalf("router schema lost required structure: %#v", compact)
	}
	if _, exists := parameters["examples"]; exists {
		t.Fatalf("documentation-only examples were retained: %#v", parameters)
	}
}

func TestCompactRouterToolsKeepsExecNestedToolCatalog(t *testing.T) {
	description := `Run JavaScript code to orchestrate tool calls.
- Example from another runtime: await tools.exec_command(...)
### apply_patch
Apply a patch to workspace files.
### shell_command
Run a PowerShell command in the workspace.`
	tools := []map[string]any{{
		"type": "custom",
		"function": map[string]any{
			"name":        "exec",
			"description": description,
			"parameters":  map[string]any{"type": "object"},
		},
	}}

	compact := compactRouterTools(tools)
	got := compact[0]["function"].(map[string]any)["description"].(string)
	if !strings.Contains(got, "tools.apply_patch") || !strings.Contains(got, "tools.shell_command") {
		t.Fatalf("exec catalog was lost: %q", got)
	}
	for _, want := range []string{
		"do not query MCP, ALL_TOOLS, or the workspace to locate a listed skill",
		"ALL_TOOLS array of {name, description}",
		"nodeRepl.write(value)",
		"Computer Use is typically present in Codex Desktop but absent from CLI",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("exec skill contract missing %q: %q", want, got)
		}
	}
	if strings.Contains(got, "tools.exec_command") {
		t.Fatalf("stale undeclared example survived compaction: %q", got)
	}
	if len(got) > 2048 {
		t.Fatalf("exec description is unbounded: %d", len(got))
	}
}

func TestCompactExecRouterDescriptionPreservesCallerShellContract(t *testing.T) {
	tests := []struct {
		name      string
		contract  string
		want      []string
		forbidden []string
	}{
		{
			name:      "windows powershell",
			contract:  "Runs a PowerShell command on Windows. Use Get-Location; Get-ChildItem -Force.",
			want:      []string{"PowerShell", "Get-Location", "Get-ChildItem"},
			forbidden: []string{"pwd; ls -la"},
		},
		{
			name:      "linux bash",
			contract:  "Runs a Bash command on Linux. Use pwd; ls -la; grep. Do not use PowerShell.",
			want:      []string{"Bash", "pwd; ls -la", "Do not use PowerShell"},
			forbidden: []string{"For PowerShell commands", "Get-Location"},
		},
		{
			name:      "macos posix",
			contract:  "Runs a POSIX shell command on macOS. Use pwd; find . -maxdepth 2.",
			want:      []string{"POSIX", "macOS", "find . -maxdepth 2"},
			forbidden: []string{"For PowerShell commands", "Get-ChildItem"},
		},
		{
			name:      "unspecified caller shell",
			contract:  "Runs commands in the caller-declared default shell. Follow this contract exactly.",
			want:      []string{"caller-declared default shell", "Follow this contract exactly"},
			forbidden: []string{"PowerShell", "Bash", "Windows", "Linux", "macOS"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			description := "Run JavaScript through the caller bridge.\n" +
				"### apply_patch\nPATCH_SECTION_SENTINEL\n" +
				"### shell_command\n" + tt.contract + "\n" +
				"### view_image\nVIEW_IMAGE_SECTION_SENTINEL"
			got := compactExecRouterDescription(description)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("router description missing %q: %q", want, got)
				}
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(got, forbidden) {
					t.Fatalf("router description introduced %q: %q", forbidden, got)
				}
			}
			if strings.Contains(got, "VIEW_IMAGE_SECTION_SENTINEL") {
				t.Fatalf("shell section leaked into the next tool: %q", got)
			}
			if len(got) > maxExecRouterDescriptionBytes {
				t.Fatalf("exec description is unbounded: %d", len(got))
			}
		})
	}
}

func TestCompactExecRouterDescriptionHandlesMissingAndLongShellContracts(t *testing.T) {
	missing := compactExecRouterDescription("Run JavaScript.\n### shell_command\n   \n### apply_patch\nApply a patch.")
	if !strings.Contains(missing, "environment is unspecified") || strings.Contains(missing, "PowerShell") {
		t.Fatalf("missing shell contract gained an environment: %q", missing)
	}

	longContract := "SHELL_CONTRACT_HEAD " + strings.Repeat("x", maxExecRouterDescriptionBytes*2) + " SHELL_CONTRACT_TAIL"
	bounded := compactExecRouterDescription("Run JavaScript.\n### shell_command\n" + longContract)
	if len(bounded) > maxExecRouterDescriptionBytes || !strings.Contains(bounded, "SHELL_CONTRACT_HEAD") || !strings.Contains(bounded, "SHELL_CONTRACT_TAIL") {
		t.Fatalf("bounded shell contract invalid: len=%d value=%q", len(bounded), bounded)
	}
}

func TestParseModelToolDecisionRejectsBadSchema(t *testing.T) {
	calls, ok := parseModelToolDecision("```json\n{\"calls\":[{\"name\":\"get_weather\",\"arguments\":{\"city\":2}}]}\n```", testTools(), "auto")
	if !ok || len(calls) != 0 {
		t.Fatalf("calls=%v ok=%v", calls, ok)
	}
}

func TestToolChoiceRequiresCall(t *testing.T) {
	tests := []struct {
		name   string
		choice any
		want   bool
	}{
		{name: "implicit auto", choice: nil, want: false},
		{name: "auto", choice: "auto", want: false},
		{name: "none", choice: "none", want: false},
		{name: "required", choice: "required", want: true},
		{name: "named", choice: map[string]any{"function": map[string]any{"name": "get_weather"}}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolChoiceRequiresCall(tt.choice); got != tt.want {
				t.Fatalf("toolChoiceRequiresCall(%#v)=%v want %v", tt.choice, got, tt.want)
			}
		})
	}
}

func TestToolRouterTimeoutsStayBelowDownstreamDeadline(t *testing.T) {
	if got := toolRouterTotalTimeout(120); got != 55*time.Second {
		t.Fatalf("default router budget=%s want 55s", got)
	}
	if got := toolRouterTotalTimeout(9); got != 9*time.Second {
		t.Fatalf("configured short router budget=%s want 9s", got)
	}
	if got := requiredToolRouterAttemptTimeout(120); got != 18*time.Second {
		t.Fatalf("required router attempt timeout=%s want 18s", got)
	}
	if maxToolRouterTotalTimeout >= 60*time.Second {
		t.Fatalf("router failover budget=%s must stay below downstream 60s deadline", maxToolRouterTotalTimeout)
	}
	if total := time.Duration(maxToolRouterAccountAttempts) * requiredToolRouterAttemptTimeout(120); total > maxToolRouterTotalTimeout {
		t.Fatalf("required account attempts=%s exceed router budget=%s", total, maxToolRouterTotalTimeout)
	}
}

func TestCallToolRouterWithToneFallbackRetriesEmptyWithMagic(t *testing.T) {
	var tones []string
	result, err := callToolRouterWithToneFallback("precise", func(tone string) (chathub.Result, error) {
		tones = append(tones, tone)
		if tone != "magic" {
			return chathub.Result{}, chathub.ErrEmptyCompletion
		}
		return chathub.Result{Text: `CALL_TOOL: apply_patch({"input":"patch"})`}, nil
	})
	if err != nil || result.Text == "" || !reflect.DeepEqual(tones, []string{"precise", "magic"}) {
		t.Fatalf("result=%#v err=%v tones=%v", result, err, tones)
	}
}

func TestCallToolRouterWithToneFallbackDoesNotRetryOtherFailures(t *testing.T) {
	want := errors.New("terminal")
	calls := 0
	_, err := callToolRouterWithToneFallback("precise", func(string) (chathub.Result, error) {
		calls++
		return chathub.Result{}, want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestCallToolRouterWithToneFallbackDoesNotLoopMagic(t *testing.T) {
	calls := 0
	_, err := callToolRouterWithToneFallback("magic", func(string) (chathub.Result, error) {
		calls++
		return chathub.Result{}, chathub.ErrEmptyCompletion
	})
	if !IsEmptyCompletion(err) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestRequiredToolDecisionRejectsEmptyOrInvalidOutput(t *testing.T) {
	for _, output := range []string{
		"I will answer without a tool.",
		`{"calls":[]}`,
		`{"calls":[{"name":"missing_tool","arguments":{}}]}`,
	} {
		calls, parsed := parseModelToolDecision(output, testTools(), "required")
		calls, _ = validateDetectedToolCalls(calls, testTools(), "required")
		if parsed && len(calls) > 0 {
			t.Fatalf("invalid required decision accepted: %q", output)
		}
	}

	calls, parsed := parseModelToolDecision(`{"calls":[{"name":"get_weather","arguments":{"city":"Shanghai"}}]}`, testTools(), "required")
	calls, _ = validateDetectedToolCalls(calls, testTools(), "required")
	if !parsed || len(calls) != 1 || calls[0].Name != "get_weather" {
		t.Fatalf("valid required decision rejected: parsed=%v calls=%+v", parsed, calls)
	}
}
