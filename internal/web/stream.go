package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"m365-native/internal/chathub"
)

func (s *Server) chatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body chatBody
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(firstNonEmpty(body.Message, body.Prompt))
	if text == "" {
		http.Error(w, "message required", http.StatusBadRequest)
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
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if acc.OID == "" || acc.TID == "" {
		if o, t := extractOIDTID(acc.AccessToken); o != "" {
			acc.OID, acc.TID = o, t
		}
	}
	if acc.OID == "" || acc.TID == "" {
		http.Error(w, "account missing oid/tid", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	res, err := s.chat.Chat(ctx, chathub.Account{AccessToken: acc.AccessToken, OID: acc.OID, TID: acc.TID}, chathub.Request{
		Text: text, Tone: body.Tone, ConversationID: body.ConversationID, SessionID: body.SessionID, Attachments: body.Attachments,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if body.SessionKey != "" {
		s.sessions.upsert(conversation{ID: body.SessionKey, AccountID: acc.ID, ConversationID: res.ConversationID, SessionID: res.SessionID, Title: text})
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", http.StatusInternalServerError)
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
		writeSSE(w, "event", payload)
		flusher.Flush()
	}
	for i, event := range chathub.SemanticEvents(res.Events) {
		writeSSE(w, "semantic", map[string]any{"index": i, "type": "m365.semantic", "event": event})
		flusher.Flush()
	}
	writeSSE(w, "done", map[string]any{
		"type": "done", "text": res.Text,
		"conversationId": res.ConversationID, "sessionId": res.SessionID, "requestId": res.RequestID,
		"throttling": res.Throttling,
	})
	flusher.Flush()
}

func writeSSE(w http.ResponseWriter, name string, value any) {
	b, _ := json.Marshal(value)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
}
