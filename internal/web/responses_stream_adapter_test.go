package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type synchronizedStreamRecorder struct {
	header http.Header
	mu     sync.Mutex
	body   strings.Builder
}

func newSynchronizedStreamRecorder() *synchronizedStreamRecorder {
	return &synchronizedStreamRecorder{header: make(http.Header)}
}

func (w *synchronizedStreamRecorder) Header() http.Header { return w.header }
func (w *synchronizedStreamRecorder) WriteHeader(int)     {}
func (w *synchronizedStreamRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(p)
}
func (w *synchronizedStreamRecorder) Flush() {}
func (w *synchronizedStreamRecorder) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

func waitForStreamText(t *testing.T, w *synchronizedStreamRecorder, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.String(), want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("stream did not contain %q before deadline: %s", want, w.String())
}

func responsesInnerStream(lines ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range lines {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
	}
}

func TestStreamResponsesAdapterForwardsShortDeltaBeforeCompletion(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := newSynchronizedStreamRecorder()
	release := make(chan struct{})
	done := make(chan bool, 1)
	run := func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		<-release
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}
	go func() {
		done <- s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_live", "session", "tenant", run)
	}()
	waitForStreamText(t, w, `"delta":"hi"`)
	select {
	case <-done:
		t.Fatal("adapter completed before the upstream stream was released")
	default:
	}
	close(release)
	if ok := <-done; !ok {
		t.Fatalf("adapter failed: %s", w.String())
	}
}

