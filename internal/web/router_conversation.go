package web

import (
	"strings"

	"m365-copilot2api/internal/chathub"
)

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

func routerPlanningInput(fullPrompt string, fullAttachments []chathub.Attachment, explicitAttachments []chathub.Attachment, messages []oaiMsg, affinity *affinityRequest, reuse bool) (string, []chathub.Attachment) {
	if reuse && affinity != nil && affinity.enforced && affinity.incremental && affinity.prefixCount > 0 && affinity.prefixCount < len(messages) {
		prompt, attachments := flattenPromptMessages(messages[affinity.prefixCount:], explicitAttachments)
		if prompt != "" {
			return compactToolResult(prompt, maxRouterPromptBytes), attachments
		}
	}
	// Tool selection needs the active user turn and its tool evidence, not the
	// system/developer prompt or an unbounded replay of previous turns. Besides
	// reducing latency, this prevents unrelated policy text from changing a
	// straightforward tool decision. Keep head and tail when the active turn
	// itself contains a large document.
	prompt, attachments := flattenPromptMessages(activeMessages(messages), explicitAttachments)
	if strings.TrimSpace(prompt) == "" {
		return compactToolResult(fullPrompt, maxRouterPromptBytes), fullAttachments
	}
	return compactToolResult(prompt, maxRouterPromptBytes), attachments
}
