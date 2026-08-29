package chathub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"
)

const (
	maxAutoContinuationAttempts = 2
	continuationOverlapWindow   = 16 << 10
	continuationTailRunes       = 1200
)

var ErrIncompleteCompletion = errors.New("upstream repeatedly closed before completion")

type chatAttempt func(Request, func(string) error, StreamHandler) (Result, error)

func (c *Client) chatWithHandlers(ctx context.Context, acc Account, req Request, onDelta func(string) error, onEvent StreamHandler) (Result, error) {
	return runWithContinuation(ctx, req, onDelta, onEvent, func(next Request, delta func(string) error, event StreamHandler) (Result, error) {
		return c.chatWithHandlersOnce(ctx, acc, next, delta, event)
	})
}

func runWithContinuation(ctx context.Context, req Request, onDelta func(string) error, onEvent StreamHandler, attempt chatAttempt) (Result, error) {
	combined, err := attempt(req, onDelta, onEvent)
	if err != nil || !req.AutoContinue || !combined.Incomplete || resultHasToolCall(combined) {
		return combined, err
	}

	for retry := 1; retry <= maxAutoContinuationAttempts; retry++ {
		if ctx.Err() != nil {
			return combined, ctx.Err()
		}
		log.Printf("chathub continuation attempt=%d/%d conversation=%s text=%d reasoning=%d", retry, maxAutoContinuationAttempts, combined.ConversationID, len(combined.Text), len(combined.Reasoning))
		nextReq := continuationRequest(req, combined)
		textGate := newContinuationGate(combined.Text, onDelta)
		reasoningGate := newContinuationGate(combined.Reasoning, func(value string) error {
			if onEvent == nil {
				return nil
			}
			return onEvent(StreamEvent{Kind: "reasoning", Text: value})
		})
		wrappedEvent := func(event StreamEvent) error {
			if event.Kind == "reasoning" && event.Text != "" {
				return reasoningGate.Push(event.Text)
			}
			if onEvent != nil {
				return onEvent(event)
			}
			return nil
		}

		next, nextErr := attempt(nextReq, textGate.Push, wrappedEvent)
		if flushErr := textGate.Flush(); nextErr == nil && flushErr != nil {
			nextErr = flushErr
		}
		if flushErr := reasoningGate.Flush(); nextErr == nil && flushErr != nil {
			nextErr = flushErr
		}
		if nextErr != nil {
			return combined, nextErr
		}

		mergeContinuationResult(&combined, next)
		if !combined.Incomplete || resultHasToolCall(next) {
			return combined, nil
		}
	}
	return combined, ErrIncompleteCompletion
}

func continuationRequest(original Request, previous Result) Request {
	next := original
	if strings.TrimSpace(previous.Text) == "" {
		next.Text = "The prior turn stopped after internal reasoning without a final answer. Produce the final answer to the original user request now. Do not mention the interruption, internal reasoning, or this instruction."
	} else {
		next.Text = fmt.Sprintf("Continue the assistant answer immediately after the exact tail below. Output only the missing continuation. Do not restart, summarize, repeat earlier wording, mention an interruption, or add a new heading.\n\n<answer_tail>\n%s\n</answer_tail>", tailRunes(previous.Text, continuationTailRunes))
	}
	next.ConversationID = previous.ConversationID
	next.SessionID = previous.SessionID
	next.Attachments = nil
	next.Tools = nil
	next.ToolChoice = "none"
	next.MCPServerURL = ""
	next.PreviousMessages = nil
	next.Started = false
	next.AutoContinue = false
	next.DisablePool = true
	return next
}

func tailRunes(value string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[len(runes)-limit:])
}

func resultHasToolCall(result Result) bool {
	seen := make(map[string]bool)
	for _, raw := range result.Events {
		var value any
		if json.Unmarshal(raw, &value) == nil && len(extractToolEvents(value, seen)) > 0 {
			return true
		}
	}
	return false
}

func mergeContinuationResult(base *Result, next Result) {
	base.Text += continuationNovel(base.Text, next.Text)
	base.Reasoning += continuationNovel(base.Reasoning, next.Reasoning)
	base.Incomplete = next.Incomplete
	base.TerminalReason = next.TerminalReason
	base.Events = append(base.Events, next.Events...)
	base.Normalized = append(base.Normalized, next.Normalized...)
	base.Images = append(base.Images, next.Images...)
	base.SuggestedResponses = append(base.SuggestedResponses, next.SuggestedResponses...)
	base.Scores = append(base.Scores, next.Scores...)
	for key, value := range next.References {
		if base.References == nil {
			base.References = make(map[string]Reference)
		}
		base.References[key] = value
	}
	if next.ConversationID != "" {
		base.ConversationID = next.ConversationID
	}
	if next.SessionID != "" {
		base.SessionID = next.SessionID
	}
	if next.Throttling != nil {
		base.Throttling = next.Throttling
	}
	if next.RawResult != "" {
		base.RawResult = next.RawResult
	}
	if next.MeteringInformation != nil {
		base.MeteringInformation = next.MeteringInformation
	}
	if next.Timestamps.LastTokenReceived != "" {
		base.Timestamps.LastTokenReceived = next.Timestamps.LastTokenReceived
	}
}

func continuationNovel(previous, continuation string) string {
	if previous == "" || continuation == "" {
		return continuation
	}
	window := previous
	if len(window) > continuationOverlapWindow {
		window = window[len(window)-continuationOverlapWindow:]
		for len(window) > 0 && !utf8.RuneStart(window[0]) {
			window = window[1:]
		}
	}
	max := len(window)
	if len(continuation) < max {
		max = len(continuation)
	}
	for overlap := max; overlap > 0; overlap-- {
		if !utf8.RuneStart(window[len(window)-overlap]) || overlap < len(continuation) && !utf8.RuneStart(continuation[overlap]) {
			continue
		}
		if window[len(window)-overlap:] == continuation[:overlap] {
			return continuation[overlap:]
		}
	}
	return continuation
}

type continuationGate struct {
	previous string
	pending  strings.Builder
	emit     func(string) error
	released bool
}

func newContinuationGate(previous string, emit func(string) error) *continuationGate {
	return &continuationGate{previous: previous, emit: emit}
}

func (g *continuationGate) Push(value string) error {
	if value == "" || g.emit == nil {
		return nil
	}
	if g.released {
		return g.emit(value)
	}
	g.pending.WriteString(value)
	buffered := g.pending.String()
	if len(buffered) < continuationOverlapWindow && continuationCouldExtendOverlap(g.previous, buffered) {
		return nil
	}
	g.released = true
	g.pending.Reset()
	return g.emit(continuationNovel(g.previous, buffered))
}

func (g *continuationGate) Flush() error {
	if g.released || g.pending.Len() == 0 || g.emit == nil {
		return nil
	}
	g.released = true
	value := continuationNovel(g.previous, g.pending.String())
	g.pending.Reset()
	return g.emit(value)
}

func continuationCouldExtendOverlap(previous, prefix string) bool {
	if previous == "" || prefix == "" {
		return false
	}
	window := previous
	if len(window) > continuationOverlapWindow {
		window = window[len(window)-continuationOverlapWindow:]
	}
	for start := 0; start < len(window); start++ {
		if !utf8.RuneStart(window[start]) {
			continue
		}
		suffix := window[start:]
		if len(prefix) <= len(suffix) && strings.HasPrefix(suffix, prefix) {
			return true
		}
	}
	return false
}