func TestStreamResponsesAdapterDropsReasoningAfterContent(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	run := responsesInnerStream(
		`{"choices":[{"delta":{"content":"first "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"The cache trace shows a tenant mismatch."}}]}`,
		`{"choices":[{"delta":{"content":"second"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)
	if ok := s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_late_reasoning", "session", "tenant", run); !ok {
		t.Fatalf("adapter failed: %s", w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "response.reasoning_summary") || strings.Contains(body, "tenant mismatch") {
		t.Fatalf("late reasoning was exposed: %s", body)
	}
	if !strings.Contains(body, `"text":"first second"`) {
		t.Fatalf("content was damaged: %s", body)
	}
}

func TestStreamResponsesAdapterEmitsReasoningBeforeContent(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	run := responsesInnerStream(
		`{"choices":[{"delta":{"reasoning_content":"Verifying algebraic bounds. "}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"Comparing x=2 and x=3 eliminates a branch."}}]}`,
		`{"choices":[{"delta":{"content":"first "}}]}`,
		`{"choices":[{"delta":{"content":"second"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)
	if ok := s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_ordered_reasoning", "session", "tenant", run); !ok {
		t.Fatalf("adapter failed: %s", w.Body.String())
	}
	body := w.Body.String()
	reasoningAt := strings.Index(body, "event: response.reasoning_summary_text.delta")
	contentAt := strings.Index(body, "event: response.output_text.delta")
	if reasoningAt < 0 || contentAt <= reasoningAt {
		t.Fatalf("reasoning/content order is invalid: %s", body)
	}
}

func TestStreamResponsesAdapterOmitsFilteredReasoningItem(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	run := responsesInnerStream(
		`{"choices":[{"delta":{"reasoning_content":"In pro"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"gress..."}}]}`,
		`{"choices":[{"delta":{"content":"answer"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)
	if ok := s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_empty_reasoning", "session", "tenant", run); !ok {
		t.Fatalf("adapter failed: %s", w.Body.String())
	}
	if body := w.Body.String(); strings.Contains(body, `"type":"reasoning"`) || strings.Contains(body, "reasoning_summary") {
		t.Fatalf("filtered reasoning created an empty item: %s", body)
	}
}

func TestStreamResponsesAdapterHeartbeatsDuringUpstreamSilence(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := newSynchronizedStreamRecorder()
	release := make(chan struct{})
	done := make(chan bool, 1)
	run := func(w http.ResponseWriter, _ *http.Request) {
		<-release
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}
	go func() {
		done <- s.streamResponsesAdapterWithRunnerAndCompletionInterval(w, r, oaiReq{}, "gpt-5.6-sol", "resp_heartbeat", "session", "tenant", run, nil, 5*time.Millisecond)
	}()
	waitForStreamText(t, w, ": keepalive\n\n")
	close(release)
	if ok := <-done; !ok {
		t.Fatalf("adapter failed: %s", w.String())
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
	if !strings.Contains(body, `"delta":`+mustJSON(partial)) {
		t.Fatalf("upstream delta was not forwarded immediately: %s", body)
	}
	if !strings.Contains(body, `"delta":`+mustJSON("文件](/tmp/outputs/probe.txt)")) || !strings.Contains(body, mustJSON(final)) || !strings.Contains(body, "event: response.completed") {
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
	partial := strings.Repeat("a", 64)
	final := strings.Repeat("b", 64)
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
	if strings.Contains(body, `"call_id":""`) || strings.Contains(body, `"name":""`) {
		t.Fatalf("tool item was exposed before its identity was complete: %s", body)
	}
	node := s.responseMessages["tenant"]["resp_mixed"]
	if node == nil || len(node.Messages) != 2 || node.Messages[1].Content != "I will check. " || len(node.Messages[1].ToolCalls) != 1 || len(node.ToolCalls) != 1 {
		t.Fatalf("mixed history=%#v", node)
	}
}

func TestStreamResponsesAdapterEmitsCustomApplyPatchLifecycle(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	patch := "*** Begin Patch\n*** Add File: 1.txt\n+123214324\n*** End Patch"
	arguments := mustJSON(map[string]any{"input": patch})
	run := responsesInnerStream(
		mustJSON(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": "call_apply_patch", "type": "custom",
			"function": map[string]any{"name": "apply_patch", "arguments": arguments[:24]},
		}}}}}}),
		mustJSON(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "function": map[string]any{"arguments": arguments[24:]},
		}}}}}}),
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	)
	if ok := s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_apply_patch", "session", "tenant", run); !ok {
		t.Fatalf("adapter failed: %s", w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"event: response.output_item.added",
		`"type":"custom_tool_call"`,
		`"call_id":"call_apply_patch"`,
		`"name":"apply_patch"`,
		"event: response.custom_tool_call_input.delta",
		"event: response.custom_tool_call_input.done",
		mustJSON(patch),
		"event: response.output_item.done",
		"event: response.completed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("custom apply_patch stream missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "event: response.failed") {
		t.Fatalf("custom apply_patch stream failed: %s", body)
	}
	node := s.responseMessages["tenant"]["resp_apply_patch"]
	if node == nil || len(node.Messages) != 1 || len(node.Messages[0].ToolCalls) != 1 {
		t.Fatalf("custom apply_patch history=%#v", node)
	}
	call := node.Messages[0].ToolCalls[0]
	fn, _ := call["function"].(map[string]any)
	if call["type"] != "custom" || fn["name"] != "apply_patch" || fn["arguments"] != arguments {
		t.Fatalf("stored custom apply_patch call=%#v", call)
	}
}

func TestStreamResponsesAdapterWaitsForSplitToolIdentity(t *testing.T) {
	s := newResponsesAdapterTestServer()
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	w := httptest.NewRecorder()
	run := responsesInnerStream(
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":"{\"city\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_split","function":{"name":"weather","arguments":"\"Paris\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	)
	if ok := s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp_split_tool", "session", "tenant", run); !ok {
		t.Fatalf("adapter failed: %s", w.Body.String())
	}
	body := w.Body.String()
	added := strings.Index(body, "event: response.output_item.added")
	delta := strings.Index(body, "event: response.function_call_arguments.delta")
	if added < 0 || delta < added {
		t.Fatalf("tool item was not announced before argument deltas: %s", body)
	}
	for _, want := range []string{`"call_id":"call_split"`, `"name":"weather"`, `"arguments":"{\"city\":\"Paris\"}"`, "event: response.completed"} {
		if !strings.Contains(body, want) {
			t.Fatalf("split tool stream missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"call_id":""`) || strings.Contains(body, `"name":""`) || strings.Contains(body, `"delta":""`) {
		t.Fatalf("split tool stream exposed incomplete metadata: %s", body)
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
