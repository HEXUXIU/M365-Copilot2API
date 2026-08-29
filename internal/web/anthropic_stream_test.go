package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicStreamForwardsShortTextBeforeCompletion(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	w := newSynchronizedStreamRecorder()
	release := make(chan struct{})
	done := make(chan bool, 1)
	run := func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		<-release
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}
	go func() {
		_, ok := s.streamAnthropicAdapterWithRunner(w, r, oaiReq{}, "claude-sonnet", reuseUsage{PromptTokens: 4}, run)
		done <- ok
	}()
	waitForStreamText(t, w, `"text":"hi","type":"text_delta"`)
	select {
	case <-done:
		t.Fatal("adapter completed before the upstream stream was released")
	default:
	}
	close(release)
	if ok := <-done; !ok {
		t.Fatalf("adapter failed: %s", w.String())
	}
	body := w.String()
	for _, want := range []string{"event: message_start", "event: content_block_start", "event: content_block_stop", "event: message_delta", "event: message_stop"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Anthropic stream missing %q: %s", want, body)
		}
	}
}

func TestAnthropicStreamWaitsForCompleteToolIdentity(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	w := httptest.NewRecorder()
	run := responsesInnerStream(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":"{\"city\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_weather","function":{"name":"weather","arguments":"\"Paris\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`[DONE]`,
	)
	usage, ok := s.streamAnthropicAdapterWithRunner(w, r, oaiReq{}, "claude-sonnet", reuseUsage{}, run)
	if !ok {
		t.Fatalf("adapter failed: %s", w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"id":"call_weather"`, `"name":"weather"`, `"partial_json":"{\"city\":"`, `"partial_json":"\"Paris\"}"`, `"stop_reason":"tool_use"`, "event: message_stop"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Anthropic tool stream missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"id":""`) || strings.Contains(body, `"name":""`) || usage.PromptTokens != 10 || usage.CompletionTokens != 5 {
		t.Fatalf("invalid tool stream or usage=%+v: %s", usage, body)
	}
}
