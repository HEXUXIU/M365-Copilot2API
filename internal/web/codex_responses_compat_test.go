package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"m365-native/internal/chathub"
)

func TestParseContentAcceptsResponsesTextBlocks(t *testing.T) {
	content := []any{
		map[string]any{"type": "input_text", "text": "input"},
		map[string]any{"type": "output_text", "text": " output"},
	}
	text, files := parseContent(content)
	if text != "input output" || len(files) != 0 {
		t.Fatalf("text=%q files=%#v", text, files)
	}
}

func TestResponsesUsageEstimateIsNonZeroForText(t *testing.T) {
	usage := estimateResponsesUsage("gpt-5.5", []oaiMsg{{Role: "user", Content: "hello"}}, nil, nil, "world").Values
	if usage["input_tokens"].(int) <= 0 || usage["output_tokens"].(int) <= 0 || usage["total_tokens"].(int) <= 0 {
		t.Fatalf("usage=%#v", usage)
	}
}

func TestResponsesGPTUsageUsesOfflineTiktoken(t *testing.T) {
	input := "这是用于验证 GPT tokenizer 的中文和 code: func main() {}"
	estimate := estimateResponsesUsage("gpt-5.5", []oaiMsg{{Role: "user", Content: input}}, nil, nil, "")
	enc, err := getGPTTokenizer()
	if err != nil {
		t.Fatal(err)
	}
	roleIDs, _, err := enc.Encode("user")
	if err != nil {
		t.Fatal(err)
	}
	inputIDs, _, err := enc.Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	want := requestProtocolTokens + replyPrimingTokens + messageProtocolTokens + len(roleIDs) + len(inputIDs)
	if estimate.Source != usageSourceTiktoken || estimate.Values["input_tokens"] != want {
		t.Fatalf("estimate=%#v want=%d", estimate, want)
	}
}

func TestResponsesUnknownModelUsesHeuristicFallback(t *testing.T) {
	estimate := estimateResponsesUsage("claude-sonnet", []oaiMsg{{Role: "user", Content: "hello"}}, nil, nil, "")
	if estimate.Source != usageSourceHeuristic {
		t.Fatalf("source=%q", estimate.Source)
	}
}

func TestResponsesUsageIncludesToolSchemaAndChoice(t *testing.T) {
	base := estimateResponsesUsage("gpt-5.5", []oaiMsg{{Role: "user", Content: "weather"}}, nil, nil, "")
	tools := []chathub.Tool{{Type: "function", Function: json.RawMessage(`{"name":"weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}`)}}
	withTools := estimateResponsesUsage("gpt-5.5", []oaiMsg{{Role: "user", Content: "weather"}}, tools, map[string]any{"type": "function", "name": "weather"}, "")
	if withTools.Values["input_tokens"].(int) <= base.Values["input_tokens"].(int) {
		t.Fatalf("tool schema and choice were not counted: base=%#v tools=%#v", base, withTools)
	}
}

func TestResponsesResultIncludesUsage(t *testing.T) {
	rr := httptest.NewRecorder()
	writeResponsesResult(rr, "gpt-5.5", false, map[string]any{
		"choices":           []any{map[string]any{"message": map[string]any{"content": "hello"}}},
		"usage":             estimateResponsesUsage("gpt-5.5", []oaiMsg{{Role: "user", Content: "prompt"}}, nil, nil, "hello").Values,
		"m365_usage_source": usageSourceTiktoken,
	})
	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	usage, ok := response["usage"].(map[string]any)
	if !ok || usage["total_tokens"].(float64) <= 0 {
		t.Fatalf("missing usage: %#v", response)
	}
	m365, ok := response["m365"].(map[string]any)
	if !ok || m365["usage_source"] != usageSourceTiktoken || m365["usage_estimate_scope"] != "visible_request_and_completion" {
		t.Fatalf("missing usage source: %#v", response)
	}
}

func TestStreamingResponsesResultIncludesUsage(t *testing.T) {
	rr := httptest.NewRecorder()
	writeResponsesResult(rr, "gpt-5.5", true, map[string]any{
		"choices":           []any{map[string]any{"message": map[string]any{"content": "hello"}}},
		"usage":             estimateResponsesUsage("gpt-5.5", []oaiMsg{{Role: "user", Content: "prompt"}}, nil, nil, "hello").Values,
		"m365_usage_source": usageSourceTiktoken,
	})
	body := rr.Body.String()
	if !strings.Contains(body, "event: response.completed") || !strings.Contains(body, `"total_tokens":`) || !strings.Contains(body, usageSourceTiktoken) {
		t.Fatalf("stream completion missing usage: %s", body)
	}
}

func TestResponsesStreamEmitsFailedForInnerRequestError(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","input":[],"stream":true}`))
	w := httptest.NewRecorder()
	s.responses(w, r)
	body := w.Body.String()
	for _, want := range []string{"event: response.created", "event: response.failed", `"status":"failed"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
	if strings.Contains(body, "event: response.completed") {
		t.Fatalf("unexpected completion event in %s", body)
	}
}
