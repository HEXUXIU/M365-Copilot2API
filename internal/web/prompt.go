package web

import (
	"fmt"
	"m365-copilot2api/internal/chathub"
	"regexp"
	"strings"
)

var (
	systemReminderBlockPattern = regexp.MustCompile(`(?is)<system-reminder\b[^>]*>.*?</system-reminder\s*>`)
	envContextBlockPattern     = regexp.MustCompile(`(?is)<environment_context\b[^>]*>.*?</environment_context\s*>`)
	billingHeaderPattern       = regexp.MustCompile(`(?i)x-anthropic-billing-header:[^;\n]*;?\s*`)
	ccKeyValPattern            = regexp.MustCompile(`(?i)\bcc_[a-z0-9_]+=[^;\n]*;?\s*`)
)

var clientIdentityRewrites = [][2]string{
	{"You are Claude Code, Anthropic's official CLI for Claude.", "You are an interactive coding assistant."},
	{"You are ZCode, an interactive coding agent", "You are an interactive coding assistant"},
	{"You are an interactive ZCode agent that helps users with software engineering tasks.", "You help users with software engineering tasks."},
	{"Main branch (you will usually use this for PRs)", "Default branch (you will usually use this for PRs)"},
}

// sanitizeClientSystemText strips client billing/telemetry headers and rewrites
// vendor-specific agent identity claims (ported from linbeize/workbuddy2api
// internal/upstream/sanitize.go) so M365 Copilot does not reject the prompt on
// identity-override or third-party CLI fingerprints.
func sanitizeClientSystemText(text string) string {
	if text == "" {
		return ""
	}
	for _, rw := range clientIdentityRewrites {
		text = strings.ReplaceAll(text, rw[0], rw[1])
	}
	if billingHeaderPattern.MatchString(text) {
		text = billingHeaderPattern.ReplaceAllString(text, "")
	}
	if strings.Contains(text, "cc_") {
		prev := ""
		for prev != text {
			prev = text
			text = ccKeyValPattern.ReplaceAllString(text, "")
		}
	}
	return strings.TrimSpace(text)
}

// extractHarnessContextFromUser splits harness-injected context blocks
// (<system-reminder>...</system-reminder>, <environment_context>...</environment_context>,
// or DSH "Current runtime context." snapshots) out of a user message so they
// can be placed in the leading [system] section rather than burying the user's
// actual request at the end of the prompt.
func extractHarnessContextFromUser(txt string) (cleanUser string, extractedSystem []string) {
	trimmed := strings.TrimSpace(txt)
	if trimmed == "" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "Current runtime context.") {
		return "", []string{trimmed}
	}
	for _, m := range systemReminderBlockPattern.FindAllString(trimmed, -1) {
		extractedSystem = append(extractedSystem, strings.TrimSpace(m))
	}
	trimmed = systemReminderBlockPattern.ReplaceAllString(trimmed, "")
	for _, m := range envContextBlockPattern.FindAllString(trimmed, -1) {
		extractedSystem = append(extractedSystem, strings.TrimSpace(m))
	}
	trimmed = envContextBlockPattern.ReplaceAllString(trimmed, "")
	return strings.TrimSpace(trimmed), extractedSystem
}

// incrementalSliceStart returns the safe start index for an incremental
// prompt slice. Agent harnesses (DSH, Claude Code, ZCode, OpenCode) dynamically
// insert or move <system-reminder> and runtime-context user messages at the
// tail of the messages array across turns, so a naive messages[historyLen:]
// slice can land on trailing reminder-only messages and cut off the actual
// user task or tool results. Walking back to include the latest real user task
// guarantees the model always sees the active task and all subsequent tool turns.
//
// A tool-result continuation is the case that matters most: the tail holds only
// the executed call's result (plus harness reminders), so sending messages[historyLen:]
// alone leaves the upstream model with a result it cannot attribute to anything
// — it answers the formatting spec instead of the task. The slice therefore
// always reaches back to the real user request that owns those tool turns.
func incrementalSliceStart(messages []oaiMsg, historyLen int) int {
	if historyLen <= 0 || historyLen >= len(messages) {
		return historyLen
	}
	// A fresh user turn in the tail is self-contained: the cloud conversation
	// already carries everything before it, so keep historyLen.
	for i := historyLen; i < len(messages); i++ {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role != "user" && role != "" {
			continue
		}
		txt, _ := parseContent(messages[i].Content)
		cleanTxt, _ := extractHarnessContextFromUser(txt)
		if cleanTxt != "" {
			return historyLen
		}
	}
	// Otherwise the tail is tool results and/or harness reminders. Walk back to
	// the most recent real user request so the model sees what it is finishing.
	for i := historyLen - 1; i >= 0; i-- {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role != "user" && role != "" {
			continue
		}
		txt, _ := parseContent(messages[i].Content)
		cleanTxt, _ := extractHarnessContextFromUser(txt)
		if cleanTxt != "" {
			return i
		}
	}
	// No real user turn exists above the tool results (an assistant-only
	// continuation); include the whole tool block.
	for i := historyLen - 1; i >= 0; i-- {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role == "tool" {
			for i > 0 && strings.ToLower(strings.TrimSpace(messages[i-1].Role)) == "tool" {
				i--
			}
			return i
		}
		if role == "assistant" && len(messages[i].ToolCalls) > 0 {
			return i
		}
	}
	return historyLen
}

