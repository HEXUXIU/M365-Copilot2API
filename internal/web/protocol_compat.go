package web

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"m365-copilot2api/internal/chathub"
)

// responsesRequest is the OpenAI Responses API request subset supported by the gateway.
type responsesRequest struct {
	Model              string           `json:"model"`
	AccountID          string           `json:"accountId,omitempty"`
	Instructions       string           `json:"instructions,omitempty"`
	Input              any              `json:"input"`
	Tools              []map[string]any `json:"tools,omitempty"`
	ToolChoice         any              `json:"tool_choice,omitempty"`
	Stream             bool             `json:"stream,omitempty"`
	ServiceTier        string           `json:"service_tier,omitempty"`
	User               string           `json:"user,omitempty"`
	Reasoning          *reasoningConfig `json:"reasoning,omitempty"`
	PromptCacheKey     string           `json:"prompt_cache_key,omitempty"`
	PreviousResponseID string           `json:"previous_response_id,omitempty"`
	Conversation       string           `json:"conversation,omitempty"`
	NewConversation    bool             `json:"new_conversation,omitempty"`
	Temperature        *float64         `json:"temperature,omitempty"`
	TopP               *float64         `json:"top_p,omitempty"`
	MaxOutputTokens    *int             `json:"max_output_tokens,omitempty"`
}

const redundantProbeInstructionsMinBytes = 8 << 10

var arithmeticHealthProbePattern = regexp.MustCompile(`(?s)^Calculate and respond with ONLY the number, nothing else\.\s+Q:\s*-?\d+\s*[+\-*/]\s*-?\d+\s*=\s*\?\s*A:\s*-?\d+\s+Q:\s*-?\d+\s*[+\-*/]\s*-?\d+\s*=\s*\?\s*A:\s*-?\d+\s+Q:\s*-?\d+\s*[+\-*/]\s*-?\d+\s*=\s*\?\s*A:\s*$`)

// compactRedundantProbeInstructions removes the Codex client bootstrap from a
// narrowly identified arithmetic health check. The probe already contains its
// complete output contract, so forwarding a 20+ KiB coding-agent instruction
// on every minute-long check only wastes context and obscures usage metrics.
func compactRedundantProbeInstructions(r *responsesRequest) bool {
	if r == nil || len(r.Instructions) < redundantProbeInstructionsMinBytes || len(r.Tools) != 0 ||
		strings.TrimSpace(r.PreviousResponseID) != "" || strings.TrimSpace(r.Conversation) != "" ||
		strings.TrimSpace(r.PromptCacheKey) != "" {
		return false
	}
	items, ok := r.Input.([]any)
	if !ok || len(items) != 1 {
		return false
	}
	item, ok := items[0].(map[string]any)
	if !ok || !strings.EqualFold(strings.TrimSpace(fmt.Sprint(item["role"])), "user") {
		return false
	}
	text, ok := item["content"].(string)
	if !ok || !arithmeticHealthProbePattern.MatchString(strings.TrimSpace(text)) {
		return false
	}
	r.Instructions = ""
	return true
}

