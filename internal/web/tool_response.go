package web

import (
	"m365-copilot2api/internal/chathub"
	"net/http"
	"time"
	"unicode/utf8"
)

func writeToolResponse(w http.ResponseWriter, id, model string, stream, sendUsage bool, calls []detectedToolCall, res chathub.Result, usageOverride map[string]any) error {
	// Usage is always emitted for tool streams; Codex relies on a terminal usage frame.
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
		w.Header().Set("X-Accel-Buffering", "no")
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
				emit(base(map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": argChunk}}}}, nil))
			}
		}
		// Keep the terminal marker in its own chunk. Some OpenAI-to-Anthropic
		// relays finalize a tool block as soon as they see finish_reason and
		// otherwise drop arguments carried in that same chunk.
		emit(base(map[string]any{}, "tool_calls"))
		if sendUsage || stream {
			usage := usageOverride
			if usage == nil {
				usage = map[string]any{"prompt_tokens": pt, "completion_tokens": ct, "total_tokens": pt + ct}
			}
			usageChunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{}, "usage": usage}
			_ = sseSafeRaw(w, flusher, "data: "+mustJSON(usageChunk)+"\n\n")
		}
		_ = sseSafeRaw(w, flusher, "data: [DONE]\n\n")
		return nil
	}
	usage := usageOverride
	if usage == nil {
		usage = map[string]any{"prompt_tokens": pt, "completion_tokens": ct, "total_tokens": pt + ct}
	}
	jsonOut(w, map[string]any{"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "tool_calls"}}, "m365": compatM365Metadata(res), "usage": usage})
	return nil
}
