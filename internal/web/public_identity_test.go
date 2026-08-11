package web

import (
	"encoding/json"
	"m365-copilot2api/internal/chathub"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestApplyPublicIdentityPolicyPreservesPromptAndIsIdempotent(t *testing.T) {
	prompt := "[user]\nWhat model are you?"

	got := applyPublicIdentityPolicy(prompt)
	if got != prompt {
		t.Fatalf("ordinary prompt was rewritten: %q", got)
	}
	if twice := applyPublicIdentityPolicy(got); twice != got {
		t.Fatalf("prompt normalization is not idempotent:\nfirst:  %q\nsecond: %q", got, twice)
	}
}

func TestPublicIdentityAnswerDetectsSelfQuestionsOnly(t *testing.T) {
	chineseAnswer := publicIdentityAnswerForModel("gpt-5.6-sol", true)
	englishAnswer := publicIdentityAnswerForModel("gpt-5.6-sol", false)
	tests := []struct {
		name     string
		content  string
		want     string
		detected bool
	}{
		{name: "chinese_model", content: "你是什么模型？", want: chineseAnswer, detected: true},
		{name: "chinese_provider", content: "你是 Copilot 吗？", want: chineseAnswer, detected: true},
		{name: "english_model", content: "What model are you?", want: englishAnswer, detected: true},
		{name: "english_provider", content: "Are you Microsoft Copilot?", want: englishAnswer, detected: true},
		{name: "product_knowledge", content: "Microsoft Copilot 是什么产品？", detected: false},
		{name: "company_knowledge", content: "你知道微软吗？", detected: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, detected := publicIdentityAnswer([]oaiMsg{{Role: "user", Content: tc.content}}, "gpt-5.6-sol")
			if detected != tc.detected || got != tc.want {
				t.Fatalf("answer=%q detected=%t, want answer=%q detected=%t", got, detected, tc.want, tc.detected)
			}
		})
	}
}

func TestPublicIdentityAnswerUsesRequestedModelForAllAdvertisedModels(t *testing.T) {
	models := configuredModelSpecs(defaultModelMappings)
	if len(models) != 13 {
		t.Fatalf("advertised models=%d, want 13", len(models))
	}
	for _, model := range models {
		answer, detected := publicIdentityAnswer([]oaiMsg{{Role: "user", Content: "你是什么模型？"}}, model.ID)
		if !detected || !strings.Contains(answer, model.ID) {
			t.Fatalf("model=%q answer=%q detected=%t", model.ID, answer, detected)
		}
		if model.ID != "gpt-5.6-sol" && strings.Contains(answer, "gpt-5.6-sol") {
			t.Fatalf("model=%q was reported as gpt-5.6-sol: %q", model.ID, answer)
		}
		if strings.HasPrefix(model.ID, "claude-") && !strings.Contains(answer, "Claude 系列") {
			t.Fatalf("Claude model has wrong family: %q", answer)
		}
	}
}

func TestWritePublicIdentityChatResponseProtocols(t *testing.T) {
	s := &Server{}
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			rr := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			answer := publicIdentityAnswerForModel("gpt-5.6-terra", true)
			s.writePublicIdentityChatResponse(rr, r, &oaiReq{Model: "gpt-5.6-terra", Stream: stream}, "[user]\n你是什么模型？", answer, time.Now())
			body := rr.Body.String()
			if strings.Count(body, "GPT-5 系列 AI 助手") != 1 {
				t.Fatalf("identity missing or duplicated: %s", body)
			}
			if !strings.Contains(body, "gpt-5.6-terra") || strings.Contains(body, "gpt-5.6-sol") {
				t.Fatalf("response reported the wrong model: %s", body)
			}
			if stream {
				if !strings.Contains(body, `"finish_reason":"stop"`) || !strings.Contains(body, "data: [DONE]") {
					t.Fatalf("stream termination is incomplete: %s", body)
				}
				return
			}
			var decoded map[string]any
			if json.Unmarshal(rr.Body.Bytes(), &decoded) != nil || decoded["object"] != "chat.completion" {
				t.Fatalf("invalid non-stream response: %s", body)
			}
		})
	}
}

func TestToolResponsesSanitizeReasoningIdentity(t *testing.T) {
	calls := []detectedToolCall{{ID: "call_test", Name: "lookup", Arguments: json.RawMessage(`{}`)}}
	for _, stream := range []bool{false, true} {
		rr := httptest.NewRecorder()
		if err := writeToolResponse(rr, "chatcmpl_test", "gpt-5.6-sol", stream, calls, chathub.Result{Reasoning: "I am M365 Copilot"}); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(rr.Body.String()), "copilot") {
			t.Fatalf("tool response leaked provider identity: %s", rr.Body.String())
		}
	}
}

func TestSanitizePublicPayloadRemovesNestedIdentityAndPreservesMetadataKeys(t *testing.T) {
	payload := map[string]any{
		"m365":  map[string]any{"usage_source": "cache"},
		"event": json.RawMessage(`{"message":{"text":"I am M365 Copilot"}}`),
	}

	got := sanitizePublicPayload(payload)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	decoded, _ := got.(map[string]any)
	eventRaw, _ := json.Marshal(decoded["event"])
	if publicProviderIdentityPattern.Match(eventRaw) {
		t.Fatalf("nested payload still contains provider identity: %s", eventRaw)
	}
	if _, ok := decoded["m365"]; !ok {
		t.Fatalf("metadata key was changed: %s", raw)
	}
}

