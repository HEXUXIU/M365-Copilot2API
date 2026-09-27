package web

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// responseNamespace builds the dual isolation key tenant\x00session so a
// tenant can never read another tenant's response, and even within the same
// tenant two explicit sessions (X-M365-Session-Id) cannot cross-read. The
// scheme matches session_resolver.explicitKey and userSessionStore.userKey.
func responseNamespace(tenant, sessionID string) string { return tenant + "\x00" + sessionID }

func responseSessionID(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get(sessionHeaderName))
}

func tenantHashPrefix(tenant string) string {
	if len(tenant) >= 8 {
		return tenant[:8]
	}
	return tenant
}

func extractResponsesToolOutputIDs(input any) []string {
	arr, ok := input.([]any)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(arr))
	for _, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		if typ != "function_call_output" && typ != "custom_tool_call_output" {
			continue
		}
		if id, _ := m["call_id"].(string); strings.TrimSpace(id) != "" {
			ids = append(ids, strings.TrimSpace(id))
		}
	}
	return ids
}

func buildRespToolCallsMap(toolCalls []map[string]any) map[string]*ToolCallRecord {
	if len(toolCalls) == 0 {
		return map[string]*ToolCallRecord{}
	}
	m := make(map[string]*ToolCallRecord, len(toolCalls))
	for _, tc := range toolCalls {
		id, _ := tc["id"].(string)
		if id == "" {
			continue
		}
		fn, _ := tc["function"].(map[string]any)
		name, _ := fn["name"].(string)
		args, _ := fn["arguments"].(string)
		typ, _ := tc["type"].(string)
		if typ == "" {
			typ = "function"
		}
		m[id] = &ToolCallRecord{CallID: id, Name: name, Arguments: args, Type: typ}
	}
	return m
}

func sessionHashPrefix(s string) string {
	if s == "" {
		return "-"
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:8]
}

type pipeResponseWriter struct {
	h      http.Header
	w      *io.PipeWriter
	status int
}

func (p *pipeResponseWriter) Header() http.Header { return p.h }
func (p *pipeResponseWriter) WriteHeader(n int) {
	if p.status == 0 {
		p.status = n
	}
}
func (p *pipeResponseWriter) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.status = 200
	}
	return p.w.Write(b)
}
func (p *pipeResponseWriter) Flush() {}

