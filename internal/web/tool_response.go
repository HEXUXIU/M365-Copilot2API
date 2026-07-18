package web

import (
	"fmt"
	"m365-native/internal/chathub"
	"net/http"
	"strings"
	"time"
)

func toolPlanSummary(calls []detectedToolCall) string {
	if len(calls) == 0 {
		return "我先整理当前请求，再继续处理。"
	}
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Name)
	}
	return "我先确认必要的外部信息，然后调用：" + strings.Join(names, "、") + "。"
}

func writeToolResponse(w http.ResponseWriter, id, model string, stream bool, calls []detectedToolCall, res chathub.Result) error {
	toolCalls := toolCallMaps(calls)
	summary := toolPlanSummary(calls)
	msg := map[string]any{"role": "assistant", "content": nil, "reasoning_content": summary, "tool_calls": toolCalls}
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, _ := w.(http.Flusher)
		emit := func(v any) {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(v))
			if flusher != nil {
				flusher.Flush()
			}
		}
		base := func(delta map[string]any, finish any) map[string]any {
			return map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
		}
		emit(base(map[string]any{"role": "assistant", "content": nil, "reasoning_content": summary}, nil))
		for i, tc := range calls {
			emit(base(map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": string(tc.Arguments)}}}}, nil))
		}
		emit(base(map[string]any{}, "tool_calls"))
		fmt.Fprint(w, "data: [DONE]\n\n")
		return nil
	}
	jsonOut(w, map[string]any{"id": id, "object": "chat.completion", "model": model, "choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "tool_calls"}}, "m365": compatM365Metadata(res)})
	return nil
}
