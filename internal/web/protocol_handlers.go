package web

import (
	"bufio"
	"bytes"
	"context"
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
	"sync"
	"time"

	"github.com/google/uuid"
)

type explicitToolRequiredContextKey struct{}
type responsesAdapterContextKey struct{}

const responsesHeartbeatInterval = 15 * time.Second

func carryExplicitToolRequirement(r *http.Request, required bool) *http.Request {
	if !required {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), explicitToolRequiredContextKey{}, true))
}

func explicitToolRequirementFromContext(ctx context.Context) bool {
	required, _ := ctx.Value(explicitToolRequiredContextKey{}).(bool)
	return required
}

func carryResponsesAdapter(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), responsesAdapterContextKey{}, true))
}

func responsesAdapterFromContext(ctx context.Context) bool {
	enabled, _ := ctx.Value(responsesAdapterContextKey{}).(bool)
	return enabled
}

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

func startSSEHeartbeat(ctx context.Context, sw *sseWriter, interval time.Duration) func() {
	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if err := sw.raw(": keepalive\n\n"); err != nil {
					return
				}
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
		wg.Wait()
	}
}

// streamResponsesAdapter converts the internal OpenAI SSE incrementally instead
// of buffering the entire completion in httptest.ResponseRecorder.
func (s *Server) streamResponsesAdapter(w http.ResponseWriter, r *http.Request, o oaiReq, model, responseID, affinitySessionID, tenant string, beforeCompleted func([]byte) error) bool {
	return s.streamResponsesAdapterWithRunnerAndCompletion(w, r, o, model, responseID, affinitySessionID, tenant, s.openaiChat, beforeCompleted)
}

func (s *Server) streamResponsesAdapterWithRunner(w http.ResponseWriter, r *http.Request, o oaiReq, model, responseID, affinitySessionID, tenant string, run func(http.ResponseWriter, *http.Request)) bool {
	return s.streamResponsesAdapterWithRunnerAndCompletion(w, r, o, model, responseID, affinitySessionID, tenant, run, nil)
}

func (s *Server) streamResponsesAdapterWithRunnerAndCompletion(w http.ResponseWriter, r *http.Request, o oaiReq, model, responseID, affinitySessionID, tenant string, run func(http.ResponseWriter, *http.Request), beforeCompleted func([]byte) error) bool {
	return s.streamResponsesAdapterWithRunnerAndCompletionInterval(w, r, o, model, responseID, affinitySessionID, tenant, run, beforeCompleted, responsesHeartbeatInterval)
}

