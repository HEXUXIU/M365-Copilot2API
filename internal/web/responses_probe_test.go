package web

import (
	"strings"
	"testing"
)

func arithmeticProbeRequest(instructions, question string) responsesRequest {
	return responsesRequest{
		Model:        "gpt-5.6-sol",
		Instructions: instructions,
		Input: []any{map[string]any{
			"role":    "user",
			"content": question,
		}},
		Stream: true,
	}
}

func TestCompactRedundantProbeInstructions(t *testing.T) {
	instructions := strings.Repeat("Codex bootstrap instruction. ", 900)
	question := "Calculate and respond with ONLY the number, nothing else.\n\nQ: 3 + 5 = ?\nA: 8\n\nQ: 12 - 7 = ?\nA: 5\n\nQ: 45 + 27 = ?\nA:"
	r := arithmeticProbeRequest(instructions, question)
	if !compactRedundantProbeInstructions(&r) {
		t.Fatal("known arithmetic probe was not compacted")
	}
	if r.Instructions != "" {
		t.Fatalf("instructions were retained: %d bytes", len(r.Instructions))
	}
	items := r.Input.([]any)
	if got := items[0].(map[string]any)["content"]; got != question {
		t.Fatalf("probe question changed: %q", got)
	}
}

func TestCompactRedundantProbeInstructionsDoesNotAffectNormalRequests(t *testing.T) {
	instructions := strings.Repeat("Codex bootstrap instruction. ", 900)
	question := "Calculate and respond with ONLY the number, nothing else.\n\nQ: 3 + 5 = ?\nA: 8\n\nQ: 12 - 7 = ?\nA: 5\n\nQ: 45 + 27 = ?\nA:"
	tests := map[string]responsesRequest{
		"ordinary task":      arithmeticProbeRequest(instructions, "Implement the requested feature and run its tests."),
		"short instructions": arithmeticProbeRequest("Be concise.", question),
		"tool request": func() responsesRequest {
			r := arithmeticProbeRequest(instructions, question)
			r.Tools = []map[string]any{{"type": "function", "name": "calculator"}}
			return r
		}(),
		"continuation": func() responsesRequest {
			r := arithmeticProbeRequest(instructions, question)
			r.PreviousResponseID = "resp_previous"
			return r
		}(),
		"extra input": func() responsesRequest {
			r := arithmeticProbeRequest(instructions, question)
			r.Input = append(r.Input.([]any), map[string]any{"role": "user", "content": "more"})
			return r
		}(),
	}
	for name, r := range tests {
		t.Run(name, func(t *testing.T) {
			before := r.Instructions
			if compactRedundantProbeInstructions(&r) {
				t.Fatal("normal request was compacted")
			}
			if r.Instructions != before {
				t.Fatal("normal request instructions changed")
			}
		})
	}
}
