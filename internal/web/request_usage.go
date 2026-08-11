package web

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"m365-copilot2api/internal/chathub"
)

type requestUsageCaptureKey struct{}

type cachePromptDelta struct {
	full string
	sent string
}

type requestUsageCapture struct {
	promptDelta atomic.Pointer[cachePromptDelta]
}

func ensureRequestUsageCapture(r *http.Request) (*http.Request, *requestUsageCapture) {
	if capture := requestUsageCaptureFrom(r); capture != nil {
		return r, capture
	}
	capture := &requestUsageCapture{}
	ctx := context.WithValue(r.Context(), requestUsageCaptureKey{}, capture)
	return r.WithContext(ctx), capture
}

func requestUsageCaptureFrom(r *http.Request) *requestUsageCapture {
	if r == nil {
		return nil
	}
	capture, _ := r.Context().Value(requestUsageCaptureKey{}).(*requestUsageCapture)
	return capture
}

func (c *requestUsageCapture) RecordPromptDelta(fullPrompt, sentPrompt string) {
	if c == nil || strings.TrimSpace(fullPrompt) == "" || strings.TrimSpace(sentPrompt) == "" || fullPrompt == sentPrompt {
		return
	}
	c.promptDelta.CompareAndSwap(nil, &cachePromptDelta{full: fullPrompt, sent: sentPrompt})
}

func (c *requestUsageCapture) CachedTokens(model string) int {
	if c == nil {
		return 0
	}
	delta := c.promptDelta.Load()
	if delta == nil {
		return 0
	}
	count, _ := tokenEstimator(model)
	return max(count(delta.full)-count(delta.sent), 0)
}

func recordResolvedCacheUsage(r *http.Request, resolved ResolveResult, totalMessages int, fullPrompt, sentPrompt string) {
	if resolved.IsNew || resolved.HistoryLen <= 0 || resolved.HistoryLen >= totalMessages {
		return
	}
	if capture := requestUsageCaptureFrom(r); capture != nil {
		capture.RecordPromptDelta(fullPrompt, sentPrompt)
	}
}

func requestChatCompletionUsage(r *http.Request, model, prompt, output string) map[string]any {
	cachedTokens := 0
	if capture := requestUsageCaptureFrom(r); capture != nil {
		cachedTokens = capture.CachedTokens(model)
	}
	return estimateChatCompletionUsage(model, prompt, output, cachedTokens)
}

func requestResponsesUsage(r *http.Request, model string, messages []oaiMsg, tools []chathub.Tool, toolChoice any, output string) responsesUsageEstimate {
	cachedTokens := 0
	if capture := requestUsageCaptureFrom(r); capture != nil {
		cachedTokens = capture.CachedTokens(model)
	}
	return estimateResponsesUsageWithCache(model, messages, tools, toolChoice, output, cachedTokens)
}

func chatCachedTokens(usage map[string]any) int64 {
	details, _ := usage["prompt_tokens_details"].(map[string]any)
	switch value := details["cached_tokens"].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func responsesCachedTokens(usage map[string]any) int {
	details, _ := usage["input_tokens_details"].(map[string]any)
	switch value := details["cached_tokens"].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func estimateChatCompletionUsage(model, prompt, output string, cachedTokens int) map[string]any {
	count, _ := tokenEstimator(model)
	promptTokens := int64(count(prompt))
	completionTokens := int64(count(output))
	usage := map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      promptTokens + completionTokens,
	}

	cacheTokens := min(int64(max(cachedTokens, 0)), promptTokens)
	if cacheTokens > 0 {
		usage["prompt_tokens_details"] = map[string]any{"cached_tokens": cacheTokens}
	}
	return usage
}
