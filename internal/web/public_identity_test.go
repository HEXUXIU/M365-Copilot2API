package web

import (
	"encoding/json"
	"m365-copilot2api/internal/chathub"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApplyPublicIdentityPolicyPreservesPromptAndIsIdempotent(t *testing.T) {
	prompt := "[user]\nWhat model are you?"

	got := applyPublicIdentityPolicy(prompt)
	if !strings.Contains(got, prompt) {
		t.Fatalf("policy removed original prompt: %q", got)
	}
	if !strings.Contains(got, "GPT-5-series AI assistant") {
		t.Fatalf("policy does not define the public identity: %q", got)
	}
	if twice := applyPublicIdentityPolicy(got); twice != got {
		t.Fatalf("policy application is not idempotent:\nfirst:  %q\nsecond: %q", got, twice)
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
	input := "I am M365 Copilot, also called Microsoft 365 Copilot, Microsoft365Copilot, M365Copilot, or Microsoft Copilot. Copilot can help."

	got := sanitizePublicAssistantText(input)
	lower := strings.ToLower(got)
	for _, forbidden := range []string{"m365", "microsoft 365", "microsoft365", "copilot"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("sanitized text still contains %q: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, publicAssistantIdentity) {
		t.Fatalf("sanitized text does not contain the neutral identity: %q", got)
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
	if strings.Count(got.String(), publicAssistantIdentity) != 3 {
		t.Fatalf("expected all provider identities to be replaced: %q", got.String())
	}
}

func TestProtocolAdaptersSanitizeAssistantIdentity(t *testing.T) {
	src := map[string]any{"choices": []any{map[string]any{"message": map[string]any{
		"content":           "I am M365 Copilot.",
		"reasoning_content": "Microsoft 365 Copilot identity",
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
