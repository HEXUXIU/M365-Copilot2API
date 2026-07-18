package web

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type modelLimits struct{ ContextWindow, MaxInputTokens, MaxOutputTokens int }
type reasoningConfig struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type modelSpec struct {
	ID, Owner string
	Tools     bool
}

var gatewayModels = []modelSpec{
	{ID: "gpt-5.5", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-5.5-reasoning", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-5.6-reasoning", Owner: "microsoft-365", Tools: true},
	{ID: "claude-sonnet", Owner: "anthropic-via-microsoft-365", Tools: true},
	{ID: "claude-sonnet-reasoning", Owner: "anthropic-via-microsoft-365", Tools: true},
}

func positiveEnvInt(name string, fallback int) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err == nil && v > 0 {
		return v
	}
	return fallback
}
func configuredModelLimits() modelLimits {
	cfg := currentSettings()
	contextWindow := cfg.ContextWindow
	maxOutput := cfg.MaxOutputTokens
	if maxOutput >= contextWindow {
		maxOutput = contextWindow / 8
		if maxOutput < 1 {
			maxOutput = 1
		}
	}
	return modelLimits{ContextWindow: contextWindow, MaxInputTokens: contextWindow - maxOutput, MaxOutputTokens: maxOutput}
}
func normalizeReasoningEffort(e string) (string, error) {
	e = strings.ToLower(strings.TrimSpace(e))
	if e == "" {
		return "", nil
	}
	switch e {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return e, nil
	}
	return "", fmt.Errorf("unsupported reasoning effort %q; use none, minimal, low, medium, high, or xhigh", e)
}
func reasoningTone(model, effort string) (string, error) {
	e, err := normalizeReasoningEffort(effort)
	if err != nil {
		return "", err
	}
	base := modelTone(model)
	// Explicit reasoning aliases are never silently downgraded by a generic client default.
	if strings.Contains(strings.ToLower(model), "reasoning") {
		return base, nil
	}
	if e == "" || e == "none" || e == "minimal" || e == "low" {
		return base, nil
	}
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "claude", "claude-sonnet":
		return "Claude_Sonnet_Reasoning", nil
	case "gpt-5.5":
		return "Gpt_5_5_Reasoning", nil
	case "gpt-5.6":
		return "Gpt_5_5_Reasoning", nil
	default:
		return "Gpt_Reasoning", nil
	}
}
func modelCatalog() []map[string]any {
	l := configuredModelLimits()
	out := make([]map[string]any, 0, len(gatewayModels))
	for _, m := range gatewayModels {
		// Keep capability fields both at the top level and under capabilities:
		// different OpenAI-compatible clients inspect different locations.
		features := []string{"tools", "function_calling", "streaming", "reasoning", "vision"}
		modalities := []string{"text", "image"}
		caps := map[string]any{
			"chat_completions": true, "responses": true, "streaming": true,
			"tools": true, "reasoning": true, "reasoning_efforts": []string{"none", "minimal", "low", "medium", "high", "xhigh"},
			"reasoning_mode": "gateway_tone_routing", "supports_tools": true, "tool_calls": true,
			"function_calling": true, "supports_function_calling": true, "supports_vision": true,
			"vision": true, "modalities": modalities, "input_modalities": modalities,
			"output_modalities": []string{"text"}, "supported_features": features,
		}
		out = append(out, map[string]any{
			"id": m.ID, "object": "model", "owned_by": m.Owner,
			"context_window": l.ContextWindow, "max_input_tokens": l.MaxInputTokens, "max_output_tokens": l.MaxOutputTokens,
			"capabilities": caps, "supports_tools": true, "tool_calls": true,
			"function_calling": true, "supports_function_calling": true, "supports_vision": true,
			"vision": true, "modalities": modalities, "input_modalities": modalities,
			"output_modalities": []string{"text"}, "supported_features": features,
		})
	}
	return out
}