// streamResponsesAdapter converts the internal OpenAI SSE incrementally instead
// of buffering the entire completion in httptest.ResponseRecorder.
func (s *Server) streamResponsesAdapter(w http.ResponseWriter, r *http.Request, o oaiReq, model string) {
	o.Stream = true
	b, _ := json.Marshal(o)
	r2 := r.Clone(r.Context())
	r2.Method = http.MethodPost
	r2.Body = io.NopCloser(bytes.NewReader(b))
	r2.ContentLength = int64(len(b))
	pr, pw := io.Pipe()
	irw := &pipeResponseWriter{h: make(http.Header), w: pw}
	innerDone := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[responses] inner goroutine panic: %v", r)
			}
			_ = pw.Close()
			close(innerDone)
		}()
		s.openaiChat(irw, r2)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	emit := func(name string, v any) error {
		return writeSSE(r, w, flusher, name, v)
	}
	id := "resp_" + uuid.NewString()
	created := time.Now().Unix()
	emit("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "status": "in_progress", "model": model, "output": []any{}}})

	var text strings.Builder
	messageID := "msg_" + uuid.NewString()
	contentID := "txt_" + uuid.NewString()
	textStarted := false
	type tcState struct {
		ID, Name, Args, Type string
		ItemID               string
	}
	calls := map[int]*tcState{}
	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		if r.Context().Err() != nil {
			return
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk) != nil {
			continue
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok && content != "" {
			text.WriteString(content)
			if !textStarted {
				textStarted = true
				emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": messageID, "role": "assistant", "status": "in_progress", "content": []any{map[string]any{"type": "output_text", "id": contentID, "text": "", "annotations": []any{}}}}})
			}
			emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": messageID, "delta": content})
		}
		if rawCalls, ok := delta["tool_calls"].([]any); ok {
			for _, raw := range rawCalls {
				tc, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				idxFloat, ok := tc["index"].(float64)
				if !ok {
					continue
				}
				idx := int(idxFloat)
				st := calls[idx]
				typ := "function"
				if v, ok := tc["type"].(string); ok && v == "custom" {
					typ = "custom"
				}
				if st == nil {
					prefix := "fc_"
					item := map[string]any{"type": "function_call", "call_id": "", "name": "", "arguments": "", "status": "in_progress"}
					if typ == "custom" {
						prefix = "ctc_"
						item = map[string]any{"type": "custom_tool_call", "call_id": "", "name": "", "input": "", "status": "in_progress"}
					}
					st = &tcState{ItemID: prefix + uuid.NewString(), Type: typ}
					calls[idx] = st
					item["id"] = st.ItemID
					emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": idx, "item": item})
				}
				if v, ok := tc["id"].(string); ok {
					st.ID = v
				}
				fn, _ := tc["function"].(map[string]any)
				if v, ok := fn["name"].(string); ok {
					st.Name += v
				}
				if v, ok := fn["arguments"].(string); ok {
					st.Args += v
					if st.Type != "custom" {
						emit("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "output_index": idx, "item_id": st.ItemID, "delta": v})
					}
				}
			}
		}
	}
	<-innerDone
	if scanner.Err() != nil || irw.status >= http.StatusBadRequest {
		status := irw.status
		if status == 0 {
			status = http.StatusBadGateway
		}
		emit("response.failed", map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"id": id, "object": "response", "status": "failed", "model": model,
				"error": map[string]any{"code": status, "message": "inner chat request failed"},
			},
		})
		return
	}
	if len(calls) == 0 && strings.TrimSpace(text.String()) == "" {
		// Never leave a Responses stream after response.created without a
		// terminal event: clients otherwise render this as a successful blank
		// answer and may reuse an incomplete response on the next turn.
		emit("response.failed", map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"id": id, "object": "response", "status": "failed", "model": model,
				"error": map[string]any{"code": "empty_upstream_response", "message": "ChatHub returned no text or tool call"},
			},
		})
		return
	}
	output := []any{}
	if len(calls) > 0 {
		keys := make([]int, 0, len(calls))
		for k := range calls {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, i := range keys {
			st := calls[i]
			if st == nil {
				continue
			}
			if st.Type == "custom" {
				input := customToolInput(st.Args)
				item := map[string]any{"type": "custom_tool_call", "id": st.ItemID, "call_id": st.ID, "name": st.Name, "input": input, "status": "completed"}
				output = append(output, item)
				emit("response.custom_tool_call_input.delta", map[string]any{"type": "response.custom_tool_call_input.delta", "output_index": i, "item_id": item["id"], "delta": input})
				emit("response.custom_tool_call_input.done", map[string]any{"type": "response.custom_tool_call_input.done", "output_index": i, "item_id": item["id"], "input": input})
				emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": i, "item": item})
				continue
			}
			item := map[string]any{"type": "function_call", "id": st.ItemID, "call_id": st.ID, "name": st.Name, "arguments": st.Args, "status": "completed"}
			output = append(output, item)
			emit("response.function_call_arguments.done", map[string]any{"type": "response.function_call_arguments.done", "output_index": i, "item_id": st.ItemID, "arguments": st.Args})
			emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": i, "item": item})
		}
	} else {
		item := map[string]any{"type": "message", "id": messageID, "role": "assistant", "status": "in_progress", "content": []any{map[string]any{"type": "output_text", "id": contentID, "text": "", "annotations": []any{}}}}
		output = append(output, item)
		if !textStarted {
			emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
			emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": messageID, "delta": text.String()})
		}
		emit("response.output_text.done", map[string]any{"type": "response.output_text.done", "output_index": 0, "content_index": 0, "item_id": messageID, "text": text.String()})
		item["status"] = "completed"
		item["content"] = []any{map[string]any{"type": "output_text", "id": contentID, "text": text.String(), "annotations": []any{}}}
		emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
	}
	usageOutput := text.String()
	for _, call := range calls {
		usageOutput += call.Name + call.Args
	}
	estimate := estimateResponsesUsage(model, o.Messages, o.Tools, o.ToolChoice, usageOutput)
	resp := map[string]any{"id": id, "object": "response", "created_at": created, "status": "completed", "model": model, "output": output, "usage": estimate.Values, "m365": localUsageMetadata(estimate.Source)}
	emit("response.completed", map[string]any{"type": "response.completed", "response": resp})
}