func hasActionableTurn(flattened string) bool {
	return strings.Contains(flattened, "\n[user]\n") ||
		strings.HasPrefix(flattened, "[user]\n") ||
		strings.Contains(flattened, "\n[tool result id=") ||
		strings.HasPrefix(flattened, "[tool result id=")
}

func flattenPromptMessagesBudgeted(messages []oaiMsg, attachments []chathub.Attachment, budget int) (string, []chathub.Attachment, bool, error) {
	truncatedMsgs, truncated, err := slidingWindow(messages, budget)
	if err != nil {
		return "", attachments, false, err
	}
	prompt, atts := flattenPromptMessages(truncatedMsgs, attachments)
	return prompt, atts, truncated, nil
}

// flattenAtoms delegates to context_budget flattenAtoms for atom-aware flattening.
func flattenAtomsAlias(atoms []contextAtom, attachments []chathub.Attachment) (string, []chathub.Attachment) {
	return flattenAtoms(atoms, attachments)
}

func flattenPromptMessages(messages []oaiMsg, attachments []chathub.Attachment) (string, []chathub.Attachment) {
	var systemParts []string
	type flatTurn struct {
		role       string
		text       string
		toolCalls  []map[string]any
		toolCallID string
	}
	var turns []flatTurn
	lastRealUserText := ""

	for _, m := range messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role == "" {
			role = "user"
		}
		if role == "system" || role == "developer" {
			txt, sysFiles := parseContent(m.Content)
			attachments = append(attachments, sysFiles...)
			txt = strings.TrimSpace(txt)
			if txt != "" {
				systemParts = append(systemParts, txt)
			}
			continue
		}
		content := m.Content
		if role == "tool" {
			switch content.(type) {
			case nil:
				content = ""
			case string:
			default:
				content = mustJSON(content)
			}
		}
		txt, files := parseContent(content)
		attachments = append(attachments, files...)
		txt = strings.TrimSpace(txt)
		if role == "user" && len(m.ToolCalls) == 0 {
			cleanTxt, sysExtracted := extractHarnessContextFromUser(txt)
			if len(sysExtracted) > 0 {
				systemParts = append(systemParts, sysExtracted...)
			}
			txt = cleanTxt
			if txt != "" {
				lastRealUserText = txt
			}
		}
		if len(m.ToolCalls) == 0 && role != "tool" && txt == "" {
			continue
		}
		turns = append(turns, flatTurn{
			role:       role,
			text:       txt,
			toolCalls:  m.ToolCalls,
			toolCallID: m.ToolCallID,
		})
	}

	var b strings.Builder
	if len(systemParts) > 0 {
		cleanedSys := make([]string, 0, len(systemParts))
		for _, sp := range systemParts {
			if s := sanitizeClientSystemText(sp); s != "" {
				cleanedSys = append(cleanedSys, s)
			}
		}
		if len(cleanedSys) > 0 {
			b.WriteString("\n[system]\n")
			b.WriteString(strings.Join(cleanedSys, "\n\n"))
			b.WriteString("\n")
		}
	}
	lastRole := ""
	for _, t := range turns {
		lastRole = t.role
		if len(t.toolCalls) > 0 {
			if t.text != "" {
				b.WriteString(fmt.Sprintf("\n[%s]\n%s\n", t.role, t.text))
			}
			b.WriteString(fmt.Sprintf("\n[%s tool_calls]\n%s\n", t.role, mustJSON(t.toolCalls)))
			continue
		}
		if t.role == "tool" {
			txt := compactToolResult(t.text, 4000)
			b.WriteString(fmt.Sprintf("\n[tool result id=%s]\n%s\n", t.toolCallID, txt))
			continue
		}
		if t.text == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("\n[%s]\n%s\n", t.role, t.text))
	}
	if lastRole == "tool" {
		if lastRealUserText != "" {
			b.WriteString(fmt.Sprintf("\n[continuation]\nThe tool action above has been executed by the host and its output/error is shown above. Continue fulfilling the user's task (%s): if the previous action failed or more steps are needed, emit the next tool call now; otherwise give the user your final answer.\n", compactToolResult(lastRealUserText, 240)))
		} else {
			b.WriteString("\n[continuation]\nThe tool action above has been executed by the host and its output/error is shown above. Continue the task: if the previous action failed or more steps are needed, emit the next tool call now; otherwise give the user your final answer.\n")
		}
	}
	return strings.TrimSpace(b.String()), attachments
}
