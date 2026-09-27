package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteAnthropicResultThinkingPlain(t *testing.T) {
	src := map[string]any{"choices": []any{map[string]any{"message": map[string]any{
		"content":           "final",
		"reasoning_content": "deep think",
	}}}}
	w := httptest.NewRecorder()
	writeAnthropicResult(w, "claude-sonnet", false, src)
	var out struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad json: %v %s", err, w.Body.String())
	}
	if len(out.Content) != 2 || out.Content[0]["type"] != "thinking" || out.Content[0]["thinking"] != "deep think" || out.Content[1]["type"] != "text" {
		t.Fatalf("unexpected blocks: %#v", out.Content)
	}
}

func TestAnthropicResultContentParts(t *testing.T) {
	src := map[string]any{"choices": []any{map[string]any{"message": map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "hi"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/x.png"}},
		},
	}}}}
	w := httptest.NewRecorder()
	writeAnthropicResult(w, "m", false, src)
	var out struct {
		Content []map[string]any `json:"content"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Content) != 2 {
		t.Fatalf("expected 2 blocks got %d: %s", len(out.Content), w.Body.String())
	}
	if out.Content[1]["type"] != "image" {
		t.Fatalf("image part not converted: %s", w.Body.String())
	}
	_ = strings.TrimSpace
}

func TestAnthropicStreamEmitsThinkingSSE(t *testing.T) {
	src := map[string]any{"choices": []any{map[string]any{"message": map[string]any{
		"content":           "final",
		"reasoning_content": "think1",
	}}}}
	w := httptest.NewRecorder()
	writeAnthropicResult(w, "m", true, src)
	body := w.Body.String()
	if !strings.Contains(body, `"type":"thinking"`) && !strings.Contains(body, `thinking_delta`) {
		t.Fatalf("missing thinking SSE frames: %s", body)
	}
	if !strings.Contains(body, "text_delta") {
		t.Fatalf("missing text delta: %s", body)
	}
}
