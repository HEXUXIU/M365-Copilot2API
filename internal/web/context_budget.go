package web

import (
	"fmt"
	"regexp"
	"strings"

	"m365-copilot2api/internal/chathub"
)

type atomKind string

const (
	kindSystem atomKind = "SYSTEM"
	kindUser   atomKind = "USER"
	kindTool   atomKind = "ATOM_TOOL"
	kindAssist atomKind = "ASSIST"
	kindAnchor atomKind = "ANCHOR"
)

type contextAtom struct {
	Kind   atomKind
	Msgs   []oaiMsg
	Tokens int
	Start  int
	End    int
}

func estimateBudgetTokens(text string) int {
	return heuristicTokenCount(text)
}

func estimateMessageTokens(m oaiMsg, counter func(string) int) int {
	if counter == nil {
		counter = estimateBudgetTokens
	}
	tokens := messageProtocolTokens
	tokens += counter(m.Role)
	tokens += counter(m.Name)
	tokens += counter(m.ToolCallID)
	tokens += serializedTokenCount(m.Content, counter)
	for _, call := range m.ToolCalls {
		tokens += serializedTokenCount(call, counter)
	}
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

func buildAtomsFast(messages []oaiMsg) []contextAtom {
	return buildAtomsWithCounter(messages, heuristicTokenCount)
}

func buildAtoms(messages []oaiMsg) []contextAtom {
	counter, _ := tokenEstimator("gpt-4")
	if counter == nil {
		counter = heuristicTokenCount
	}
	return buildAtomsWithCounter(messages, counter)
}

func buildAtomsWithCounter(messages []oaiMsg, counter func(string) int) []contextAtom {
	if len(messages) == 0 {
		return nil
	}
	var atoms []contextAtom
	i := 0
	for i < len(messages) {
		m := messages[i]
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role == "system" || role == "developer" {
			start := i
			var msgs []oaiMsg
			total := 0
			for i < len(messages) {
				r := strings.ToLower(strings.TrimSpace(messages[i].Role))
				if r != "system" && r != "developer" {
					break
				}
				msgs = append(msgs, messages[i])
				total += estimateMessageTokens(messages[i], counter)
				i++
			}
			atoms = append(atoms, contextAtom{Kind: kindSystem, Msgs: msgs, Tokens: total, Start: start, End: i})
			continue
		}
		if role == "assistant" && len(m.ToolCalls) > 0 {
			start := i
			var msgs []oaiMsg
			total := 0
			msgs = append(msgs, m)
			total += estimateMessageTokens(m, counter)
			i++
			for i < len(messages) && strings.ToLower(strings.TrimSpace(messages[i].Role)) == "tool" {
				msgs = append(msgs, messages[i])
				total += estimateMessageTokens(messages[i], counter)
				i++
			}
			atoms = append(atoms, contextAtom{Kind: kindTool, Msgs: msgs, Tokens: total, Start: start, End: i})
			continue
		}
		if role == "tool" {
			start := i
			var msgs []oaiMsg
			total := 0
			for i < len(messages) && strings.ToLower(strings.TrimSpace(messages[i].Role)) == "tool" {
				msgs = append(msgs, messages[i])
				total += estimateMessageTokens(messages[i], counter)
				i++
			}
			atoms = append(atoms, contextAtom{Kind: kindTool, Msgs: msgs, Tokens: total, Start: start, End: i})
			continue
		}
		if role == "user" {
			atoms = append(atoms, contextAtom{Kind: kindUser, Msgs: []oaiMsg{m}, Tokens: estimateMessageTokens(m, counter), Start: i, End: i + 1})
			i++
			continue
		}
		if role == "assistant" {
			atoms = append(atoms, contextAtom{Kind: kindAssist, Msgs: []oaiMsg{m}, Tokens: estimateMessageTokens(m, counter), Start: i, End: i + 1})
			i++
			continue
		}
		atoms = append(atoms, contextAtom{Kind: kindUser, Msgs: []oaiMsg{m}, Tokens: estimateMessageTokens(m, counter), Start: i, End: i + 1})
		i++
	}
	for idx, a := range atoms {
		if a.Kind == kindUser {
			atoms[idx].Kind = kindAnchor
			break
		}
	}
	return atoms
}

func flattenAtoms(atoms []contextAtom, attachments []chathub.Attachment) (string, []chathub.Attachment) {
	var msgs []oaiMsg
	for _, a := range atoms {
		msgs = append(msgs, a.Msgs...)
	}
	return flattenPromptMessages(msgs, attachments)
}

func flattenPromptMessagesWithBudget(messages []oaiMsg, attachments []chathub.Attachment, budget int) (string, []chathub.Attachment, bool, error) {
	truncatedMsgs, truncated, err := slidingWindow(messages, budget)
	if err != nil {
		return "", attachments, false, err
	}
	prompt, atts := flattenPromptMessages(truncatedMsgs, attachments)
	return prompt, atts, truncated, nil
}

// budget for slidingWindow: B = ContextWindow - MaxOutput - 512
var windowsPathRe = regexp.MustCompile(`\b[A-Za-z]:\\[\\A-Za-z0-9_.\- ~]+`)
var uncPathRe = regexp.MustCompile(`\\\\[A-Za-z0-9_.\-]+\\[\\A-Za-z0-9_.\- ~]+`)
var unixPathRe = regexp.MustCompile(`(?:^|[\s"'(\[])((?:/[A-Za-z0-9_.\-]+){2,})`)

func extractTaskAnchors(messages []oaiMsg) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] || len(out) >= 64 {
			return
		}
		if len(p) > 512 {
			p = p[:512]
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, m := range messages {
		for _, s := range flattenStrings(m) {
			for _, p := range windowsPathRe.FindAllString(s, -1) {
				add(p)
			}
			for _, p := range uncPathRe.FindAllString(s, -1) {
				add(p)
			}
			for _, g := range unixPathRe.FindAllStringSubmatch(s, -1) {
				if len(g) > 1 {
					add(g[1])
				}
			}
		}
	}
	return out
}

