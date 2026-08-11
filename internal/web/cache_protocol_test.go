package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"m365-copilot2api/internal/chathub"
)

func TestCacheUsageProtocolMappings(t *testing.T) {
	u := reuseUsage{PromptTokens: 100, CompletionTokens: 20, CachedTokens: 64, Confirmed: true}

	t.Run("chat_non_stream", func(t *testing.T) {
		rr := httptest.NewRecorder()
		jsonOut(rr, map[string]any{"usage": chatUsage(u)})
		var payload map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		usage := payload["usage"].(map[string]any)
		details := usage["prompt_tokens_details"].(map[string]any)
		if numberInt64(details["cached_tokens"]) != 64 {
			t.Fatalf("payload=%s", rr.Body.String())
		}
	})

	t.Run("chat_stream_terminal", func(t *testing.T) {
		rr := httptest.NewRecorder()
		writeStreamFinish(context.Background(), rr, rr, "chatcmpl-test", "gpt-test", chatUsage(u))
		var chunk map[string]any
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rr.Body.String()), "data: "))
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			t.Fatal(err)
		}
		usage := chunk["usage"].(map[string]any)
		details := usage["prompt_tokens_details"].(map[string]any)
		if numberInt64(details["cached_tokens"]) != 64 {
			t.Fatalf("chunk=%s", rr.Body.String())
		}
	})

	t.Run("responses", func(t *testing.T) {
		rr := httptest.NewRecorder()
		writeResponsesResult(rr, "gpt-test", false, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "ok"}, "finish_reason": "stop"}},
			"usage":   responsesUsage(u), "m365_usage_source": "conversation_reuse",
		})
		var payload map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		usage := payload["usage"].(map[string]any)
		details := usage["input_tokens_details"].(map[string]any)
		if numberInt64(details["cached_tokens"]) != 64 {
			t.Fatalf("payload=%s", rr.Body.String())
		}
	})

	t.Run("anthropic_non_stream", func(t *testing.T) {
		rr := httptest.NewRecorder()
		writeAnthropicResult(rr, "gpt-test", false, map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": "ok"}, "finish_reason": "stop"}},
		}, anthropicUsage(u), "conversation_reuse")
		var payload map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		usage := payload["usage"].(map[string]any)
		if numberInt64(usage["cache_read_input_tokens"]) != 64 {
			t.Fatalf("payload=%s", rr.Body.String())
		}
	})

	t.Run("tool_stream_terminal", func(t *testing.T) {
		rr := httptest.NewRecorder()
		calls := []detectedToolCall{{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{"q":"x"}`)}}
		if err := writeToolResponse(rr, "chatcmpl-test", "gpt-test", true, calls, chathub.Result{}, chatUsage(u)); err != nil {
			t.Fatal(err)
		}
		frames := strings.Split(rr.Body.String(), "\n\n")
		found := false
		for _, frame := range frames {
			if !strings.HasPrefix(frame, "data: {") {
				continue
			}
			var chunk map[string]any
			if json.Unmarshal([]byte(strings.TrimPrefix(frame, "data: ")), &chunk) != nil || chunk["usage"] == nil {
				continue
			}
			usage := chunk["usage"].(map[string]any)
			details := usage["prompt_tokens_details"].(map[string]any)
			found = numberInt64(details["cached_tokens"]) == 64
		}
		if !found {
			t.Fatalf("terminal usage missing: %s", rr.Body.String())
		}
	})
}