func (s *Server) streamResponsesAdapterWithRunnerAndCompletionInterval(w http.ResponseWriter, r *http.Request, o oaiReq, model, responseID, affinitySessionID, tenant string, run func(http.ResponseWriter, *http.Request), beforeCompleted func([]byte) error, heartbeatInterval time.Duration) bool {
	o.Stream = true
	b, _ := json.Marshal(o)
	r2 := r.Clone(r.Context())
	r2 = carryExplicitToolRequirement(r2, o.ExplicitToolRequired)
	r2 = carryResponsesAdapter(r2)
	r2.Method = http.MethodPost
	r2.Body = io.NopCloser(bytes.NewReader(b))
	r2.ContentLength = int64(len(b))
	pr, pw := io.Pipe()
	defer pr.Close()
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
		run(irw, r2)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	sw := newSSEWriter(w, flusher)
	sequence := 0
	emit := func(name string, v any) error {
		if event, ok := v.(map[string]any); ok {
			event["sequence_number"] = sequence
			sequence++
		}
		payload, _ := json.Marshal(v)
		return sw.raw("event: " + name + "\ndata: " + string(payload) + "\n\n")
	}
	id := responseID
	created := time.Now().Unix()
	if err := emit("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "created_at": created, "status": "in_progress", "model": model, "output": []any{}}}); err != nil {
		return false
	}
	stopHeartbeat := startSSEHeartbeat(r.Context(), sw, heartbeatInterval)
	defer stopHeartbeat()

	var text strings.Builder
	var textEmitted strings.Builder
	authoritativeText := ""
	hasAuthoritativeText := false
	messageID := "msg_" + uuid.NewString()
	textStarted := false
	textOutputIndex := -1
	var reasoning strings.Builder
	reasoningID := "rs_" + uuid.NewString()
	reasoningStarted := false
	reasoningOutputIndex := -1
	nextOutputIndex := 0
	allocateOutputIndex := func() int {
		index := nextOutputIndex
		nextOutputIndex++
		return index
	}
	ensureTextStarted := func() error {
		if textStarted {
			return nil
		}
		textStarted = true
		textOutputIndex = allocateOutputIndex()
		if err := emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": textOutputIndex, "item": map[string]any{"type": "message", "id": messageID, "role": "assistant", "status": "in_progress", "content": []any{}}}); err != nil {
			return err
		}
		return emit("response.content_part.added", map[string]any{"type": "response.content_part.added", "output_index": textOutputIndex, "content_index": 0, "item_id": messageID, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
	}
	emitTextDelta := func(part string) error {
		if part == "" {
			return nil
		}
		if err := ensureTextStarted(); err != nil {
			return err
		}
		textEmitted.WriteString(part)
		return emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": textOutputIndex, "content_index": 0, "item_id": messageID, "delta": part})
	}
	type tcState struct {
		ID, Name, Args, Type string
		ItemID               string
		OutputIndex          int
		Started              bool
	}
	calls := map[int]*tcState{}
	var innerUsage map[string]any
	innerFinishReason := ""
	innerError := ""
	sawInnerDone := false
	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		if r.Context().Err() != nil {
			return false
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if line == "data: [DONE]" {
			sawInnerDone = true
			continue
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk) != nil {
			continue
		}
		if usage, ok := chunk["usage"].(map[string]any); ok {
			innerUsage = usage
		}
		if finalText, ok := chunk["x_m365_final_text"].(string); ok {
			authoritativeText = finalText
			hasAuthoritativeText = true
		}
		if inner, ok := chunk["error"].(map[string]any); ok {
			innerError = strings.TrimSpace(fmt.Sprint(inner["message"]))
			if innerError == "" {
				innerError = "inner chat stream returned an error"
			}
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		if finish, ok := choice["finish_reason"].(string); ok && finish != "" {
			innerFinishReason = finish
		}
		delta, _ := choice["delta"].(map[string]any)
		if part, ok := delta["reasoning_content"].(string); ok && part != "" {
			if !reasoningStarted {
				reasoningStarted = true
				reasoningOutputIndex = allocateOutputIndex()
				emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": reasoningOutputIndex, "item": map[string]any{"type": "reasoning", "id": reasoningID, "summary": []any{}}})
				emit("response.reasoning_summary_part.added", map[string]any{"type": "response.reasoning_summary_part.added", "output_index": reasoningOutputIndex, "summary_index": 0, "item_id": reasoningID, "part": map[string]any{"type": "summary_text", "text": ""}})
			}
			reasoning.WriteString(part)
			emit("response.reasoning_summary_text.delta", map[string]any{"type": "response.reasoning_summary_text.delta", "output_index": reasoningOutputIndex, "summary_index": 0, "item_id": reasoningID, "delta": part})
		}
		if content, ok := delta["content"].(string); ok && content != "" {
			text.WriteString(content)
			if err := emitTextDelta(content); err != nil {
				return false
			}
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
					if typ == "custom" {
						prefix = "ctc_"
					}
					st = &tcState{ItemID: prefix + uuid.NewString(), Type: typ, OutputIndex: allocateOutputIndex()}
					calls[idx] = st
				}
				if v, ok := tc["id"].(string); ok {
					st.ID = v
				}
				fn, _ := tc["function"].(map[string]any)
				if v, ok := fn["name"].(string); ok {
					st.Name += v
				}
				if !st.Started && strings.TrimSpace(st.ID) != "" && strings.TrimSpace(st.Name) != "" {
					item := map[string]any{"type": "function_call", "id": st.ItemID, "call_id": st.ID, "name": st.Name, "arguments": "", "status": "in_progress"}
					if st.Type == "custom" {
						item = map[string]any{"type": "custom_tool_call", "id": st.ItemID, "call_id": st.ID, "name": st.Name, "input": "", "status": "in_progress"}
					}
					if err := emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": st.OutputIndex, "item": item}); err != nil {
						return false
					}
					st.Started = true
					if st.Type != "custom" && st.Args != "" {
						if err := emit("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "output_index": st.OutputIndex, "item_id": st.ItemID, "delta": st.Args}); err != nil {
							return false
						}
					}
				}
				if v, ok := fn["arguments"].(string); ok && v != "" {
					st.Args += v
					if st.Started && st.Type != "custom" {
						if err := emit("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "output_index": st.OutputIndex, "item_id": st.ItemID, "delta": v}); err != nil {
							return false
						}
					}
				}
			}
		}
	}
	<-innerDone
	if scanner.Err() != nil || irw.status >= http.StatusBadRequest || innerError != "" || !sawInnerDone || innerFinishReason == "" || innerFinishReason == "error" {
		status := irw.status
		if status < http.StatusBadRequest {
			status = http.StatusBadGateway
		}
		message := innerError
		if message == "" {
			switch {
			case scanner.Err() != nil:
				message = "inner chat stream read failed: " + scanner.Err().Error()
			case !sawInnerDone || innerFinishReason == "":
				message = "inner chat stream ended before its terminal event"
			case innerFinishReason == "error":
				message = "inner chat stream finished with an error"
			default:
				message = "inner chat request failed"
			}
		}
		emit("response.failed", map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"id": id, "object": "response", "status": "failed", "model": model,
				"error": map[string]any{"code": status, "message": message},
			},
		})
		return false
	}
	finalText := text.String()
	if hasAuthoritativeText {
		finalText = authoritativeText
	}
	if !strings.HasPrefix(finalText, textEmitted.String()) {
		emit("response.failed", map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"id": id, "object": "response", "status": "failed", "model": model,
				"error": map[string]any{"code": "stream_reconciliation_failed", "message": "upstream revised text that was already streamed"},
			},
		})
		return false
	}
	text.Reset()
	text.WriteString(finalText)
	if err := emitTextDelta(finalText[len(textEmitted.String()):]); err != nil {
		return false
	}
	if len(calls) == 0 && strings.TrimSpace(text.String()) == "" && strings.TrimSpace(reasoning.String()) == "" {
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
		return false
	}
	for _, call := range calls {
		if call == nil || strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" {
			emit("response.failed", map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"id": id, "object": "response", "status": "failed", "model": model,
					"error": map[string]any{"code": "invalid_tool_call", "message": "upstream tool call is missing call_id or name"},
				},
			})
			return false
		}
	}
	outputByIndex := map[int]any{}
	if reasoningStarted {
		reasoningText := reasoning.String()
		summary := map[string]any{"type": "summary_text", "text": reasoningText}
		item := map[string]any{"type": "reasoning", "id": reasoningID, "summary": []any{summary}}
		emit("response.reasoning_summary_text.done", map[string]any{"type": "response.reasoning_summary_text.done", "output_index": reasoningOutputIndex, "summary_index": 0, "item_id": reasoningID, "text": reasoningText})
		emit("response.reasoning_summary_part.done", map[string]any{"type": "response.reasoning_summary_part.done", "output_index": reasoningOutputIndex, "summary_index": 0, "item_id": reasoningID, "part": summary})
		emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": reasoningOutputIndex, "item": item})
		outputByIndex[reasoningOutputIndex] = item
	}
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
				outputByIndex[st.OutputIndex] = item
				emit("response.custom_tool_call_input.delta", map[string]any{"type": "response.custom_tool_call_input.delta", "output_index": st.OutputIndex, "item_id": item["id"], "delta": input})
				emit("response.custom_tool_call_input.done", map[string]any{"type": "response.custom_tool_call_input.done", "output_index": st.OutputIndex, "item_id": item["id"], "input": input})
				emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": st.OutputIndex, "item": item})
				continue
			}
			item := map[string]any{"type": "function_call", "id": st.ItemID, "call_id": st.ID, "name": st.Name, "arguments": st.Args, "status": "completed"}
			outputByIndex[st.OutputIndex] = item
			emit("response.function_call_arguments.done", map[string]any{"type": "response.function_call_arguments.done", "output_index": st.OutputIndex, "item_id": st.ItemID, "arguments": st.Args})
			emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": st.OutputIndex, "item": item})
		}
	}
	if text.Len() > 0 {
		if !textStarted {
			if err := ensureTextStarted(); err != nil {
				return false
			}
		}
		contentPart := map[string]any{"type": "output_text", "text": text.String(), "annotations": []any{}}
		item := map[string]any{"type": "message", "id": messageID, "role": "assistant", "status": "completed", "content": []any{contentPart}}
		emit("response.output_text.done", map[string]any{"type": "response.output_text.done", "output_index": textOutputIndex, "content_index": 0, "item_id": messageID, "text": text.String()})
		emit("response.content_part.done", map[string]any{"type": "response.content_part.done", "output_index": textOutputIndex, "content_index": 0, "item_id": messageID, "part": contentPart})
		emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": textOutputIndex, "item": item})
		outputByIndex[textOutputIndex] = item
	}
	outputIndexes := make([]int, 0, len(outputByIndex))
	for index := range outputByIndex {
		outputIndexes = append(outputIndexes, index)
	}
	sort.Ints(outputIndexes)
	output := make([]any, 0, len(outputIndexes))
	for _, index := range outputIndexes {
		output = append(output, outputByIndex[index])
	}
	usageOutput := text.String() + reasoning.String()
	for _, call := range calls {
		usageOutput += call.Name + call.Args
	}
	estimate := estimateResponsesUsage(model, o.Messages, o.Tools, o.ToolChoice, usageOutput)
	u := responsesReuseUsage(innerUsage, numberInt64(estimate.Values["input_tokens"]), numberInt64(estimate.Values["output_tokens"]))
	cached := confirmedCachedTokens(u)
	usage := responsesUsage(u)
	source := usageSourceFromCachedTokens(cached)
	resp := map[string]any{"id": id, "object": "response", "created_at": created, "status": "completed", "model": model, "output": output, "usage": usage, "m365": localUsageMetadata(source)}
	stored := append([]oaiMsg(nil), o.Messages...)
	var converted []map[string]any
	if len(calls) > 0 {
		keys := make([]int, 0, len(calls))
		for key := range calls {
			keys = append(keys, key)
		}
		sort.Ints(keys)
		converted = make([]map[string]any, 0, len(calls))
		for _, key := range keys {
			call := calls[key]
			converted = append(converted, map[string]any{"id": call.ID, "type": call.Type, "function": map[string]any{"name": call.Name, "arguments": call.Args}})
		}
	}
	stored = appendResponsesAssistantHistory(stored, text.String(), converted)
	s.storeResponsesHistory(r.Context(), tenant, id, affinitySessionID, stored)
	if s.affinity != nil {
		s.affinity.bindResponse(r.Context(), s.affinityTenantIdentity(r), id, affinitySessionID)
	}
	completed := map[string]any{"type": "response.completed", "response": resp, "sequence_number": sequence}
	sequence++
	payload, _ := json.Marshal(completed)
	terminal := []byte(fmt.Sprintf("event: response.completed\ndata: %s\n\n", payload))
	if beforeCompleted != nil {
		if err := beforeCompleted(terminal); err != nil {
			log.Printf("[responses-state] pre-terminal commit failed response=%s: %v", shortPrefix(hashString(responseID)), err)
			emit("response.failed", map[string]any{
				"type": "response.failed",
				"response": map[string]any{
					"id": id, "object": "response", "status": "failed", "model": model,
					"error": map[string]any{"code": "response_state_commit_failed", "message": "response state changed before completion"},
				},
			})
			return false
		}
	}
	if err := r.Context().Err(); err != nil {
		return false
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := w.Write(terminal); err != nil {
		return false
	}
	if flusher != nil {
		flusher.Flush()
	}
	return true
}