const customExecWorkspaceInstruction = `You are operating through the caller's local execution bridge. Never use, request, or mention remote native tools. The only permitted execution tools are the caller-provided custom tools, including exec and apply_patch when declared. The top-level exec tool is an orchestration bridge whose JavaScript can call its listed nested tools instead of assuming exec is only a terminal. Runtime capabilities are caller-specific: Computer Use is optional and never a prerequisite. Use Browser, Computer Use, application, or shell runtimes only when this request's skill catalog or ALL_TOOLS actually declares them; otherwise use the declared caller-local exec bridge and its available nested tools. A missing Computer Use entry is a routing fact, not a reason to stop or switch environments. Codex skills are instruction bundles, not callable tool names. When the user names an available skill or the task clearly matches one, resolve its exact SKILL.md path from the supplied skill catalog and make the next exec call read that complete file. The skill catalog is already the authoritative path map: do not query MCP resources, ALL_TOOLS, or the workspace to locate a listed skill. Read every required referenced instruction or resource and follow the skill's runtime instructions before acting. Only after reading the skill, if it requires a deferred nested tool, inspect the ALL_TOOLS array of {name, description} entries inside exec to locate its exact name and contract, for example ALL_TOOLS.filter(tool => tool.name.includes('browser')); never invent tools.<skill-name>. Reading SKILL.md, documentation, ALL_TOOLS, or workspace metadata is preparation only and never completes a requested file, browser, desktop, send, or interaction task; invoke the discovered runtime on the following turn and perform the action. The executor starts in the project workspace selected by the caller. Work in that workspace by default. When the user explicitly names a caller-local known folder such as Desktop, Downloads, or Documents, resolve that exact folder through the caller's declared shell and operate there; never silently substitute the workspace. On Windows PowerShell, resolve Desktop with [Environment]::GetFolderPath('Desktop'), combine paths with Join-Path, and write UTF-8 text with Set-Content -LiteralPath $path -Value 'text' -Encoding utf8; keep every quote and parameter complete. Do not guess absolute paths or write under /root, /workspace, /mnt/data, /tmp, or another absolute project path that the user did not explicitly request. Use custom exec to inspect the actual working directory before workspace changes and to resolve a requested known folder before writing there. Never claim a file was created, modified, searched, sent, opened, or verified until the matching custom tool returns a successful result. After every change, use custom exec to verify the result. Do not treat a workspace listing as evidence that a requested desktop UI, browser, or computer-use action occurred. codex_app__navigate_to_codex_page accepts a Codex threadId and must never be used to open a URL, local webpage, or desktop application. A failed tool result is actionable evidence: correct the syntax, arguments, or tool selection and retry with a changed strategy in the same user task. The caller-provided shell contract is authoritative. A command failure does not mean the workspace moved or the shell changed. Never claim execution switched to a remote container unless a matching caller tool result explicitly says so.`

const customExecNodeReplInstruction = `When invoking mcp__node_repl__js, its code must emit textual return values with nodeRepl.write(value) or images with await nodeRepl.emitImage(value). Bare final JavaScript expressions return no output. Explicitly write every sky.documentation result before continuing; if a prior Node REPL result was empty, retry with nodeRepl.write and changed code.`

const customExecEffectiveInstruction = customExecWorkspaceInstruction + "\n" + customExecNodeReplInstruction

const (
	emptyToolOutputPlaceholder   = "(no tool output)"
	missingToolOutputPlaceholder = "Tool execution did not return a result."
)

