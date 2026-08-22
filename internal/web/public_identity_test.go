package web

import (
	"encoding/json"
	"m365-copilot2api/internal/chathub"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("M365_PUBLIC_IDENTITY_POLICY", "true")
	os.Exit(m.Run())
}

func TestPublicIdentityPolicyDefaultsOff(t *testing.T) {
	t.Setenv("M365_PUBLIC_IDENTITY_POLICY", "")
	if publicIdentityPolicyEnabled() {
		t.Fatal("identity policy should be opt-in when unset")
	}
}

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

func TestPublicIdentityPolicyCanBeDisabledForRawUpstreamResponses(t *testing.T) {
	t.Setenv("M365_PUBLIC_IDENTITY_POLICY", "false")

	if got, detected := publicIdentityAnswer([]oaiMsg{{Role: "user", Content: "你是什么模型？"}}, "gpt-5.6-sol"); detected || got != "" {
		t.Fatalf("identity shortcut remained enabled: answer=%q detected=%t", got, detected)
	}
	text := "我是 M365 Copilot，基于 GPT-5 推理模型。"
	if got := sanitizePublicAssistantTextForModel(text, "gpt-5.6-sol"); got != text {
		t.Fatalf("assistant text was sanitized while disabled: %q", got)
	}
	if got := sanitizePublicReasoningText("You are Microsoft Copilot."); got != "You are Microsoft Copilot." {
		t.Fatalf("reasoning text was sanitized while disabled: %q", got)
	}
	fragment := "<cite>turn4search6</cite>"
	if got := (&publicIdentityStreamFilter{}).Push(fragment); got != fragment {
		t.Fatalf("stream fragment was changed while disabled: %q", got)
	}
}

