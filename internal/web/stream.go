package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"m365-copilot2api/internal/chathub"
)

func (s *Server) chatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	var body chatBody
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad json")
		return
	}
	text := strings.TrimSpace(firstNonEmpty(body.Message, body.Prompt))
	if text == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "message required")
		return
	}
	if body.SessionKey != "" {
		if v, ok := s.sessions.get(body.SessionKey); ok {
			body.AccountID = firstNonEmpty(body.AccountID, v.AccountID)
			body.ConversationID = firstNonEmpty(body.ConversationID, v.ConversationID)
			body.SessionID = firstNonEmpty(body.SessionID, v.SessionID)
		}
	}
	acc, err := s.resolveAccount(body.AccountID)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	if acc.OID == "" || acc.TID == "" {
		if o, t := extractOIDTID(acc.AccessToken); o != "" {
			acc.OID, acc.TID = o, t
		}
	}
	if acc.OID == "" || acc.TID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "account_error", "account missing oid/tid")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	res, err := s.chatWithAccount(ctx, acc.ID, chathub.Account{AccessToken: acc.AccessToken, OID: acc.OID, TID: acc.TID}, chathub.Request{
		Text: text, Tone: body.Tone, ConversationID: body.ConversationID, SessionID: body.SessionID, Attachments: body.Attachments,
	})
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", upstreamError(err))
		return
	}
	if body.SessionKey != "" {
		s.sessions.upsert(conversation{ID: body.SessionKey, AccountID: acc.ID, ConversationID: res.ConversationID, SessionID: res.SessionID, Title: text})
	}
	res.Text = sanitizePublicAssistantText(res.Text)
	res.Reasoning = sanitizePublicReasoningText(res.Reasoning)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "stream unsupported")
		return
	}
	for i, event := range res.Normalized {
		payload := map[string]any{
			"index":          i,
			"type":           "chathub.event",
			"event":          event,
			"conversationId": res.ConversationID,
			"sessionId":      res.SessionID,
			"requestId":      res.RequestID,
		}
		if err := writeSSE(r, w, flusher, "event", payload); err != nil {
			return
		}
	}
	for i, event := range chathub.SemanticEvents(res.Events) {
		if err := writeSSE(r, w, flusher, "semantic", map[string]any{"index": i, "type": "m365.semantic", "event": event}); err != nil {
			return
		}
	}
	if err := writeSSE(r, w, flusher, "done", map[string]any{
		"type": "done", "text": res.Text,
		"conversationId": res.ConversationID, "sessionId": res.SessionID, "requestId": res.RequestID,
		"throttling": res.Throttling,
	}); err != nil {
		return
	}
}

// writeSSE emits one SSE frame, returning when the client has disconnected
// (request context canceled) or the write fails so the handler can abort
// instead of blocking a goroutine against a dead socket.
func writeSSE(r *http.Request, w http.ResponseWriter, f http.Flusher, name string, value any) error {
	if err := r.Context().Err(); err != nil {
		return err
	}
	b, _ := json.Marshal(value)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b); err != nil {
		return err
	}
	if f != nil {
		f.Flush()
	}
	return nil
}
