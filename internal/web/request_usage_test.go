package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"m365-copilot2api/internal/chathub"
)

func TestRequestUsageCaptureMeasuresPromptDelta(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r, capture := ensureRequestUsageCapture(r)
	if requestUsageCaptureFrom(r) != capture {
		t.Fatal("request context did not preserve the usage capture")
	}

	fullPrompt := "[system]\n你是精确的助手。\n\n[user]\nExplain cache behavior 123.\n\n[user]\n继续"
	sentPrompt := "[user]\n继续"
	capture.RecordPromptDelta(fullPrompt, sentPrompt)
	count, _ := tokenEstimator("gpt-5.6-sol")
	want := count(fullPrompt) - count(sentPrompt)
	if got := capture.CachedTokens("gpt-5.6-sol"); got != want {
		t.Fatalf("cached tokens=%d want=%d", got, want)
	}
}

func TestRequestUsageCaptureConcurrentAccess(t *testing.T) {
	var capture requestUsageCapture
	fullPrompt := strings.Repeat("稳定 cache context 123 ", 64)
	sentPrompt := "继续"
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			capture.RecordPromptDelta(fullPrompt, sentPrompt)
		}()
		go func() {
			defer wg.Done()
			_ = capture.CachedTokens("gpt-5.6-sol")
		}()
	}
	wg.Wait()
	if got := capture.CachedTokens("gpt-5.6-sol"); got <= 0 {
		t.Fatalf("cached tokens=%d want positive", got)
	}
}

func TestChatCompletionUsageUsesPromptDelta(t *testing.T) {
	messages := []oaiMsg{
		{Role: "system", Content: "稳定 system context"},
		{Role: "user", Content: "first question 123"},
		{Role: "user", Content: "继续"},
	}
	fullPrompt, _ := flattenPromptMessages(messages, nil)
	sentPrompt, _ := flattenPromptMessages(messages[2:], nil)
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r, capture := ensureRequestUsageCapture(r)
	capture.RecordPromptDelta(fullPrompt, sentPrompt)
	usage := requestChatCompletionUsage(r, "gpt-5.6-sol", fullPrompt, "完成")

	count, _ := tokenEstimator("gpt-5.6-sol")
	if got, want := usage["prompt_tokens"], int64(count(fullPrompt)); got != want {
		t.Fatalf("prompt_tokens=%#v want=%d", got, want)
	}
	wantCached := int64(count(fullPrompt) - count(sentPrompt))
	if got := chatCachedTokens(usage); got != wantCached {
		t.Fatalf("cached_tokens=%d want=%d", got, wantCached)
	}
	if got, want := usage["completion_tokens"], int64(count("完成")); got != want {
		t.Fatalf("completion_tokens=%#v want=%d", got, want)
	}
}

func TestChatCompletionUsageOmitsCacheWithoutReuse(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	usage := requestChatCompletionUsage(r, "gpt-5.6-sol", "hello", "world")
	if _, ok := usage["prompt_tokens_details"]; ok {
		t.Fatalf("unexpected cache details: %#v", usage)
	}
}

func TestRecordResolvedCacheUsageRequiresSentIncrementalPrompt(t *testing.T) {
	fullPrompt := "[system]\nstable context\n\n[user]\nfirst\n\n[user]\nnext"
	sentPrompt := "[user]\nnext"
	count, _ := tokenEstimator("gpt-5.6-sol")
	wantCached := count(fullPrompt) - count(sentPrompt)
	tests := []struct {
		name       string
		resolved   ResolveResult
		total      int
		fullPrompt string
		sentPrompt string
		want       int
	}{
		{name: "new conversation", resolved: ResolveResult{IsNew: true, HistoryLen: 2}, total: 3, fullPrompt: fullPrompt, sentPrompt: sentPrompt},
		{name: "similarity fallback without prefix", resolved: ResolveResult{MatchedBy: "context_similar_0.90", IsNew: false}, total: 3, fullPrompt: fullPrompt, sentPrompt: sentPrompt},
		{name: "full history has no suffix", resolved: ResolveResult{MatchedBy: "context_prefix_3", IsNew: false, HistoryLen: 3}, total: 3, fullPrompt: fullPrompt, sentPrompt: sentPrompt},
		{name: "empty incremental prompt", resolved: ResolveResult{MatchedBy: "context_prefix_2", IsNew: false, HistoryLen: 2}, total: 3, fullPrompt: fullPrompt},
		{name: "full prompt resent", resolved: ResolveResult{MatchedBy: "context_prefix_2", IsNew: false, HistoryLen: 2}, total: 3, fullPrompt: fullPrompt, sentPrompt: fullPrompt},
		{name: "strict prefix suffix sent", resolved: ResolveResult{MatchedBy: "context_prefix_2", IsNew: false, HistoryLen: 2}, total: 3, fullPrompt: fullPrompt, sentPrompt: sentPrompt, want: wantCached},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/responses", nil)
			r, capture := ensureRequestUsageCapture(r)
			recordResolvedCacheUsage(r, tt.resolved, tt.total, tt.fullPrompt, tt.sentPrompt)
			if got := capture.CachedTokens("gpt-5.6-sol"); got != tt.want {
				t.Fatalf("cached tokens=%d want=%d", got, tt.want)
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

func TestRequestResponsesUsageUsesPromptDelta(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r, capture := ensureRequestUsageCapture(r)
	messages := []oaiMsg{
		{Role: "system", Content: "稳定 system context"},
		{Role: "user", Content: "first question 123"},
		{Role: "user", Content: "继续"},
	}
	fullPrompt, _ := flattenPromptMessages(messages, nil)
	sentPrompt, _ := flattenPromptMessages(messages[2:], nil)
	capture.RecordPromptDelta(fullPrompt, sentPrompt)

	estimate := requestResponsesUsage(r, "gpt-5.6-sol", messages, nil, nil, "answer")
	count, _ := tokenEstimator("gpt-5.6-sol")
	want := count(fullPrompt) - count(sentPrompt)
	if cached := responsesCachedTokens(estimate.Values); cached != want {
		t.Fatalf("cached tokens=%d want=%d usage=%#v", cached, want, estimate.Values)
	}
}

func TestRequestResponsesUsageClampsPromptDelta(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/responses", nil)
	r, capture := ensureRequestUsageCapture(r)
	capture.RecordPromptDelta(strings.Repeat("large cached context ", 512), "next")

	estimate := requestResponsesUsage(r, "gpt-5.6-sol", []oaiMsg{{Role: "user", Content: "next"}}, nil, nil, "answer")
	inputTokens := estimate.Values["input_tokens"].(int)
	if cached := responsesCachedTokens(estimate.Values); cached != inputTokens {
		t.Fatalf("cached tokens=%d want clamped input=%d", cached, inputTokens)
	}
}
