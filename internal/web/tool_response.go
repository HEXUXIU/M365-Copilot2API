package web

import (
	"m365-copilot2api/internal/chathub"
	"net/http"
	"time"
)

func writeToolResponse(w http.ResponseWriter, id, model string, stream bool, calls []detectedToolCall, res chathub.Result, usage map[string]any) error {
	toolCalls := toolCallMaps(calls)
	msg := map[string]any{"role": "assistant", "content": nil, "tool_calls": toolCalls}
	if res.Reasoning != "" {
		msg["reasoning_content"] = res.Reasoning
	}
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
		if res.Reasoning != "" {
			firstDelta["reasoning_content"] = res.Reasoning
		}
		emit(base(firstDelta, nil))
		for i, tc := range calls {
			typ := tc.Type
			if typ == "" {
				typ = "function"
			}
			emit(base(map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": tc.ID, "type": typ, "function": map[string]any{"name": tc.Name, "arguments": string(tc.Arguments)}}}}, nil))
		}
		finish := base(map[string]any{}, "tool_calls")
		if usage != nil {
			finish["usage"] = usage
		}
		emit(finish)
		_ = sseSafeRaw(w, flusher, "data: [DONE]\n\n")
		return nil
	}
	jsonOut(w, map[string]any{"id": id, "object": "chat.completion", "model": model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "tool_calls"}}, "m365": compatM365Metadata(res), "usage": usage})
	return nil
}
