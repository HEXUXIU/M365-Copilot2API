package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

func openAIChoice(v map[string]any) (map[string]any, string) {
	choices, _ := v["choices"].([]any)
	if len(choices) == 0 {
		return nil, ""
	}
	c, _ := choices[0].(map[string]any)
	m, _ := c["message"].(map[string]any)
	finish, _ := c["finish_reason"].(string)
	return m, finish
}

func writeAnthropicResult(w http.ResponseWriter, model string, stream bool, src map[string]any, usage map[string]any, usageSource string) {
	id := "msg_" + uuid.NewString()
	msg, finish := openAIChoice(src)
	sanitizePublicAssistantMessage(msg, model)
	blocks := []any{}
	stop := "end_turn"
	if reasoning, _ := msg["reasoning_content"].(string); reasoning != "" {
		blocks = append(blocks, map[string]any{"type": "thinking", "thinking": reasoning, "signature": ""})
	}
	if calls, ok := msg["tool_calls"].([]any); ok {
		stop = "tool_use"
		for _, raw := range calls {
			tc, _ := raw.(map[string]any)
			fn, _ := tc["function"].(map[string]any)
			var input any = map[string]any{}
			if a, ok := fn["arguments"].(string); ok {
				_ = json.Unmarshal([]byte(a), &input)
			}
			blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc["id"], "name": fn["name"], "input": input})
		}
	} else {
		switch content := msg["content"].(type) {
		case []any:
			for _, raw := range content {
				part, _ := raw.(map[string]any)
				switch part["type"] {
				case "text":
					if t, _ := part["text"].(string); t != "" {
						blocks = append(blocks, map[string]any{"type": "text", "text": t})
					}
				case "image_url":
					img, _ := part["image_url"].(map[string]any)
					if u, _ := img["url"].(string); u != "" {
						if strings.HasPrefix(u, "data:") {
							parts := strings.SplitN(u, ",", 2)
							meta := parts[0]
							b64 := ""
							if len(parts) == 2 {
								b64 = parts[1]
							}
							media := strings.TrimPrefix(meta, "data:")
							media = strings.SplitN(media, ";", 2)[0]
							blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": media, "data": b64}})
						} else {
							blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": u}})
						}
					}
				}
			}
		default:
			blocks = append(blocks, map[string]any{"type": "text", "text": fmt.Sprint(content)})
		}
		if len(blocks) == 0 {
			blocks = append(blocks, map[string]any{"type": "text", "text": ""})
		}
	}
	_ = finish
	if usage == nil {
		inputTokens := int64(0)
		outputTokens := int64(0)
		if u, ok := src["usage"].(map[string]any); ok {
			if v, ok := u["prompt_tokens"]; ok {
				if n, ok := v.(int64); ok {
					inputTokens = n
				}
				if n, ok := v.(float64); ok {
					inputTokens = int64(n)
				}
			}
			if v, ok := u["completion_tokens"]; ok {
				if n, ok := v.(int64); ok {
					outputTokens = n
				}
				if n, ok := v.(float64); ok {
					outputTokens = int64(n)
				}
			}
		}
		usage = map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens}
	}
	if usageSource == "" {
		usageSource = "none"
	}
	out := map[string]any{"id": id, "type": "message", "role": "assistant", "model": model, "content": blocks, "stop_reason": stop, "stop_sequence": nil, "usage": usage, "m365": localUsageMetadata(usageSource)}
	if !stream {
		jsonOut(w, out)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	f, _ := w.(http.Flusher)
	aborted := false
	emit := func(n string, v any) {
		if aborted {
			return
		}
		if err := sseWriteFrame(w, f, n, v); err != nil {
			aborted = true
		}
	}
	startUsage := map[string]any{"input_tokens": numberInt64(usage["input_tokens"]), "output_tokens": int64(0)}
	if cached := numberInt64(usage["cache_read_input_tokens"]); cached > 0 {
		startUsage["cache_read_input_tokens"] = cached
	}
	emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": model, "content": []any{}, "stop_reason": nil, "usage": startUsage}})
	for i, b := range blocks {
		m, _ := b.(map[string]any)
		startBlock := b
		blockType := ""
		if t, _ := m["type"].(string); t != "" {
			blockType = t
		}
		switch blockType {
		case "tool_use":
			startBlock = map[string]any{"type": "tool_use", "id": m["id"], "name": m["name"], "input": map[string]any{}}
		case "thinking":
			startBlock = map[string]any{"type": "thinking", "thinking": "", "signature": ""}
		case "image":
			startBlock = map[string]any{"type": "image", "source": m["source"]}
		}
		emit("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": startBlock})
		switch blockType {
		case "text":
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "text_delta", "text": m["text"]}})
		case "tool_use":
			partial, _ := json.Marshal(m["input"])
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(partial)}})
		case "thinking":
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "thinking_delta", "thinking": m["thinking"]}})
		}
		emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}
	emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil}, "usage": usage})
	emit("message_stop", map[string]any{"type": "message_stop"})
}

// sseWriteFrame writes one SSE frame and flushes; a write error (client gone,
// deadline exceeded) aborts the stream instead of leaving the handler blocked.
func sseWriteFrame(w http.ResponseWriter, f http.Flusher, name string, value any) error {
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

// sseDataRaw writes a raw "data: ..." frame with the same write deadline.
func sseDataRaw(w http.ResponseWriter, f http.Flusher, data string) error {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	if f != nil {
		f.Flush()
	}
	return nil
}

// sseSafeRaw writes a pre-formatted frame (e.g. ": connected" or "[DONE]").
func sseSafeRaw(w http.ResponseWriter, f http.Flusher, payload string) error {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := fmt.Fprint(w, payload); err != nil {
		return err
	}
	if f != nil {
		f.Flush()
	}
	return nil
}
