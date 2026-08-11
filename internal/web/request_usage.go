package web

import (
	"context"
	"net/http"
	"sync/atomic"

	"m365-copilot2api/internal/chathub"
)

type requestUsageCaptureKey struct{}

type requestUsageCapture struct {
	reusedMessages atomic.Int64
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

func (c *requestUsageCapture) RecordReusedPrefix(historyLen, totalMessages int) {
	if c == nil || historyLen <= 0 || historyLen >= totalMessages {
		return
	}
	for {
		current := c.reusedMessages.Load()
		if int64(historyLen) <= current || c.reusedMessages.CompareAndSwap(current, int64(historyLen)) {
			return
		}
	}
}

func (c *requestUsageCapture) ReusedMessages() int {
	if c == nil {
		return 0
	}
	return int(c.reusedMessages.Load())
}

func recordResolvedCacheUsage(r *http.Request, resolved ResolveResult, totalMessages int) {
	if resolved.IsNew {
		return
	}
	if capture := requestUsageCaptureFrom(r); capture != nil {
		capture.RecordReusedPrefix(resolved.HistoryLen, totalMessages)
	}
}

func requestChatCompletionUsage(r *http.Request, messages []oaiMsg, prompt, output string) map[string]any {
	reusedMessages := 0
	if capture := requestUsageCaptureFrom(r); capture != nil {
		reusedMessages = capture.ReusedMessages()
	}
	return estimateChatCompletionUsage(messages, prompt, output, reusedMessages)
}

func requestResponsesUsage(r *http.Request, model string, messages []oaiMsg, tools []chathub.Tool, toolChoice any, output string) responsesUsageEstimate {
	reusedMessages := 0
	if capture := requestUsageCaptureFrom(r); capture != nil {
		reusedMessages = capture.ReusedMessages()
	}
	return estimateResponsesUsageWithCache(model, messages, tools, toolChoice, output, reusedMessages)
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

func estimateChatCompletionUsage(messages []oaiMsg, prompt, output string, reusedMessages int) map[string]any {
	promptTokens := EstimateTokens(prompt)
	completionTokens := EstimateTokens(output)
	usage := map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"total_tokens":      promptTokens + completionTokens,
	}

	if reusedMessages > len(messages) {
		reusedMessages = len(messages)
	}
	cacheTokens := int64(0)
	for _, message := range messages[:max(reusedMessages, 0)] {
		cacheTokens += EstimateTokens(contentToString(message.Content))
	}
	cacheTokens = min(cacheTokens, promptTokens)
	if cacheTokens > 0 {
		usage["prompt_tokens_details"] = map[string]any{"cached_tokens": cacheTokens}
	}
	return usage
}