func appendResponsesAssistantHistory(messages []oaiMsg, text string, calls []map[string]any) []oaiMsg {
	if text == "" && len(calls) == 0 {
		return messages
	}
	return append(messages, oaiMsg{Role: "assistant", Content: text, ToolCalls: calls})
}

// mergeResponsesContinuation keeps repeated request-level system policies out
// of the assistant-call/tool-result pair. Codex resends additional_tools on
// every turn, which recreates the custom-exec policy before the tool output.
func mergeResponsesContinuation(parent, current []oaiMsg) []oaiMsg {
	leadingSystem := 0
	for leadingSystem < len(current) && strings.EqualFold(strings.TrimSpace(current[leadingSystem].Role), "system") {
		leadingSystem++
	}
	policies := make([]oaiMsg, 0, leadingSystem)
	for _, policy := range current[:leadingSystem] {
		duplicate := false
		policyText := contentToString(policy.Content)
		for _, existing := range parent {
			if strings.EqualFold(strings.TrimSpace(existing.Role), "system") && contentToString(existing.Content) == policyText {
				duplicate = true
				break
			}
		}
		if !duplicate {
			policies = append(policies, policy)
		}
	}
	merged := append(policies, parent...)
	return append(merged, current[leadingSystem:]...)
}

func (s *Server) runOpenAIAdapter(r *http.Request, o oaiReq) (map[string]any, []byte, int, error) {
	o.Stream = false
	b, _ := json.Marshal(o)
	r2 := r.Clone(r.Context())
	r2 = carryExplicitToolRequirement(r2, o.ExplicitToolRequired)
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
	if compactRedundantProbeInstructions(&body) {
		w.Header().Set("X-M365-Probe-Instructions-Compacted", "1")
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
	affinitySessionID := sessionID
	publicID := "resp_" + uuid.NewString()
	nsKey := responseNamespace(tenant, sessionID)
	var claim *responseClaim
	claimFinished := false
	defer func() {
		if claim != nil && !claimFinished {
			s.releaseResponseClaim(r.Context(), claim)
			responseStateAudit(claim, "released")
		}
	}()
	if body.PreviousResponseID != "" {
		toolIDs := extractResponsesToolOutputIDs(body.Input)
		var replay *responseReplay
		var claimErr error
		claim, replay, claimErr = s.claimResponse(r.Context(), nsKey, body.PreviousResponseID, responseRequestDigest(body), tenant, sessionID, toolIDs)
		if claimErr != nil {
			status, errorType, message := responseStateErrorFields(claimErr)
			writeResponsesError(w, status, errorType, message)
			return
		}
		if replay != nil {
			log.Printf("[responses-audit] tenantHash=%s session=%s previous=%s action=replayed tool_ids=%v", tenantHashPrefix(tenant), sessionHashPrefix(sessionID), body.PreviousResponseID, toolIDs)
			writeResponseReplay(w, replay)
			return
		}
		responseStateAudit(claim, "claimed")
		log.Printf("[responses-audit] tenantHash=%s session=%s previous=%s action=claimed version=%d tool_ids=%v parentToolCalls=%d", tenantHashPrefix(tenant), sessionHashPrefix(sessionID), body.PreviousResponseID, claim.Version, toolIDs, claim.ToolCount)
		if s.debug != nil {
			s.debug.add(debugRecord{ID: "resp_" + uuid.NewString(), At: time.Now(), Path: "/v1/responses", Method: "POST", Status: 200, Level: "info", Gateway: map[string]any{"previous_response_id": body.PreviousResponseID, "tenantHash": tenantHashPrefix(tenant), "session": sessionHashPrefix(sessionID), "tool_ids": toolIDs, "version": claim.Version, "parentToolCalls": claim.ToolCount, "action": "claimed"}})
		}
		o.Messages = mergeResponsesContinuation(claim.Messages, o.Messages)
	}
	if s.settings != nil && s.settings.get().ToolProtocolMode == "pi_compat" {
		normalized, normalizeErr := normalizeResponsesToolHistory(o.Messages)
		if normalizeErr != nil {
			writeResponsesError(w, 400, "tool_protocol_error", normalizeErr.Error())
			return
		}
		o.Messages = normalized
	}
	r.Header.Set(sessionHeaderName, affinitySessionID)
	r.Header.Set(previousResponseHeader, affinitySessionID)
	if body.Stream {
		streamWriter := w
		var capture *captureResponseWriter
		if claim != nil {
			capture = &captureResponseWriter{ResponseWriter: w}
			streamWriter = capture
		}
		var beforeCompleted func([]byte) error
		if claim != nil {
			beforeCompleted = func(terminal []byte) error {
				replay := capture.replay()
				replay.Body = append(replay.Body, terminal...)
				if err := s.finishResponseClaim(r.Context(), claim, publicID, replay); err != nil {
					return err
				}
				claimFinished = true
				responseStateAudit(claim, "committed")
				return nil
			}
		}
		if !s.streamResponsesAdapter(streamWriter, r, o, firstNonEmpty(body.Model, defaultPublicModelName), publicID, affinitySessionID, nsKey, beforeCompleted) {
			return
		}
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
	estimate := estimateResponsesUsage(firstNonEmpty(body.Model, defaultPublicModelName), o.Messages, o.Tools, o.ToolChoice, outputForUsage)
	innerUsage, _ := out["usage"].(map[string]any)
	u := responsesReuseUsage(innerUsage, numberInt64(estimate.Values["input_tokens"]), numberInt64(estimate.Values["output_tokens"]))
	cached := confirmedCachedTokens(u)
	out["usage"] = responsesUsage(u)
	out["m365_usage_source"] = usageSourceFromCachedTokens(cached)
	s.usage.record(UsageRecord{
		Time:         time.Now(),
		APIKeyPrefix: extractAPIKey(r),
		Model:        firstNonEmpty(body.Model, defaultPublicModelName),
		Endpoint:     "/v1/responses",
		InputTokens:  u.PromptTokens - confirmedCachedTokens(u),
		OutputTokens: u.CompletionTokens,
		CacheTokens:  confirmedCachedTokens(u),
		CacheHit:     u.Confirmed,
		CacheSource:  usageSourceFromCachedTokens(cached),
		DurationMs:   time.Since(startedAt).Milliseconds(),
		Status:       200,
	})
	// Retain the normalized history so a subsequent previous_response_id can
	// validate its function_call_output against the original tool call.
	if _, ok := out["id"].(string); ok {
		out["m365_response_id"] = publicID
		stored := append([]oaiMsg(nil), o.Messages...)
		var storedToolCalls []map[string]any
		if msg, _ := openAIChoice(out); msg != nil {
			assistantText, _ := msg["content"].(string)
			if calls, ok := msg["tool_calls"].([]any); ok && len(calls) > 0 {
				converted := make([]map[string]any, 0, len(calls))
				for _, call := range calls {
					if m, ok := call.(map[string]any); ok {
						converted = append(converted, m)
					}
				}
				storedToolCalls = converted
			}
			stored = appendResponsesAssistantHistory(stored, assistantText, storedToolCalls)
		}
		toolCallsMap := buildRespToolCallsMap(storedToolCalls)
		s.responseMu.Lock()
		s.persistResponseNodeLocked(r.Context(), nsKey, publicID, &RespNode{At: time.Now(), Messages: stored, ToolCalls: toolCallsMap, Version: 1, Consumed: false, ParentID: body.PreviousResponseID, Tenant: tenant, SessionID: sessionID})
		s.responseMu.Unlock()
		log.Printf("[responses-audit] tenantHash=%s session=%s new=%s parent=%s toolCalls=%d version=1", tenantHashPrefix(tenant), sessionHashPrefix(sessionID), publicID, body.PreviousResponseID, len(toolCallsMap))
	}
	if claim == nil {
		writeResponsesResult(w, firstNonEmpty(body.Model, defaultPublicModelName), body.Stream, out)
		return
	}
	recorder := httptest.NewRecorder()
	writeResponsesResult(recorder, firstNonEmpty(body.Model, defaultPublicModelName), false, out)
	replay := responseReplay{Status: recorder.Code, Header: recorder.Header().Clone(), Body: append([]byte(nil), recorder.Body.Bytes()...)}
	if err := s.finishResponseClaim(r.Context(), claim, publicID, replay); err != nil {
		writeResponsesError(w, http.StatusConflict, "conflict", "previous_response_id state changed before completion")
		return
	}
	claimFinished = true
	responseStateAudit(claim, "committed")
	writeResponseReplay(w, &replay)
}

func (s *Server) responsesTenantKeys(r *http.Request) (tenant, affinityTenant string) {
	tenant = extractAPIKey(r)
	if s.affinity == nil || s.affinity.config.Mode == affinityOff {
		return tenant, ""
	}
	affinityTenant = s.affinityTenantIdentity(r)
	return affinityTenant, affinityTenant
}

func (s *Server) storeResponsesHistory(ctx context.Context, tenant, responseID, affinitySessionID string, messages []oaiMsg) {
	s.responseMu.Lock()
	defer s.responseMu.Unlock()
	nodeTenant := tenant
	if separator := strings.IndexByte(nodeTenant, 0); separator >= 0 {
		nodeTenant = nodeTenant[:separator]
	}
	var toolCalls []map[string]any
	if len(messages) > 0 && messages[len(messages)-1].Role == "assistant" {
		toolCalls = messages[len(messages)-1].ToolCalls
	}
	s.persistResponseNodeLocked(ctx, tenant, responseID, &RespNode{At: time.Now(), Messages: append([]oaiMsg(nil), messages...), ToolCalls: buildRespToolCallsMap(toolCalls), SessionID: affinitySessionID, Version: 1, Tenant: nodeTenant})
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
	model := firstNonEmpty(body.Model, defaultPublicModelName)
	if body.Stream {
		estimate := estimateResponsesUsage(model, o.Messages, o.Tools, o.ToolChoice, "")
		fallback := reuseUsage{PromptTokens: numberInt64(estimate.Values["input_tokens"])}
		u, ok := s.streamAnthropicAdapter(w, r, o, model, fallback)
		cached := confirmedCachedTokens(u)
		status := 200
		if !ok {
			status = http.StatusBadGateway
		}
		s.usage.record(UsageRecord{
			Time: time.Now(), APIKeyPrefix: extractAPIKey(r), Model: model, Endpoint: "/v1/messages",
			InputTokens: u.PromptTokens - cached, OutputTokens: u.CompletionTokens, CacheTokens: cached,
			CacheHit: u.Confirmed, CacheSource: usageSourceFromCachedTokens(cached),
			DurationMs: time.Since(startedAt).Milliseconds(), Status: status,
		})
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
	estimate := estimateResponsesUsage(
		model,
		o.Messages,
		o.Tools,
		o.ToolChoice,
		adapterOutputForUsage(out),
	)
	cached := cachedTokensFromChatResult(out)
	u := reuseUsage{PromptTokens: numberInt64(estimate.Values["input_tokens"]), CompletionTokens: numberInt64(estimate.Values["output_tokens"]), CachedTokens: cached, Confirmed: cached > 0}
	source := usageSourceFromCachedTokens(cached)
	s.usage.record(UsageRecord{
		Time:         time.Now(),
		APIKeyPrefix: extractAPIKey(r),
		Model:        model,
		Endpoint:     "/v1/messages",
		InputTokens:  u.PromptTokens - confirmedCachedTokens(u),
		OutputTokens: u.CompletionTokens,
		CacheTokens:  confirmedCachedTokens(u),
		CacheHit:     u.Confirmed,
		CacheSource:  source,
		DurationMs:   time.Since(startedAt).Milliseconds(),
		Status:       200,
	})
	writeAnthropicResult(w, model, false, out, anthropicUsage(u), source)
}

func adapterOutputForUsage(out map[string]any) string {
	msg, _ := openAIChoice(out)
	if msg == nil {
		return ""
	}
	var output strings.Builder
	output.WriteString(contentToString(msg["content"]))
	output.WriteString(contentToString(msg["reasoning_content"]))
	if calls, ok := msg["tool_calls"].([]any); ok {
		for _, call := range calls {
			output.WriteString(fmt.Sprint(call))
		}
	}
	return output.String()
}
