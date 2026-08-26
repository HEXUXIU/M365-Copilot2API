package web

import (
	"m365-copilot2api/internal/chathub"
	"net/http"
	"time"
	"unicode/utf8"
)

func writeToolResponse(w http.ResponseWriter, id, model string, args ...any) error {
	var stream, sendUsage bool
	// Usage is always emitted for tool streams; clients rely on a terminal usage frame.
	sendUsage = true
	var calls []detectedToolCall
	var res chathub.Result
	var usageOverride map[string]any
	if len(args) >= 4 && func() bool { _, ok := args[1].(bool); return ok }() {
		stream, _ = args[0].(bool)
		sendUsage, _ = args[1].(bool)
		calls, _ = args[2].([]detectedToolCall)
		res, _ = args[3].(chathub.Result)
	} else if len(args) >= 3 {
		// Legacy call shape: stream, calls, result, usage.
		stream, _ = args[0].(bool)
		calls, _ = args[1].([]detectedToolCall)
		res, _ = args[2].(chathub.Result)
		sendUsage = true
		if len(args) > 3 {
			usageOverride, _ = args[3].(map[string]any)
		}
	} else if len(args) >= 2 {
		calls, _ = args[0].([]detectedToolCall)
		res, _ = args[1].(chathub.Result)
		stream = true
		sendUsage = true
	}
	toolCalls := toolCallMaps(calls)
	msg := map[string]any{"role": "assistant", "content": nil, "tool_calls": toolCalls}
	if res.Reasoning != "" {
		if reasoning := sanitizePublicReasoningText(res.Reasoning); reasoning != "" {
			msg["reasoning_content"] = reasoning
		}
	}
	pt := EstimateTokens(res.Text)
	for _, tc := range calls {
		pt += EstimateTokens(string(tc.Arguments))
	}
	ct := EstimateTokens(res.Text)
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, _ := w.(http.Flusher)
		emit := func(v any) {
			if err := sseDataRaw(w, flusher, mustJSON(v)); err != nil {
				return
			}
		}
		base := func(delta map[string]any, finish any) map[string]any {
			return map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		}
		firstDelta := map[string]any{"role": "assistant", "content": nil}
		if reasoning := sanitizePublicReasoningText(res.Reasoning); reasoning != "" {
			firstDelta["reasoning_content"] = reasoning
		}
		emit(base(firstDelta, nil))
		const chunkSize = 512
		for i, tc := range calls {
			typ := tc.Type
			if typ == "" {
				typ = "function"
			}
			isLast := i == len(calls)-1
			emit(base(map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": tc.ID, "type": typ, "function": map[string]any{"name": tc.Name, "arguments": ""}}}}, nil))
			args := string(tc.Arguments)
			for off := 0; off < len(args); off += chunkSize {
				end := off + chunkSize
				if end > len(args) {
					end = len(args)
				}
				for end < len(args) && !utf8.RuneStart(args[end]) {
					end++
				}
				argChunk := args[off:end]
				isLastArgChunk := off+chunkSize >= len(args)
				var finish any
				if isLast && isLastArgChunk {
					finish = "tool_calls"
				}
				emit(base(map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": argChunk}}}}, finish))
			}
			if len(args) == 0 && isLast {
				emit(base(map[string]any{}, "tool_calls"))
			}
		}
		if sendUsage || stream {
			usage := usageOverride
			if usage == nil {
				usage = map[string]any{"prompt_tokens": pt, "completion_tokens": ct, "total_tokens": pt + ct}
			}
			usageChunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": nil}}, "usage": usage}
			_ = sseSafeRaw(w, flusher, "data: "+mustJSON(usageChunk)+"\n\n")
		}
		_ = sseSafeRaw(w, flusher, "data: [DONE]\n\n")
		return nil
	}
	jsonOut(w, map[string]any{"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "tool_calls"}}, "m365": compatM365Metadata(res), "usage": map[string]any{"prompt_tokens": pt, "completion_tokens": ct, "total_tokens": pt + ct}})
	return nil
}