// normalizeResponsesToolHistory makes reconstructed Responses items a valid
// OpenAI tool conversation. The caller's original input remains untouched so
// affinity/cache digests continue to describe the public request exactly.
func normalizeResponsesToolHistory(messages []oaiMsg) ([]oaiMsg, error) {
	out := make([]oaiMsg, 0, len(messages)+2)
	pending := make(map[string]struct{})
	pendingOrder := make([]string, 0)
	completed := make(map[string]struct{})

	flushMissing := func() {
		for _, id := range pendingOrder {
			if _, ok := pending[id]; !ok {
				continue
			}
			out = append(out, oaiMsg{Role: "tool", ToolCallID: id, Content: missingToolOutputPlaceholder})
			delete(pending, id)
			completed[id] = struct{}{}
		}
		pendingOrder = pendingOrder[:0]
	}

	for i, message := range messages {
		if message.Role == "assistant" {
			// Responses represents message and function-call output items
			// separately. They still belong to one assistant turn.
			if len(out) > 0 && out[len(out)-1].Role == "assistant" && (len(out[len(out)-1].ToolCalls) > 0 || len(message.ToolCalls) > 0) {
				last := &out[len(out)-1]
				if strings.TrimSpace(contentToString(message.Content)) != "" {
					if strings.TrimSpace(contentToString(last.Content)) == "" {
						last.Content = message.Content
					} else {
						last.Content = contentToString(last.Content) + contentToString(message.Content)
					}
				}
				for _, call := range message.ToolCalls {
					id, _ := call["id"].(string)
					id = strings.TrimSpace(id)
					if id == "" {
						return nil, fmt.Errorf("assistant tool call missing call_id at index %d", i)
					}
					if _, ok := pending[id]; ok {
						return nil, fmt.Errorf("duplicate tool call_id: %s", id)
					}
					if _, ok := completed[id]; ok {
						return nil, fmt.Errorf("duplicate tool call_id: %s", id)
					}
					call["id"] = id
					last.ToolCalls = append(last.ToolCalls, call)
					pending[id] = struct{}{}
					pendingOrder = append(pendingOrder, id)
				}
				continue
			}
			if len(pending) > 0 {
				flushMissing()
			}
			for _, call := range message.ToolCalls {
				id, _ := call["id"].(string)
				id = strings.TrimSpace(id)
				if id == "" {
					return nil, fmt.Errorf("assistant tool call missing call_id at index %d", i)
				}
				if _, ok := completed[id]; ok {
					return nil, fmt.Errorf("duplicate tool call_id: %s", id)
				}
				if _, ok := pending[id]; ok {
					return nil, fmt.Errorf("duplicate tool call_id: %s", id)
				}
				call["id"] = id
				pending[id] = struct{}{}
				pendingOrder = append(pendingOrder, id)
			}
			out = append(out, message)
			continue
		}

		if message.Role == "tool" {
			id := strings.TrimSpace(message.ToolCallID)
			if id == "" {
				return nil, fmt.Errorf("tool result missing call_id at index %d", i)
			}
			if _, ok := pending[id]; !ok {
				return nil, fmt.Errorf("tool result has no matching call_id: %s", id)
			}
			if strings.TrimSpace(contentToString(message.Content)) == "" {
				message.Content = emptyToolOutputPlaceholder
			}
			message.ToolCallID = id
			delete(pending, id)
			completed[id] = struct{}{}
			out = append(out, message)
			continue
		}

		if len(pending) > 0 {
			flushMissing()
		}
		out = append(out, message)
	}
	if len(pending) > 0 {
		flushMissing()
	}
	return out, nil
}

