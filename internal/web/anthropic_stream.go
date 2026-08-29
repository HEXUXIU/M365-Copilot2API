package web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type anthropicToolStreamState struct {
	callID    string
	name      string
	arguments string
	index     int
	started   bool
}

// streamAnthropicAdapter converts the gateway's internal OpenAI stream into
// Anthropic SSE as deltas arrive. Tool blocks are announced only after both
// their stable call ID and name are known.
func (s *Server) streamAnthropicAdapter(w http.ResponseWriter, r *http.Request, o oaiReq, model string, fallback reuseUsage) (reuseUsage, bool) {
	return s.streamAnthropicAdapterWithRunner(w, r, o, model, fallback, s.openaiChat)
}

func (s *Server) streamAnthropicAdapterWithRunner(w http.ResponseWriter, r *http.Request, o oaiReq, model string, fallback reuseUsage, run func(http.ResponseWriter, *http.Request)) (reuseUsage, bool) {
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
			if recovered := recover(); recovered != nil {
				log.Printf("[anthropic] inner goroutine panic: %v", recovered)
			}
			_ = pw.Close()
			close(innerDone)
		}()
		run(irw, r2)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	sw := newSSEWriter(w, flusher)
	emit := func(name string, value any) error {
		payload, _ := json.Marshal(value)
		return sw.raw("event: " + name + "\ndata: " + string(payload) + "\n\n")
	}

	messageID := "msg_" + uuid.NewString()
	startUsage := anthropicUsage(fallback)
	startUsage["output_tokens"] = int64(0)
	if err := emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": messageID, "type": "message", "role": "assistant", "model": model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": startUsage,
		},
	}); err != nil {
		return fallback, false
	}
	stopHeartbeat := startSSEHeartbeat(r.Context(), sw, responsesHeartbeatInterval)
	defer stopHeartbeat()

	nextBlock := 0
	textIndex := -1
	reasoningIndex := -1
	startedBlocks := map[int]bool{}
	stoppedBlocks := map[int]bool{}
	startBlock := func(index int, block map[string]any) error {
		if startedBlocks[index] {
			return nil
		}
		startedBlocks[index] = true
		return emit("content_block_start", map[string]any{"type": "content_block_start", "index": index, "content_block": block})
	}
	stopBlock := func(index int) error {
		if !startedBlocks[index] || stoppedBlocks[index] {
			return nil
		}
		stoppedBlocks[index] = true
		return emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
	}

	var text strings.Builder
	var emittedText strings.Builder
	var reasoning strings.Builder
	reasoningGate := newPublicReasoningGate()
	authoritativeText := ""
	hasAuthoritativeText := false
	tools := map[int]*anthropicToolStreamState{}
	var innerUsage map[string]any
	innerFinishReason := ""
	innerError := ""
	sawInnerDone := false
	emitReasoning := func(part string) error {
		if part == "" {
			return nil
		}
		if reasoningIndex < 0 {
			reasoningIndex = nextBlock
			nextBlock++
			if err := startBlock(reasoningIndex, map[string]any{"type": "thinking", "thinking": "", "signature": ""}); err != nil {
				return err
			}
		}
		reasoning.WriteString(part)
		return emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": reasoningIndex, "delta": map[string]any{"type": "thinking_delta", "thinking": part}})
	}
	startPublicContent := func() error {
		return emitReasoning(reasoningGate.StartContent())
	}

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		if r.Context().Err() != nil {
			return fallback, false
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
			if err := emitReasoning(reasoningGate.PushReasoning(part)); err != nil {
				return fallback, false
			}
		}
		if part, ok := delta["content"].(string); ok && part != "" {
			if err := startPublicContent(); err != nil {
				return fallback, false
			}
			if textIndex < 0 {
				textIndex = nextBlock
				nextBlock++
				if err := startBlock(textIndex, map[string]any{"type": "text", "text": ""}); err != nil {
					return fallback, false
				}
			}
			text.WriteString(part)
			emittedText.WriteString(part)
			if err := emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": textIndex, "delta": map[string]any{"type": "text_delta", "text": part}}); err != nil {
				return fallback, false
			}
		}
		if rawCalls, ok := delta["tool_calls"].([]any); ok {
			if len(rawCalls) > 0 {
				if err := startPublicContent(); err != nil {
					return fallback, false
				}
			}
			for _, rawCall := range rawCalls {
				call, ok := rawCall.(map[string]any)
				if !ok {
					continue
				}
				indexValue, ok := call["index"].(float64)
				if !ok {
					continue
				}
				callIndex := int(indexValue)
				state := tools[callIndex]
				if state == nil {
					state = &anthropicToolStreamState{index: nextBlock}
					nextBlock++
					tools[callIndex] = state
				}
				if value, ok := call["id"].(string); ok {
					state.callID = value
				}
				function, _ := call["function"].(map[string]any)
				if value, ok := function["name"].(string); ok {
					state.name += value
				}
				if !state.started && strings.TrimSpace(state.callID) != "" && strings.TrimSpace(state.name) != "" {
					if err := startBlock(state.index, map[string]any{"type": "tool_use", "id": state.callID, "name": state.name, "input": map[string]any{}}); err != nil {
						return fallback, false
					}
					state.started = true
					if state.arguments != "" {
						if err := emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": state.index, "delta": map[string]any{"type": "input_json_delta", "partial_json": state.arguments}}); err != nil {
							return fallback, false
						}
					}
				}
				if value, ok := function["arguments"].(string); ok && value != "" {
					state.arguments += value
					if state.started {
						if err := emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": state.index, "delta": map[string]any{"type": "input_json_delta", "partial_json": value}}); err != nil {
							return fallback, false
						}
					}
				}
			}
		}
	}
	<-innerDone

	fail := func(message string) (reuseUsage, bool) {
		_ = emit("error", map[string]any{"type": "error", "error": map[string]any{"type": "api_error", "message": sanitizePublicInternalText(message)}})
		return fallback, false
	}
	if scanner.Err() != nil {
		return fail("inner chat stream read failed: " + scanner.Err().Error())
	}
	if irw.status >= http.StatusBadRequest || innerError != "" || !sawInnerDone || innerFinishReason == "" || innerFinishReason == "error" {
		message := innerError
		if message == "" {
			message = "inner chat stream ended before its terminal event"
		}
		return fail(message)
	}
	if reasoningGate.LateBytes() > 0 {
		log.Printf("[reasoning-order] protocol=anthropic dropped_late_bytes=%d", reasoningGate.LateBytes())
	}
	finalText := text.String()
	if hasAuthoritativeText {
		finalText = authoritativeText
	}
	if !strings.HasPrefix(finalText, emittedText.String()) {
		return fail("upstream revised text that was already streamed")
	}
	if suffix := finalText[len(emittedText.String()):]; suffix != "" {
		if err := startPublicContent(); err != nil {
			return fallback, false
		}
		if textIndex < 0 {
			textIndex = nextBlock
			nextBlock++
			if err := startBlock(textIndex, map[string]any{"type": "text", "text": ""}); err != nil {
				return fallback, false
			}
		}
		if err := emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": textIndex, "delta": map[string]any{"type": "text_delta", "text": suffix}}); err != nil {
			return fallback, false
		}
		text.WriteString(suffix)
		emittedText.WriteString(suffix)
	}
	if err := emitReasoning(reasoningGate.Finish()); err != nil {
		return fallback, false
	}

	blockIndexes := make([]int, 0, len(startedBlocks))
	for index := range startedBlocks {
		blockIndexes = append(blockIndexes, index)
	}
	sort.Ints(blockIndexes)
	for _, index := range blockIndexes {
		if err := stopBlock(index); err != nil {
			return fallback, false
		}
	}
	if len(startedBlocks) == 0 {
		return fail("ChatHub returned no text or tool call")
	}
	for _, state := range tools {
		if !state.started || strings.TrimSpace(state.callID) == "" || strings.TrimSpace(state.name) == "" {
			return fail("upstream tool call is missing call_id or name")
		}
	}

	usageOutput := text.String() + reasoning.String()
	for _, state := range tools {
		usageOutput += state.name + state.arguments
	}
	if fallback.CompletionTokens <= 0 {
		fallback.CompletionTokens = int64(EstimateTokens(usageOutput))
	}
	u := responsesReuseUsage(innerUsage, fallback.PromptTokens, fallback.CompletionTokens)
	stopReason := "end_turn"
	if len(tools) > 0 {
		stopReason = "tool_use"
	}
	if err := emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil}, "usage": anthropicUsage(u)}); err != nil {
		return fallback, false
	}
	if err := emit("message_stop", map[string]any{"type": "message_stop"}); err != nil {
		return fallback, false
	}
	return u, true
}
