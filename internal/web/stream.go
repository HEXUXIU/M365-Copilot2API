package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
	defer cancel()
	streamSettings := s.settings.get()
	res, err := s.chatWithAccount(ctx, acc.ID, chathub.Account{AccessToken: acc.AccessToken, OID: acc.OID, TID: acc.TID}, chathub.Request{
		Text: text, Tone: body.Tone, ConversationID: body.ConversationID, SessionID: body.SessionID, Attachments: body.Attachments,
		LicenseType: streamSettings.LicenseType, Scenario: streamSettings.Scenario,
		ConversationSignature: body.ConversationSignature, PreviousMessages: body.PreviousMessages, ConnectedFederatedIDs: body.ConnectedFederatedIDs,
		FeatureFlags: s.featureFlags(),
	})
	if err != nil {
		if errors.Is(err, chathub.ErrImageLimit) && s.accountPool != nil {
			s.accountPool.MarkImageLimited(acc.ID)
		}
		s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
		writeUpstreamError(w, err)
		return
	}
	s.accountPool.MarkSuccess(acc.ID)
	s.applyResultMetering(acc.ID, res)
	if body.SessionKey != "" {
		s.sessions.upsert(conversation{ID: body.SessionKey, AccountID: acc.ID, ConversationID: res.ConversationID, SessionID: res.SessionID, Title: text})
	}
	res.Text = sanitizePublicAssistantText(res.Text)
	res.Text, _ = chathub.StripCitationMarkers(res.Text, res.References)
	res.Reasoning = sanitizePublicReasoningText(res.Reasoning)

	if res.Throttling != nil {
		if b, err := json.Marshal(res.Throttling); err == nil {
			w.Header().Set("X-M365-Throttling", string(b))
		}
	}
	if len(res.Scores) > 0 {
		if b, err := json.Marshal(res.Scores); err == nil {
			w.Header().Set("X-M365-Scores", string(b))
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "stream unsupported")
		return
	}
	sw := newSSEWriter(w, flusher)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	keepaliveDone := make(chan struct{})
	defer close(keepaliveDone)
	go func() {
		for {
			select {
			case <-keepaliveDone:
				return
			case <-r.Context().Done():
				return
			case <-ticker.C:
				_ = sw.raw(": keepalive\n\n")
			}
		}
	}()
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
		"throttling": res.Throttling, "suggestedResponses": res.SuggestedResponses,
		"offense": res.Offense, "scores": res.Scores, "conversationTransferToken": res.ConversationTransferToken,
		"meteringInformation": res.MeteringInformation, "spokenText": res.SpokenText,
		"storageMessageId": res.StorageMessageID,
		"timestamps":       res.Timestamps,
	}); err != nil {
		return
	}
	if res.Timestamps.RequestSent != "" {
		_ = sw.raw(": m365-metrics " + mustJSON(res.Timestamps) + "\n\n")
	}
}

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

type meteringInfoItem struct {
	MeterError string `json:"meterError"`
	HasAccess  bool   `json:"hasAccess"`
}

type throttlingMeteringEntry struct {
	RemainingAllowance int `json:"remainingAllowance"`
}

func ParseMetering(accountID string, items json.RawMessage) (meterError string, hasAccess bool) {
	hasAccess = true
	if len(items) == 0 {
		return "", hasAccess
	}
	var parsed []meteringInfoItem
	if json.Unmarshal(items, &parsed) != nil {
		return "", hasAccess
	}
	for _, mi := range parsed {
		if mi.MeterError != "" {
			meterError = mi.MeterError
		}
		hasAccess = mi.HasAccess
	}
	if meterError != "" {
		log.Printf("[metering] account=%s meterError=%q hasAccess=%v", accountID, meterError, hasAccess)
	}
	return meterError, hasAccess
}

func remainingAllowances(throttling any) map[string]int {
	remaining := map[string]int{}
	if throttling == nil {
		return remaining
	}
	b, err := json.Marshal(throttling)
	if err != nil {
		return remaining
	}
	var thr struct {
		Metering map[string]throttlingMeteringEntry `json:"metering"`
	}
	if json.Unmarshal(b, &thr) != nil {
		return remaining
	}
	for k, v := range thr.Metering {
		remaining[k] = v.RemainingAllowance
	}
	return remaining
}

func applyMeteringCooldown(pool *accountHealth, accountID string, meterError string) {
	if pool == nil || accountID == "" || meterError == "" {
		return
	}
	switch meterError {
	case "ImageGenInsufficientTokensThrottled":
		pool.MarkImageGenTokensThrottled(accountID)
		log.Printf("[metering] account=%s imageGenCooldownUntil=next_midnight_utc", accountID)
	case "ImageGenSystemCapacityThrottled":
		pool.MarkImageGenSystemThrottled(accountID)
		log.Printf("[metering] account=%s imageGenSystemCooldown=30m", accountID)
	}
}

// applyResultMetering 统一处理一轮聊天结果的 throttling/metering：
// 更新配额计数、触发图片能力冷却、记录剩余额度趋势。主路径与 stream 路径共用，
// 避免只有 /api/chat/stream 生效而 /v1/chat/completions 漏掉风控信号。
func (s *Server) applyResultMetering(accountID string, res chathub.Result) {
	if s.accountPool == nil {
		return
	}
	if res.Throttling != nil {
		s.accountPool.UpdateThrottling(accountID, res.Throttling)
		s.logThrottlingWarning(accountID, res.Throttling)
	}
	if res.MeteringInformation == nil {
		return
	}
	miRaw, err := json.Marshal(res.MeteringInformation)
	if err != nil {
		return
	}
	mErr, hasAccess := ParseMetering(accountID, json.RawMessage(miRaw))
	applyMeteringCooldown(s.accountPool, accountID, mErr)
	if remaining := remainingAllowances(res.Throttling); len(remaining) > 0 {
		log.Printf("[metering] account=%s remainingAllowance=%v", accountID, remaining)
	}
	if mErr != "" || !hasAccess {
		log.Printf("[metering] account=%s hasAccess=%v meterError=%q", accountID, hasAccess, mErr)
	}
}
