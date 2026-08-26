package web

import (
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