func flattenStrings(m oaiMsg) []string {
	var out []string
	appendWalk := func(v any) {}
	appendWalk = func(v any) {
		switch t := v.(type) {
		case string:
			out = append(out, t)
		case []any:
			for _, x := range t {
				appendWalk(x)
			}
		case map[string]any:
			for _, x := range t {
				appendWalk(x)
			}
		}
	}
	appendWalk(m.Content)
	for _, c := range m.ToolCalls {
		appendWalk(c)
	}
	return out
}

func taskAnchorBlock(messages []oaiMsg) string {
	anchors := extractTaskAnchors(messages)
	if len(anchors) == 0 {
		return ""
	}
	return "\n[task anchors — file paths referenced in this conversation, preserved for tool use; do not lose these]\n" + strings.Join(anchors, "\n") + "\n"
}

func slidingWindow(messages []oaiMsg, budget int) ([]oaiMsg, bool, error) {
	if budget <= 0 {
		budget = 1024
	}
	atoms := buildAtomsFast(messages)
	if len(atoms) == 0 {
		return messages, false, nil
	}
	total := 0
	for _, a := range atoms {
		total += a.Tokens
	}
	total += requestProtocolTokens + replyPrimingTokens
	if total <= budget {
		return messages, false, nil
	}
	var p0Indices []int
	anchorIdx := -1
	for idx, a := range atoms {
		if a.Kind == kindSystem {
			p0Indices = append(p0Indices, idx)
		}
		if a.Kind == kindAnchor && anchorIdx == -1 {
			anchorIdx = idx
		}
	}
	var p1Indices []int
	for idx := len(atoms) - 1; idx >= 0; idx-- {
		if atoms[idx].Kind == kindTool {
			p1Indices = append([]int{idx}, p1Indices...)
		} else {
			if len(p1Indices) > 0 {
				break
			}
			break
		}
	}
	sumP0P1 := requestProtocolTokens + replyPrimingTokens
	for _, idx := range p0Indices {
		sumP0P1 += atoms[idx].Tokens
	}
	for _, idx := range p1Indices {
		sumP0P1 += atoms[idx].Tokens
	}
	if anchorIdx != -1 {
		sumP0P1 += atoms[anchorIdx].Tokens
	}
	if sumP0P1 > budget {
		return nil, false, fmt.Errorf("context_length_exceeded: pinned context (system+current task+anchor) %d tokens exceed budget %d; reduce tool results or start a new session", sumP0P1, budget)
	}
	remaining := budget - sumP0P1
	selected := make(map[int]bool)
	for _, idx := range p0Indices {
		selected[idx] = true
	}
	for _, idx := range p1Indices {
		selected[idx] = true
	}
	if anchorIdx != -1 {
		selected[anchorIdx] = true
	}
	for idx := len(atoms) - 1; idx >= 0; idx-- {
		if selected[idx] {
			continue
		}
		tok := atoms[idx].Tokens
		if tok <= remaining {
			selected[idx] = true
			remaining -= tok
		}
	}
	var out []oaiMsg
	omitted := 0
	for idx, a := range atoms {
		if selected[idx] {
			if omitted > 0 {
				out = append(out, oaiMsg{Role: "context-notice", Content: fmt.Sprintf("[%d earlier conversation turn(s) omitted to fit the context budget]", omitted)})
				omitted = 0
			}
			out = append(out, a.Msgs...)
		} else {
			omitted++
		}
	}
	if omitted > 0 && len(out) > 0 {
		out = append(out, oaiMsg{Role: "context-notice", Content: fmt.Sprintf("[%d earliest conversation turn(s) omitted to fit the context budget]", omitted)})
	}
	truncated := len(selected) < len(atoms)
	if len(out) == 0 && len(atoms) > 0 {
		last := atoms[len(atoms)-1]
		out = append(out, last.Msgs...)
		truncated = true
	}
	if truncated && len(out) > 0 {
		notice := oaiMsg{Role: "context-notice", Content: "[context notice] Some earlier turns were omitted to fit the context budget. Historical tool calls and their results in the history are reference markers only — never repeat or execute a historical tool call. Use the current turn for any action."}
		out = append([]oaiMsg{notice}, out...)
	}
	return out, truncated, nil
}

