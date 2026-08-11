package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"m365-copilot2api/internal/chathub"
)

func TestRequestUsageCaptureRecordsOnlyIncrementalPrefixReuse(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r, capture := ensureRequestUsageCapture(r)
	if requestUsageCaptureFrom(r) != capture {
		t.Fatal("request context did not preserve the usage capture")
	}

	capture.RecordReusedPrefix(0, 3)
	capture.RecordReusedPrefix(3, 3)
	if got := capture.ReusedMessages(); got != 0 {
		t.Fatalf("full-prompt requests must not report cache reuse, got %d", got)
	}

	capture.RecordReusedPrefix(2, 3)
	if got := capture.ReusedMessages(); got != 2 {
		t.Fatalf("reused messages=%d want=2", got)
	}
}

func TestChatCompletionUsageIncludesCachedPrefix(t *testing.T) {
	messages := []oaiMsg{
		{Role: "system", Content: "stable system context"},
		{Role: "user", Content: "first question"},
		{Role: "user", Content: "new question"},
	}
	usage := estimateChatCompletionUsage(messages, "stable system context first question new question", "answer", 2)

	details, ok := usage["prompt_tokens_details"].(map[string]any)
	if !ok {
		t.Fatalf("missing prompt_tokens_details: %#v", usage)
	}
	cached, ok := details["cached_tokens"].(int64)
	if !ok || cached <= 0 {
		t.Fatalf("cached_tokens=%#v", details["cached_tokens"])
	}
	prompt, ok := usage["prompt_tokens"].(int64)
	if !ok || cached > prompt {
		t.Fatalf("cached=%d prompt=%#v", cached, usage["prompt_tokens"])
	}
}

func TestChatCompletionUsageOmitsCacheWithoutReuse(t *testing.T) {
	usage := estimateChatCompletionUsage(
		[]oaiMsg{{Role: "user", Content: "hello"}},
		"hello",
		"world",
		0,
	)
	if _, ok := usage["prompt_tokens_details"]; ok {
		t.Fatalf("unexpected cache details: %#v", usage)
	}
}

func TestRecordResolvedCacheUsageRequiresIncrementalReuse(t *testing.T) {
	tests := []struct {
		name     string
		resolved ResolveResult
		total    int
		want     int
	}{
		{name: "new conversation", resolved: ResolveResult{IsNew: true}, total: 3, want: 0},
		{name: "similarity fallback without prefix", resolved: ResolveResult{MatchedBy: "context_similar_0.90", IsNew: false}, total: 3, want: 0},
		{name: "full prompt resent", resolved: ResolveResult{MatchedBy: "context_prefix_3", IsNew: false, HistoryLen: 3}, total: 3, want: 0},
		{name: "strict prefix suffix sent", resolved: ResolveResult{MatchedBy: "context_prefix_2", IsNew: false, HistoryLen: 2}, total: 3, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/responses", nil)
			r, capture := ensureRequestUsageCapture(r)
			recordResolvedCacheUsage(r, tt.resolved, tt.total)
			if got := capture.ReusedMessages(); got != tt.want {
				t.Fatalf("reused messages=%d want=%d", got, tt.want)
			}
		})
	}
}

func TestWriteStreamFinishIncludesCacheUsage(t *testing.T) {
	rr := httptest.NewRecorder()
	usage := map[string]any{
		"prompt_tokens":         int64(12),
		"completion_tokens":     int64(3),
		"total_tokens":          int64(15),
		"prompt_tokens_details": map[string]any{"cached_tokens": int64(8)},
	}
	writeStreamFinish(context.Background(), rr, rr, "chatcmpl_test", "gpt-5.6-sol", usage)
	if body := rr.Body.String(); !strings.Contains(body, `"cached_tokens":8`) {
		t.Fatalf("stream finish missing cache usage: %s", body)
	}
}

func TestWriteToolResponseIncludesCacheUsage(t *testing.T) {
	usage := map[string]any{
		"prompt_tokens":         int64(12),
		"completion_tokens":     int64(3),
		"total_tokens":          int64(15),
		"prompt_tokens_details": map[string]any{"cached_tokens": int64(8)},
	}
	calls := []detectedToolCall{{ID: "call_1", Name: "lookup", Arguments: []byte(`{"id":1}`)}}
	for _, stream := range []bool{false, true} {
		rr := httptest.NewRecorder()
		if err := writeToolResponse(rr, "chatcmpl_test", "gpt-5.6-sol", stream, calls, chathub.Result{}, usage); err != nil {
			t.Fatal(err)
		}
		if body := rr.Body.String(); !strings.Contains(body, `"cached_tokens":8`) {
			t.Fatalf("stream=%t response missing cache usage: %s", stream, body)
		}
	}
}

func TestRequestResponsesUsageUsesCapturedPrefix(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r, capture := ensureRequestUsageCapture(r)
	capture.RecordReusedPrefix(2, 3)
	messages := []oaiMsg{
		{Role: "system", Content: "stable system context"},
		{Role: "user", Content: "first question"},
		{Role: "user", Content: "new question"},
	}

	estimate := requestResponsesUsage(r, "gpt-5.6-sol", messages, nil, nil, "answer")
	if cached := responsesCachedTokens(estimate.Values); cached <= 0 {
		t.Fatalf("request capture was not reflected in Responses usage: %#v", estimate.Values)
	}
}