func (s *Server) runOpenAIAdapter(r *http.Request, o oaiReq) (map[string]any, []byte, int, error) {
	o.Stream = false
	b, _ := json.Marshal(o)
	r2 := r.Clone(r.Context())
	r2.Method = http.MethodPost
	r2.Body = io.NopCloser(bytes.NewReader(b))
	r2.ContentLength = int64(len(b))
	rr := httptest.NewRecorder()
	s.openaiChat(rr, r2)
	var out map[string]any
	err := json.Unmarshal(rr.Body.Bytes(), &out)
	return out, rr.Body.Bytes(), rr.Code, err
}

func (s *Server) responses(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	if r.Method != http.MethodPost {
		writeResponsesError(w, 405, "invalid_request_error", "method not allowed")
		return
	}
	var body responsesRequest
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeResponsesError(w, 400, "invalid_request_error", "bad json")
		return
	}
	o, err := body.openAI()
	if err != nil {
		writeResponsesError(w, 400, "invalid_request_error", err.Error())
		return
	}
	// Dual isolation: tenant\x00session so two keys never share history and
	// within one tenant two explicit sessions (X-M365-Session-Id) cannot
	// cross-read. Falls back to 8-char prefix display only for legacy callers
	// without a full-key tenant, but the bucket key is always
	// responseNamespace(tenant, sessionID).
	tenant := tenantFromRequest(r)
	if tenant == "" {
		if prefix := extractAPIKey(r); prefix != "" {
			h := sha256.Sum256([]byte(prefix))
			tenant = hex.EncodeToString(h[:])
		} else {
			tenant = "anonymous"
		}
	}
	sessionID := responseSessionID(r)
	nsKey := responseNamespace(tenant, sessionID)
	if body.PreviousResponseID != "" {
		toolIDs := extractResponsesToolOutputIDs(body.Input)
		s.responseMu.Lock()
		bucket := s.responseMessages[nsKey]
		prior, ok := bucket[body.PreviousResponseID]
		if !ok || len(prior.Messages) == 0 {
			s.responseMu.Unlock()
			writeResponsesError(w, 400, "invalid_request_error", "unknown previous_response_id")
			return
		}
		if prior.Tenant != "" && prior.Tenant != tenant {
			s.responseMu.Unlock()
			writeResponsesError(w, 400, "invalid_request_error", "previous_response_id tenant mismatch")
			return
		}
		if prior.SessionID != sessionID {
			s.responseMu.Unlock()
			writeResponsesError(w, 400, "invalid_request_error", "previous_response_id session mismatch")
			return
		}
		if prior.Consumed {
			dupVersion := prior.Version
			s.responseMu.Unlock()
			log.Printf("[responses-audit] tenantHash=%s session=%s previous=%s action=rejected_consumed version=%d tool_ids=%v", tenantHashPrefix(tenant), sessionHashPrefix(sessionID), body.PreviousResponseID, dupVersion, toolIDs)
			if s.debug != nil {
				s.debug.add(debugRecord{ID: "resp_" + uuid.NewString(), At: time.Now(), Path: "/v1/responses", Method: "POST", Status: 409, Level: "warn", Gateway: map[string]any{"previous_response_id": body.PreviousResponseID, "tenantHash": tenantHashPrefix(tenant), "session": sessionHashPrefix(sessionID), "tool_ids": toolIDs, "version": dupVersion, "action": "rejected_consumed"}})
			}
			writeResponsesError(w, 409, "conflict", "previous_response_id already consumed")
			return
		}
		if len(toolIDs) > 0 {
			if len(prior.ToolCalls) == 0 {
				s.responseMu.Unlock()
				writeResponsesError(w, 400, "invalid_request_error", "previous_response_id has no pending tool calls")
				return
			}
			seen := make(map[string]bool, len(toolIDs))
			for _, id := range toolIDs {
				if seen[id] {
					s.responseMu.Unlock()
					writeResponsesError(w, 400, "invalid_request_error", "duplicate call_id: "+id)
					return
				}
				seen[id] = true
				if _, ok := prior.ToolCalls[id]; !ok {
					s.responseMu.Unlock()
					writeResponsesError(w, 400, "invalid_request_error", "call_id not in parent pending set: "+id)
					return
				}
			}
		} else if len(prior.ToolCalls) > 0 {
			s.responseMu.Unlock()
			writeResponsesError(w, 400, "invalid_request_error", "previous_response_id expects tool outputs for pending calls")
			return
		}
		prior.Version++
		prior.Consumed = true
		messages := append([]oaiMsg(nil), prior.Messages...)
		newVersion := prior.Version
		parentToolCount := len(prior.ToolCalls)
		s.responseMu.Unlock()
		log.Printf("[responses-audit] tenantHash=%s session=%s previous=%s action=consumed version=%d tool_ids=%v parentToolCalls=%d", tenantHashPrefix(tenant), sessionHashPrefix(sessionID), body.PreviousResponseID, newVersion, toolIDs, parentToolCount)
		if s.debug != nil {
			s.debug.add(debugRecord{ID: "resp_" + uuid.NewString(), At: time.Now(), Path: "/v1/responses", Method: "POST", Status: 200, Level: "info", Gateway: map[string]any{"previous_response_id": body.PreviousResponseID, "tenantHash": tenantHashPrefix(tenant), "session": sessionHashPrefix(sessionID), "tool_ids": toolIDs, "version": newVersion, "parentToolCalls": parentToolCount, "action": "consumed"}})
		}
		o.Messages = append(messages, o.Messages...)
	}
	if body.Stream {
		s.streamResponsesAdapter(w, r, o, firstNonEmpty(body.Model, "m365-copilot"))
		return
	}
	out, raw, status, err := s.runOpenAIAdapter(r, o)
	if status >= 400 {
		writeResponsesError(w, status, "upstream_error", errorMessage(raw, "upstream protocol error"))
		return
	}
	if err != nil {
		writeResponsesError(w, http.StatusBadGateway, "upstream_error", "upstream protocol error: "+err.Error())
		return
	}
	if !responsesOutputHasContent(out) {
		writeResponsesError(w, http.StatusBadGateway, "upstream_error", "ChatHub returned an empty response; no reusable message was created")
		return
	}
	msg, _ := openAIChoice(out)
	outputForUsage := ""
	if msg != nil {
		outputForUsage = fmt.Sprint(msg["content"])
		if calls, ok := msg["tool_calls"].([]any); ok {
			outputForUsage += fmt.Sprint(calls)
		}
	}
	estimate := estimateResponsesUsage(firstNonEmpty(body.Model, "m365-copilot"), o.Messages, o.Tools, o.ToolChoice, outputForUsage)
	out["usage"] = estimate.Values
	out["m365_usage_source"] = estimate.Source
	s.usage.record(UsageRecord{
		Time:         time.Now(),
		APIKeyPrefix: extractAPIKey(r),
		Model:        firstNonEmpty(body.Model, "m365-copilot"),
		Endpoint:     "/v1/responses",
		InputTokens:  int64(estimate.Values["input_tokens"].(int)),
		OutputTokens: int64(estimate.Values["output_tokens"].(int)),
		DurationMs:   time.Since(startedAt).Milliseconds(),
		Status:       200,
	})
	// Retain the normalized history so a subsequent previous_response_id can
	// validate its function_call_output against the original tool call.
	if _, ok := out["id"].(string); ok {
		publicID := "resp_" + uuid.NewString()
		out["m365_response_id"] = publicID
		stored := append([]oaiMsg(nil), o.Messages...)
		var storedToolCalls []map[string]any
		if msg, _ := openAIChoice(out); msg != nil {
			if calls, ok := msg["tool_calls"].([]any); ok && len(calls) > 0 {
				converted := make([]map[string]any, 0, len(calls))
				for _, call := range calls {
					if m, ok := call.(map[string]any); ok {
						converted = append(converted, m)
					}
				}
				stored = append(stored, oaiMsg{Role: "assistant", ToolCalls: converted})
				storedToolCalls = converted
			} else {
				if text, _ := msg["content"].(string); text != "" {
					stored = append(stored, oaiMsg{Role: "assistant", Content: text})
				}
			}
		}
		toolCallsMap := buildRespToolCallsMap(storedToolCalls)
		s.responseMu.Lock()
		bucket := s.responseMessages[nsKey]
		if bucket == nil {
			bucket = map[string]*RespNode{}
			s.responseMessages[nsKey] = bucket
		}
		for k, h := range bucket {
			if time.Since(h.At) > time.Hour {
				delete(bucket, k)
			}
		}
		if len(bucket) >= maxResponsesPerTenant {
			var oldestKey string
			var oldestAt time.Time
			for k, h := range bucket {
				if oldestKey == "" || h.At.Before(oldestAt) {
					oldestKey, oldestAt = k, h.At
				}
			}
			delete(bucket, oldestKey)
		}
		bucket[publicID] = &RespNode{At: time.Now(), Messages: stored, ToolCalls: toolCallsMap, Version: 1, Consumed: false, ParentID: body.PreviousResponseID, Tenant: tenant, SessionID: sessionID}
		s.responseMu.Unlock()
		log.Printf("[responses-audit] tenantHash=%s session=%s new=%s parent=%s toolCalls=%d version=1", tenantHashPrefix(tenant), sessionHashPrefix(sessionID), publicID, body.PreviousResponseID, len(toolCallsMap))
	}
	writeResponsesResult(w, firstNonEmpty(body.Model, "m365-copilot"), body.Stream, out)
}

