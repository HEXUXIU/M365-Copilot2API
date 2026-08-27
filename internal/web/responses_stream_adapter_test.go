package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func responsesInnerStream(lines ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range lines {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
	}
}

func TestStreamResponsesAdapterCommitsBeforeCompletedIsVisible(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	called := false
	ok := s.streamResponsesAdapterWithRunnerAndCompletion(w, r, oaiReq{}, "gpt-5.6-sol", "resp_terminal", "session", "tenant", responsesInnerStream(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	), func(terminal []byte) error {
		called = true
		if strings.Contains(w.Body.String(), "event: response.completed") {
			t.Fatal("response.completed was visible before state commit")
		}
		if !strings.Contains(string(terminal), "event: response.completed") {
			t.Fatalf("commit hook did not receive terminal event: %s", terminal)
		}
		return nil
	})
	if !ok || !called || !strings.Contains(w.Body.String(), "event: response.completed") {
		t.Fatalf("ok=%v called=%v body=%s", ok, called, w.Body.String())
	}
}

func TestStreamResponsesAdapterCarriesExplicitToolRequirement(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	seen := false
	run := func(w http.ResponseWriter, r *http.Request) {
		seen = explicitToolRequirementFromContext(r.Context())
		responsesInnerStream(
			`{"choices":[{"delta":{"content":"done"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		)(w, r)
	}
	if ok := s.streamResponsesAdapterWithRunner(w, r, oaiReq{ExplicitToolRequired: true}, "gpt-5.6-sol", "resp_required", "session", "tenant", run); !ok {
		t.Fatalf("adapter failed: %s", w.Body.String())
	}
	if !seen {
		t.Fatal("explicit tool requirement was lost in the inner stream request")
	}
}

func TestStreamResponsesAdapterMarksInnerResponsesRequest(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	seen := false
	run := func(w http.ResponseWriter, r *http.Request) {
		seen = responsesAdapterFromContext(r.Context())
		responsesInnerStream(
			`{"choices":[{"delta":{"content":"done"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"x_m365_final_text":"done"}`,
			`[DONE]`,
		)(w, r)
	}
	if ok := s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_context", "session", "tenant", run); !ok {
		t.Fatalf("adapter failed: %s", w.Body.String())
	}
	if !seen {
		t.Fatal("inner request was not marked as a Responses adapter call")
	}
}

func TestStreamResponsesAdapterUsesAuthoritativeFinalText(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	partial := "已成功创建文件。\n\n[下载"
	final := "已成功创建文件。\n\n[下载文件](/tmp/outputs/probe.txt)"
	run := responsesInnerStream(
		mustJSON(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": partial}}}}),
		mustJSON(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}}, "x_m365_final_text": final}),
		`[DONE]`,
	)
	if ok := s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_authoritative", "session", "tenant", run); !ok {
		t.Fatalf("adapter failed: %s", w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, `"delta":"`+partial+`"`) {
		t.Fatalf("partial upstream text escaped the tail buffer: %s", body)
	}
	if !strings.Contains(body, mustJSON(final)) || !strings.Contains(body, "event: response.completed") {
		t.Fatalf("authoritative final text missing: %s", body)
	}
	node := s.responseMessages["tenant"]["resp_authoritative"]
	if node == nil {
		t.Fatal("authoritative response was not stored")
	}
	if got := contentToString(node.Messages[len(node.Messages)-1].Content); got != final {
		t.Fatalf("stored response text=%q, want %q", got, final)
	}
}

func TestStreamResponsesAdapterFailsWhenRevisionPrecedesBufferedTail(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	partial := strings.Repeat("a", responsesTextTailBytes+64)
	final := strings.Repeat("b", responsesTextTailBytes+64)
	run := responsesInnerStream(
		mustJSON(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": partial}}}}),
		mustJSON(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}}, "x_m365_final_text": final}),
		`[DONE]`,
	)
	if ok := s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_diverged", "session", "tenant", run); ok {
		t.Fatalf("irreconcilable stream was accepted: %s", w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "stream_reconciliation_failed") || strings.Contains(body, "event: response.completed") {
		t.Fatalf("irreconcilable stream terminal handling is wrong: %s", body)
	}
	if _, ok := s.responseMessages["tenant"]["resp_diverged"]; ok {
		t.Fatal("irreconcilable response was stored for reuse")
	}
}

func TestStreamResponsesAdapterCommitFailureSuppressesCompleted(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	ok := s.streamResponsesAdapterWithRunnerAndCompletion(w, r, oaiReq{}, "gpt-5.6-sol", "resp_terminal", "session", "tenant", responsesInnerStream(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	), func([]byte) error { return errors.New("lost lease") })
	if ok || strings.Contains(w.Body.String(), "event: response.completed") || !strings.Contains(w.Body.String(), "event: response.failed") {
		t.Fatalf("commit failure terminal handling is wrong: %s", w.Body.String())
	}
}

func newResponsesAdapterTestServer() *Server {
	return &Server{responseMessages: make(map[string]map[string]*RespNode)}
}

func TestStreamResponsesAdapterPreservesMixedTextAndToolOutput(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	run := responsesInnerStream(
		`{"choices":[{"delta":{"role":"assistant","content":"I will check. "}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Paris\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`[DONE]`,
	)
	s.streamResponsesAdapterWithRunner(w, r, oaiReq{Messages: []oaiMsg{{Role: "user", Content: "weather"}}}, "gpt-5.6-sol", "resp_mixed", "session", "tenant", run)
	body := w.Body.String()
	for _, want := range []string{"event: response.output_text.done", "event: response.function_call_arguments.done", "event: response.completed", `"type":"message"`, `"type":"function_call"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("mixed stream missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "event: response.failed") {
		t.Fatalf("mixed stream failed: %s", body)
	}
	node := s.responseMessages["tenant"]["resp_mixed"]
	if node == nil || len(node.Messages) != 2 || node.Messages[1].Content != "I will check. " || len(node.Messages[1].ToolCalls) != 1 || len(node.ToolCalls) != 1 {
		t.Fatalf("mixed history=%#v", node)
	}
}

func TestStreamResponsesAdapterStoresInTenantSessionNamespace(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	namespace := responseNamespace("tenant", "session")
	s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_namespaced", "session", namespace, responsesInnerStream(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	))
	node := s.responseMessages[namespace]["resp_namespaced"]
	if node == nil {
		t.Fatalf("stream response was not stored in %q", namespace)
	}
	if node.Tenant != "tenant" || node.SessionID != "session" {
		t.Fatalf("stored identity=%#v", node)
	}
	if s.responseMessages["tenant"] != nil {
		t.Fatal("stream response leaked into the tenant-only namespace")
	}
}

func TestStreamResponsesAdapterRejectsMissingTerminal(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_cut", "session", "tenant", responsesInnerStream(
		`{"choices":[{"delta":{"content":"partial"}}]}`,
	))
	body := w.Body.String()
	if !strings.Contains(body, "event: response.failed") || strings.Contains(body, "event: response.completed") {
		t.Fatalf("truncated stream terminal handling is wrong: %s", body)
	}
	if _, ok := s.responseMessages["tenant"]["resp_cut"]; ok {
		t.Fatal("truncated response was stored for reuse")
	}
}

func TestStreamResponsesAdapterPropagatesInnerErrorChunk(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_error", "session", "tenant", responsesInnerStream(
		`{"error":{"message":"upstream transport failed"}}`,
		`[DONE]`,
	))
	body := w.Body.String()
	if !strings.Contains(body, "event: response.failed") || !strings.Contains(body, "upstream transport failed") || strings.Contains(body, "event: response.completed") {
		t.Fatalf("inner error handling is wrong: %s", body)
	}
}