func (r responsesRequest) openAI() (oaiReq, error) {
	o := oaiReq{Model: r.Model, AccountID: r.AccountID, Stream: r.Stream, ToolChoice: r.ToolChoice, User: r.User, PromptCacheKey: r.PromptCacheKey, ServiceTier: r.ServiceTier}
	if r.Temperature != nil {
		o.Temperature = r.Temperature
	}
	if r.TopP != nil {
		o.TopP = r.TopP
	}
	if r.MaxOutputTokens != nil {
		o.MaxCompletionTokens = r.MaxOutputTokens
	}
	if instructions := strings.TrimSpace(r.Instructions); instructions != "" {
		o.Messages = append(o.Messages, oaiMsg{Role: "system", Content: instructions})
	}
	if r.Reasoning != nil {
		o.Reasoning = r.Reasoning
		o.ReasoningEffort = r.Reasoning.Effort
	}
	switch v := r.Input.(type) {
	case string:
		if v == "" {
			return o, fmt.Errorf("input required")
		}
		o.Messages = append(o.Messages, oaiMsg{Role: "user", Content: v})
	case []any:
		for _, raw := range v {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := m["type"].(string)
			switch typ {
			case "additional_tools":
				inlineTools, _ := m["tools"].([]any)
				for _, rawTool := range inlineTools {
					tool, ok := rawTool.(map[string]any)
					if !ok {
						continue
					}
					toolType, _ := tool["type"].(string)
					if toolType == "custom" || toolType == "function" {
						r.Tools = append(r.Tools, tool)
					}
				}
				continue
			case "reasoning":
				// A Responses reasoning item is opaque model output carried in a
				// reconstructed history. It is not a user message and must not be
				// replayed into ChatHub as one.
				continue
			case "function_call_progress":
				// Progress is deliberately not converted into an assistant/tool
				// message. It is transport metadata from a long-running client-side
				// executor and must not trigger a model turn or tool completion.
				if _, ok := parseToolProgress(m); !ok {
					return o, fmt.Errorf("invalid function_call_progress")
				}
				continue
			case "function_call_output":
				id, _ := m["call_id"].(string)
				if strings.TrimSpace(id) == "" {
					return o, fmt.Errorf("function_call_output missing call_id")
				}
				o.Messages = append(o.Messages, oaiMsg{Role: "tool", ToolCallID: strings.TrimSpace(id), Content: m["output"]})
			case "custom_tool_call_output":
				id, _ := m["call_id"].(string)
				if strings.TrimSpace(id) == "" {
					return o, fmt.Errorf("custom_tool_call_output missing call_id")
				}
				o.Messages = append(o.Messages, oaiMsg{Role: "tool", ToolCallID: strings.TrimSpace(id), Content: m["output"]})
			case "function_call":
				id, _ := m["call_id"].(string)
				name, _ := m["name"].(string)
				args := m["arguments"]
				if s, ok := args.(string); ok {
					var x any
					if json.Unmarshal([]byte(s), &x) == nil {
						args = x
					}
				}
				o.Messages = append(o.Messages, oaiMsg{Role: "assistant", ToolCalls: []map[string]any{{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": mustJSON(args)}}}})
			case "custom_tool_call":
				id, _ := m["call_id"].(string)
				name, _ := m["name"].(string)
				input, _ := m["input"].(string)
				o.Messages = append(o.Messages, oaiMsg{Role: "assistant", ToolCalls: []map[string]any{{"id": id, "type": "custom", "function": map[string]any{"name": name, "arguments": mustJSON(map[string]any{"input": input})}}}})
			default:
				role, _ := m["role"].(string)
				if role == "" {
					role = "user"
				}
				// Responses input items use input_text/input_image/input_file/
				// input_audio blocks. Keep the blocks intact so flattenPromptMessages
				// can extract every attachment into the ChatHub payload.
				content := m["content"]
				if content == nil {
					content = []any{m}
				}
				o.Messages = append(o.Messages, oaiMsg{Role: role, Content: content})
			}
		}
	default:
		return o, fmt.Errorf("input must be string or array")
	}
	hasCustomExec := false
	for _, t := range r.Tools {
		typ, _ := t["type"].(string)
		name, _ := t["name"].(string)
		if typ == "custom" && name == "exec" {
			hasCustomExec = true
			break
		}
	}
	for _, t := range r.Tools {
		typ, _ := t["type"].(string)
		// A custom exec declaration selects the caller-local tool bridge. Keep
		// every custom tool from that bridge (Codex commonly sends apply_patch
		// alongside exec), while excluding remote/native function tools.
		if hasCustomExec && typ != "custom" {
			continue
		}
		f := map[string]any{"name": t["name"], "description": t["description"], "parameters": t["parameters"]}
		if typ == "custom" {
			// ChatHub accepts JSON function arguments while Responses custom tools
			// accept grammar-constrained raw input. Preserve the public tool type
			// and bridge that raw input through a single string field. This covers
			// exec as well as clients such as Codex that expose apply_patch as a
			// custom tool.
			f["parameters"] = map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}}, "required": []string{"input"}, "additionalProperties": false}
		} else if typ != "function" {
			continue
		}
		b, _ := json.Marshal(f)
		o.Tools = append(o.Tools, chathub.Tool{Type: typ, Function: b})
	}
	if hasCustomExec {
		o.Messages = append([]oaiMsg{{Role: "system", Content: customExecEffectiveInstruction}}, o.Messages...)
	}
	// A direct workspace action is strong evidence that the planner should try a
	// compatible local tool, but it is not the caller's explicit
	// tool_choice=required constraint. Keeping those distinct avoids turning an
	// ordinary create/search request into a multi-account retry storm when the
	// upstream planner emits an imperfect structured decision.
	o.ExplicitToolRequired = explicitToolRequestInResponses(o.Messages)
	return o, nil
}

// Responses clients may append internal user items after the application task.
// Preserve a user-stated requirement to call a tool until the task is completed
// or a tool result starts the continuation turn. Direct execution intent is
// tracked separately by workspaceToolRequest at routing time.
func explicitToolRequestInResponses(messages []oaiMsg) bool {
	required := false
	for _, message := range messages {
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			if explicitToolRequest([]oaiMsg{message}) {
				required = true
			}
		case "tool":
			required = false
		case "assistant":
			if len(message.ToolCalls) == 0 {
				required = false
			}
		}
	}
	return required
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}
type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}
type anthropicRequest struct {
	Model         string             `json:"model"`
	System        any                `json:"system,omitempty"`
	Messages      []anthropicMessage `json:"messages"`
	Tools         []anthropicTool    `json:"tools,omitempty"`
	ToolChoice    any                `json:"tool_choice,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
	MaxTokens     int                `json:"max_tokens,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
}

