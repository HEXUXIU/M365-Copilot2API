package web

import (
	"fmt"
	"strings"

	"m365-copilot2api/internal/chathub"
)

const maxRouterInstructionsBytes = 16 << 10

// routerConversation keeps tool planning on the same upstream conversation as
// the public tool history. When reuse is disabled, accept returns the transient
// conversation ID so the caller can remove it.
type routerConversation struct {
	reuse          bool
	conversationID string
	sessionID      string
}

func newRouterConversation(reuse bool, body *oaiReq) *routerConversation {
	state := &routerConversation{reuse: reuse}
	if reuse && body != nil {
		state.conversationID = body.ConversationID
		state.sessionID = body.SessionID
	}
	return state
}

func (state *routerConversation) apply(req *chathub.Request) {
	if state == nil || !state.reuse || req == nil {
		return
	}
	req.ConversationID = state.conversationID
	req.SessionID = state.sessionID
}

func (state *routerConversation) accept(res *chathub.Result) string {
	if state == nil || res == nil {
		return ""
	}
	if !state.reuse {
		return res.ConversationID
	}
	state.conversationID = firstNonEmpty(res.ConversationID, state.conversationID)
	state.sessionID = firstNonEmpty(res.SessionID, state.sessionID)
	res.ConversationID = state.conversationID
	res.SessionID = state.sessionID
	return ""
}

func (state *routerConversation) adopt(body *oaiReq) {
	if state == nil || !state.reuse || body == nil {
		return
	}
	body.ConversationID = firstNonEmpty(state.conversationID, body.ConversationID)
	body.SessionID = firstNonEmpty(state.sessionID, body.SessionID)
}

func (state *routerConversation) active() bool {
	return state != nil && state.reuse && state.conversationID != ""
}

func (state *routerConversation) reset() {
	if state == nil {
		return
	}
	state.conversationID = ""
	state.sessionID = ""
}

func routerPlanningInstructions(messages []oaiMsg) string {
	var b strings.Builder
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "system" && role != "developer" {
			continue
		}
		text, _ := parseContent(message.Content)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "[%s]\n%s\n", role, text)
	}
	return compactToolResult(b.String(), maxRouterInstructionsBytes)
}

func prependRouterPlanningInstructions(prompt string, messages []oaiMsg) string {
	instructions := routerPlanningInstructions(messages)
	if instructions == "" {
		return prompt
	}
	if strings.TrimSpace(prompt) == "" {
		return "[request instructions]\n" + instructions
	}
	return "[request instructions]\n" + instructions + "\n\n" + prompt
}

func routerPlanningInput(fullPrompt string, fullAttachments []chathub.Attachment, explicitAttachments []chathub.Attachment, messages []oaiMsg, affinity *affinityRequest, reuse bool) (string, []chathub.Attachment) {
	if reuse && affinity != nil && affinity.enforced && affinity.incremental && affinity.prefixCount > 0 && affinity.prefixCount < len(messages) {
		prompt, attachments := flattenPromptMessages(messages[affinity.prefixCount:], explicitAttachments)
		if prompt != "" {
			return prependRouterPlanningInstructions(compactToolResult(prompt, maxRouterPromptBytes), messages), attachments
		}
	}
	// Tool selection needs the bounded request instructions, active user turn,
	// and tool evidence, not an unbounded replay of previous conversation turns.
	prompt, attachments := flattenPromptMessages(activeMessages(messages), explicitAttachments)
	if strings.TrimSpace(prompt) == "" {
		return prependRouterPlanningInstructions(compactToolResult(fullPrompt, maxRouterPromptBytes), messages), fullAttachments
	}
	return prependRouterPlanningInstructions(compactToolResult(prompt, maxRouterPromptBytes), messages), attachments
}