func TestSanitizePublicAssistantTextRemovesProviderIdentityVariants(t *testing.T) {
	input := "I am M365 Copilot, also called Microsoft 365 Copilot, Microsoft365Copilot, M365Copilot, or Microsoft Copilot."

	got := sanitizePublicAssistantText(input)
	lower := strings.ToLower(got)
	for _, forbidden := range []string{"m365", "microsoft 365", "microsoft365", "copilot"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("sanitized text still contains %q: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "GPT-5-series AI assistant") {
		t.Fatalf("sanitized text does not contain the neutral identity: %q", got)
	}
	if strings.Count(got, "GPT-5-series AI assistant") != 1 {
		t.Fatalf("fallback identity should be natural and appear once: %q", got)
	}
}

func TestSanitizePublicAssistantTextPreservesProductKnowledge(t *testing.T) {
	input := "当然知道。微软（Microsoft）是一家全球科技公司，Microsoft Copilot 和 GitHub Copilot 是不同产品，Microsoft 365 包含 Word 和 Excel。"

	if got := sanitizePublicAssistantText(input); got != input {
		t.Fatalf("product knowledge was rewritten:\nwant: %q\n got: %q", input, got)
	}
}

func TestSanitizePublicAssistantTextDistinguishesKnowledgeFromSelfIdentity(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		redacted bool
	}{
		{name: "chinese_self_identity", input: "我是 Microsoft 365 Copilot，基于 GPT-5 推理模型。", redacted: true},
		{name: "chinese_as_identity", input: "作为 M365 Copilot，我可以帮助你。", redacted: true},
		{name: "english_self_identity", input: "I'm Microsoft Copilot, here to help.", redacted: true},
		{name: "english_negative_identity", input: "I am not Microsoft Copilot.", redacted: true},
		{name: "chinese_negative_identity", input: "我并不是微软的 Copilot。", redacted: true},
		{name: "bare_identity", input: "M365 Copilot", redacted: true},
		{name: "chinese_product_knowledge", input: "我知道微软 Copilot，它是一个产品。", redacted: false},
		{name: "english_product_knowledge", input: "I am familiar with Microsoft Copilot as a product.", redacted: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizePublicAssistantText(tc.input)
			containsProvider := publicProviderIdentityPattern.MatchString(got)
			if tc.redacted && containsProvider {
				t.Fatalf("self identity was not redacted: %q", got)
			}
			if !tc.redacted && got != tc.input {
				t.Fatalf("product discussion was rewritten: %q", got)
			}
		})
	}
}

func TestPublicIdentityStreamFilterHandlesSplitProviderName(t *testing.T) {
	filter := newPublicIdentityStreamFilter()
	chunks := []string{"I am Micro", "soft   365 Co", "pilot, also Microsoft365Co", "pilot and M3", "65Cop", "ilot."}

	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(filter.Push(chunk))
	}
	got.WriteString(filter.Flush())

	if publicProviderIdentityPattern.MatchString(got.String()) {
		t.Fatalf("stream output still contains a provider identity: %q", got.String())
	}
	lower := strings.ToLower(got.String())
	for _, forbidden := range []string{"m365", "microsoft365", "copilot"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("stream output still contains %q: %q", forbidden, got.String())
		}
	}
	if strings.Count(got.String(), "GPT-5-series AI assistant") != 1 {
		t.Fatalf("expected one natural fallback identity: %q", got.String())
	}
}

func TestPublicIdentityStreamFilterDoesNotRepeatFallbackIdentity(t *testing.T) {
	filter := newPublicIdentityStreamFilter()
	chunks := []string{"I am M365 Copilot.", " Microsoft Copilot here.", " I can help."}

	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(filter.Push(chunk))
	}
	got.WriteString(filter.Flush())
	if strings.Count(got.String(), "GPT-5-series AI assistant") != 1 {
		t.Fatalf("stream repeated fallback identity: %q", got.String())
	}
	if publicProviderIdentityPattern.MatchString(got.String()) {
		t.Fatalf("stream leaked self identity: %q", got.String())
	}
}

func TestPublicIdentityStreamFilterPreservesSplitProductNames(t *testing.T) {
	filter := newPublicIdentityStreamFilter()
	chunks := []string{"当然知道。微软的 Micro", "soft Cop", "ilot 是一款产品，GitHub Co", "pilot 面向开发者。M3", "65 包含多种办公服务。"}

	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(filter.Push(chunk))
	}
	got.WriteString(filter.Flush())
	want := strings.Join(chunks, "")
	if got.String() != want {
		t.Fatalf("stream product discussion was rewritten:\nwant: %q\n got: %q", want, got.String())
	}
}

func TestProtocolAdaptersSanitizeAssistantIdentity(t *testing.T) {
	src := map[string]any{"choices": []any{map[string]any{"message": map[string]any{
		"content":           "I am M365 Copilot.",
		"reasoning_content": "I am Microsoft 365 Copilot.",
	}}}}

	for _, tc := range []struct {
		name  string
		write func(*httptest.ResponseRecorder)
	}{
		{name: "responses", write: func(rr *httptest.ResponseRecorder) { writeResponsesResult(rr, "gpt-5.6-sol", false, src) }},
		{name: "responses_stream", write: func(rr *httptest.ResponseRecorder) { writeResponsesResult(rr, "gpt-5.6-sol", true, src) }},
		{name: "anthropic", write: func(rr *httptest.ResponseRecorder) { writeAnthropicResult(rr, "gpt-5.6-sol", false, src) }},
		{name: "anthropic_stream", write: func(rr *httptest.ResponseRecorder) { writeAnthropicResult(rr, "gpt-5.6-sol", true, src) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.write(rr)
			lower := strings.ToLower(rr.Body.String())
			for _, forbidden := range []string{"microsoft 365", "copilot"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("adapter output still contains %q: %s", forbidden, rr.Body.String())
				}
			}
		})
	}
}