func responsesOutputHasContent(src map[string]any) bool {
	msg, _ := openAIChoice(src)
	if msg == nil {
		return false
	}
	if calls, ok := msg["tool_calls"].([]any); ok && len(calls) > 0 {
		return true
	}
	text, _ := msg["content"].(string)
	return strings.TrimSpace(text) != ""
}

func (s *Server) anthropicMessages(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	if r.Method != http.MethodPost {
		writeAnthropicError(w, 405, "invalid_request_error", "method not allowed")
		return
	}
	var body anthropicRequest
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeAnthropicError(w, 400, "invalid_request_error", "bad json")
		return
	}
	o, err := body.openAI()
	if err != nil {
		writeAnthropicError(w, 400, "invalid_request_error", err.Error())
		return
	}
	out, raw, status, err := s.runOpenAIAdapter(r, o)
	if status >= 400 {
		writeAnthropicError(w, status, "api_error", errorMessage(raw, "upstream protocol error"))
		return
	}
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "upstream protocol error: "+err.Error())
		return
	}
	estimate := estimateResponsesUsage(firstNonEmpty(body.Model, "m365-copilot"), o.Messages, o.Tools, o.ToolChoice, "")
	s.usage.record(UsageRecord{
		Time:         time.Now(),
		APIKeyPrefix: extractAPIKey(r),
		Model:        firstNonEmpty(body.Model, "m365-copilot"),
		Endpoint:     "/v1/messages",
		InputTokens:  int64(estimate.Values["input_tokens"].(int)),
		OutputTokens: int64(estimate.Values["output_tokens"].(int)),
		DurationMs:   time.Since(startedAt).Milliseconds(),
		Status:       200,
	})
	writeAnthropicResult(w, firstNonEmpty(body.Model, "m365-copilot"), body.Stream, out)
}