func (r anthropicRequest) openAI() (oaiReq, error) {
	o := oaiReq{Model: r.Model, Stream: r.Stream}
	if r.MaxTokens > 0 {
		mt := r.MaxTokens
		o.MaxCompletionTokens = &mt
	}
	if len(r.StopSequences) > 0 {
		o.Stop = r.StopSequences
	}
	if r.System != nil {
		o.Messages = append(o.Messages, oaiMsg{Role: "system", Content: r.System})
	}
	for _, m := range r.Messages {
		if s, ok := m.Content.(string); ok {
			o.Messages = append(o.Messages, oaiMsg{Role: m.Role, Content: s})
			continue
		}
		blocks, ok := m.Content.([]any)
		if !ok {
			return o, fmt.Errorf("invalid anthropic content")
		}
		var text []any
		var calls []map[string]any
		for _, raw := range blocks {
			b, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typ, _ := b["type"].(string)
			switch typ {
			case "text":
				text = append(text, b)
			case "image":
				source, _ := b["source"].(map[string]any)
				if source != nil {
					srcType, _ := source["type"].(string)
					switch srcType {
					case "base64":
						data, _ := source["data"].(string)
						media, _ := source["media_type"].(string)
						if data != "" {
							if media == "" {
								media = "application/octet-stream"
							}
							text = append(text, map[string]any{
								"type":      "input_image",
								"image_url": "data:" + media + ";base64," + data,
							})
						}
					case "url":
						url, _ := source["url"].(string)
						if url != "" {
							text = append(text, map[string]any{
								"type":      "input_image",
								"image_url": url,
							})
						}
					}
				}
			case "tool_use":
				calls = append(calls, map[string]any{"id": b["id"], "type": "function", "function": map[string]any{"name": b["name"], "arguments": mustJSON(b["input"])}})
			case "tool_result":
				id, _ := b["tool_use_id"].(string)
				o.Messages = append(o.Messages, oaiMsg{Role: "tool", ToolCallID: id, Content: b["content"]})
			}
		}
		if len(text) > 0 || len(calls) > 0 {
			o.Messages = append(o.Messages, oaiMsg{Role: m.Role, Content: text, ToolCalls: calls})
		}
	}
	for _, t := range r.Tools {
		f := map[string]any{"name": t.Name, "description": t.Description, "parameters": t.InputSchema}
		b, _ := json.Marshal(f)
		o.Tools = append(o.Tools, chathub.Tool{Type: "function", Function: b})
	}
	if c, ok := r.ToolChoice.(map[string]any); ok {
		switch c["type"] {
		case "auto":
			o.ToolChoice = "auto"
		case "any":
			o.ToolChoice = "required"
		case "none":
			o.ToolChoice = "none"
		case "tool":
			o.ToolChoice = map[string]any{"type": "function", "function": map[string]any{"name": c["name"]}}
		}
	}
	return o, nil
}