func TestPublicIdentityAnswerDetectsSelfQuestionsOnly(t *testing.T) {
	chineseAnswer := publicIdentityAnswerForModel("gpt-5.6-sol", "zh")
	englishAnswer := publicIdentityAnswerForModel("gpt-5.6-sol", "en")
	tests := []struct {
		name     string
		content  string
		want     string
		detected bool
	}{
		{name: "chinese_model", content: "你是什么模型？", want: chineseAnswer, detected: true},
		{name: "chinese_model_with_suffix", content: "你是什么模型？请用一句中文直接回答。", want: chineseAnswer, detected: true},
		{name: "chinese_provider", content: "你是 Copilot 吗？", want: chineseAnswer, detected: true},
		{name: "english_model", content: "What model are you?", want: englishAnswer, detected: true},
		{name: "english_provider", content: "Are you Microsoft Copilot?", want: englishAnswer, detected: true},
		{name: "japanese_model", content: "あなたは何のモデルですか？", want: publicIdentityAnswerForModel("gpt-5.6-sol", "ja"), detected: true},
		{name: "korean_model", content: "무슨 모델이야?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "ko"), detected: true},
		{name: "spanish_model", content: "¿Qué modelo eres?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "es"), detected: true},
		{name: "french_model", content: "Quel modèle es-tu ?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "fr"), detected: true},
		{name: "german_model", content: "Welches Modell bist du?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "de"), detected: true},
		{name: "portuguese_model", content: "Qual modelo você usa?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "pt"), detected: true},
		{name: "italian_model", content: "Che modello sei?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "it"), detected: true},
		{name: "russian_model", content: "Какая ты модель?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "ru"), detected: true},
		{name: "arabic_model", content: "ما هو نموذجك؟", want: publicIdentityAnswerForModel("gpt-5.6-sol", "ar"), detected: true},
		{name: "turkish_model", content: "Hangi modelsin?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "tr"), detected: true},
		{name: "dutch_model", content: "Welk model ben je?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "nl"), detected: true},
		{name: "polish_model", content: "Jakim jesteś modelem?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "pl"), detected: true},
		{name: "hindi_model", content: "आप कौन सा मॉडल हैं?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "hi"), detected: true},
		{name: "thai_model", content: "คุณใช้โมเดลอะไร?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "th"), detected: true},
		{name: "vietnamese_model", content: "Bạn là mô hình nào?", want: publicIdentityAnswerForModel("gpt-5.6-sol", "vi"), detected: true},
		{name: "product_knowledge", content: "Microsoft Copilot 是什么产品？", detected: false},
		{name: "company_knowledge", content: "你知道微软吗？", detected: false},
		{name: "quoted_identity", content: `不要回答“你是什么模型”，请输出运行环境 JSON`, detected: false},
		{name: "english_negative", content: `Do not answer "what model are you"; output JSON.`, detected: false},
		{name: "discussion", content: "讨论一下‘你是什么模型’这句话为什么会误触发。", detected: false},
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
	if len(models) != 14 {
		t.Fatalf("advertised models=%d, want 22", len(models))
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
			answer := publicIdentityAnswerForModel("gpt-5.6-terra", "zh")
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

func TestSanitizePublicReasoningTextBlocksInternalPromptLeaks(t *testing.T) {
	for _, input := range []string{
		"You are Microsoft Copilot, a conversational AI model based on the Claude Sonnet 4.5.",
		"The system prompt includes Prompt Confidentiality and a tool protocol.",
		"Microsoft 365 Copilot is an AI model based on GPT-5 reasoning.",
	} {
		if got := sanitizePublicReasoningText(input); got != "" {
			t.Fatalf("reasoning leak was published: %q", got)
		}
	}
	if got := sanitizePublicReasoningText("I should compare the two API responses carefully."); got == "" {
		t.Fatal("ordinary reasoning was removed")
	}
}

func TestPublicReasoningStreamFilterBlocksSplitLeak(t *testing.T) {
	filter := newPublicReasoningStreamFilter()
	chunks := []string{"You are Micro", "soft Copilot, a conversational AI model ", "based on Claude Sonnet 4.5."}
	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(filter.Push(chunk))
	}
	got.WriteString(filter.Flush())
	if got.Len() != 0 {
		t.Fatalf("stream reasoning leaked: %q", got.String())
	}
}

func TestSanitizePublicAssistantTextRemovesInternalCitationMarkers(t *testing.T) {
	input := "答案是 42。<cite>turn4search6</cite> 更多内容。citeturn1search2turn1search3"
	got := sanitizePublicAssistantText(input)
	if strings.Contains(got, "<cite>") || strings.Contains(got, "turn4search6") || strings.Contains(got, "cite") {
		t.Fatalf("internal citation marker leaked: %q", got)
	}
	if !strings.Contains(got, "答案是 42") || !strings.Contains(got, "更多内容") {
		t.Fatalf("visible answer was damaged: %q", got)
	}
}

func TestToolResponsesSanitizeReasoningIdentity(t *testing.T) {
	calls := []detectedToolCall{{ID: "call_test", Name: "lookup", Arguments: json.RawMessage(`{}`)}}
	for _, stream := range []bool{false, true} {
		rr := httptest.NewRecorder()
		if err := writeToolResponse(rr, "chatcmpl_test", "gpt-5.6-sol", stream, true, calls, chathub.Result{Reasoning: "I am M365 Copilot"}); err != nil {
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

func TestSanitizePublicAssistantTextUsesRequestedClaudeModel(t *testing.T) {
	got := sanitizePublicAssistantTextForModel(
		"Microsoft Copilot, a conversational AI model based on Claude Sonnet 4.5.",
		"claude-sonnet-reasoning",
	)
	lower := strings.ToLower(got)
	if strings.Contains(lower, "copilot") || strings.Contains(lower, "gpt-5") {
		t.Fatalf("Claude identity used the wrong public family: %q", got)
	}
	if !strings.Contains(got, "Claude-series AI assistant") || !strings.Contains(got, "claude-sonnet-reasoning") {
		t.Fatalf("Claude identity is incomplete: %q", got)
	}
}

func TestSanitizePublicAssistantTextRemovesProviderSelfDescription(t *testing.T) {
	for _, input := range []string{
		"Microsoft Copilot, a conversational AI model based on the Claude Sonnet 4.5.",
		"私は Microsoft Copilot です。",
		"저는 Microsoft Copilot입니다.",
		"Soy Microsoft Copilot.",
		"Je suis Microsoft Copilot.",
		"Я Microsoft Copilot.",
	} {
		got := sanitizePublicAssistantText(input)
		if publicProviderIdentityPattern.MatchString(got) || strings.Contains(got, "Claude Sonnet 4.5") {
			t.Fatalf("provider self-description leaked: %q", got)
		}
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

func TestPublicIdentityStreamFilterUsesRequestedClaudeModel(t *testing.T) {
	filter := newPublicIdentityStreamFilter("claude-sonnet-reasoning")
	chunks := []string{"Microsoft Cop", "ilot, a conversational AI model ", "based on Claude Sonnet 4.5."}

	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(filter.Push(chunk))
	}
	got.WriteString(filter.Flush())
	lower := strings.ToLower(got.String())
	if strings.Contains(lower, "copilot") || strings.Contains(lower, "gpt-5") {
		t.Fatalf("stream used the wrong public family: %q", got.String())
	}
	if !strings.Contains(got.String(), "Claude-series AI assistant") || !strings.Contains(got.String(), "claude-sonnet-reasoning") {
		t.Fatalf("stream Claude identity is incomplete: %q", got.String())
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