func toolDefinitionTokens(tools []chathub.Tool) int {
	total := 0
	for _, t := range tools {
		total += estimateBudgetTokens(string(t.Function))
	}
	return total * 2
}

func slidingWindowWithTools(messages []oaiMsg, tools []chathub.Tool, budget int) ([]oaiMsg, bool, error) {
	toolTokens := toolDefinitionTokens(tools)
	if toolTokens > 0 && budget-toolTokens < 1024 {
		return nil, false, fmt.Errorf("tools_exceed_context: tool definitions need ~%d tokens, leaving %d of %d; reduce tool count or descriptions", toolTokens, budget-toolTokens, budget)
	}
	effective := budget - toolTokens
	anchorBlock := taskAnchorBlock(messages)
	if anchorBlock != "" {
		if len(anchorBlock) > 4096 {
			anchorBlock = anchorBlock[:4096]
		}
		anchorTokens := estimateBudgetTokens(anchorBlock)
		if effective-anchorTokens < 512 {
			anchorBlock = ""
		} else {
			effective -= anchorTokens
		}
	}
	msgs, truncated, err := slidingWindow(messages, effective)
	if err != nil {
		return nil, false, err
	}
	if truncated && anchorBlock != "" {
		msgs = append(msgs, oaiMsg{Role: "context-notice", Content: strings.TrimSpace(anchorBlock)})
	}
	return msgs, truncated, nil
}
