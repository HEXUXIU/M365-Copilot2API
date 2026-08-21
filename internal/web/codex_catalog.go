// Codex model catalog compatibility lives here. It is intentionally kept in
// package web because route handlers share unexported request and settings types.
package web

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type modelLimits struct{ ContextWindow, MaxInputTokens, MaxOutputTokens int }
type reasoningConfig struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type modelSpec struct {
	ID, Owner, DisplayName, DefaultReasoningLevel string
	Tools                                         bool
}

type reasoningEffortPreset struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

var advertisedReasoningEfforts = []reasoningEffortPreset{
	{Effort: "none", Description: "Disable additional reasoning."},
	{Effort: "minimal", Description: "Fast responses with minimal reasoning."},
	{Effort: "low", Description: "Fast responses with lighter reasoning."},
	{Effort: "medium", Description: "Balances speed and reasoning depth for everyday tasks."},
	{Effort: "high", Description: "Greater reasoning depth for complex problems."},
	{Effort: "xhigh", Description: "Extra high reasoning depth for complex problems."},
}

// gatewayCodexBaseInstructions is returned only in the Codex model catalog.
// Codex uses it to build its own request instructions; it is not interpreted
// or forwarded directly by the gateway's ChatHub adapter.
const gatewayCodexBaseInstructions = `You are a helpful AI assistant. When asked to write code, always provide the complete implementation — never truncate, abbreviate, or return only a fragment. Write full, working code with all logic included.`

func codexModelMessages() map[string]any {
	return map[string]any{
		"instructions_template": gatewayCodexBaseInstructions,
		"instructions_variables": map[string]string{
			"personality_default":   "",
			"personality_friendly":  "",
			"personality_pragmatic": "",
		},
		"approvals":   nil,
		"auto_review": nil,
	}
}

var gatewayModels = []modelSpec{
	{ID: "gpt-5.2", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-5.2-reasoning", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-5.3", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-5.4", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-5.4-reasoning", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-5.5", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-5.5-reasoning", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-5.6-reasoning", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-image-2", Owner: "microsoft-365", DisplayName: "GPT Image 2"},
	{ID: "claude-sonnet", Owner: "anthropic-via-microsoft-365", Tools: true},
	{ID: "claude-sonnet-reasoning", Owner: "anthropic-via-microsoft-365", Tools: true},
}

func validUpstreamTone(tone string) bool {
	for _, known := range liveUpstreamTones() {
		if tone == known {
			return true
		}
	}
	return false
}

func knownUpstreamTones() []string {
	return []string{"Gpt_5_2_Chat", "Gpt_5_2_Reasoning", "Gpt_5_3_Chat", "Gpt_5_3_Reasoning", "Gpt_5_4_Chat", "Gpt_5_4_Reasoning", "Gpt_5_5_Chat", "Gpt_5_5_Reasoning", "Gpt_5_6_Reasoning", "Claude_Sonnet", "Claude_Sonnet_Reasoning"}
}

var (
	dynamicTones []string
	dynamicMu    sync.RWMutex
	dynamicAt    time.Time
)

func liveUpstreamTones() []string {
	dynamicMu.RLock()
	if dynamicAt.IsZero() || time.Since(dynamicAt) > 24*time.Hour {
		dynamicMu.RUnlock()
		go syncUpstreamTones()
		dynamicMu.RLock()
	}
	t := dynamicTones
	dynamicMu.RUnlock()
	if len(t) > 0 {
		return t
	}
	return knownUpstreamTones()
}

func syncUpstreamTones() {
	tones := fetchUpstreamTones()
	if len(tones) == 0 {
		return
	}
	dynamicMu.Lock()
	dynamicTones = tones
	dynamicAt = time.Now()
	dynamicMu.Unlock()
	log.Printf("synced %d upstream tones from CDN bundle", len(tones))
}

func fetchUpstreamTones() []string {
	client := &http.Client{Timeout: 30 * time.Second}
	pageURL := "https://m365.cloud.microsoft/"
	resp, err := client.Get(pageURL)
	if err != nil {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	resp.Body.Close()
	re := regexp.MustCompile(`main\.[a-f0-9]{8}\.js`)
	m := re.FindString(string(body))
	if m == "" {
		return nil
	}
	bundleURL := "https://res.public.onecdn.static.microsoft/midgard/versionless-v2/" + m
	resp2, err := client.Get(bundleURL)
	if err != nil {
		return nil
	}
	bundle, _ := io.ReadAll(io.LimitReader(resp2.Body, 10<<20))
	resp2.Body.Close()
	toneRe := regexp.MustCompile(`(?:Gpt_[0-9]_[0-9]_[A-Za-z_]+|Claude_[A-Za-z0-9_]+|Magic)`)
	matches := toneRe.FindAllString(string(bundle), -1)
	seen := map[string]bool{}
	for _, t := range matches {
		seen[t] = true
	}
	result := make([]string, 0, len(seen))
	for t := range seen {
		result = append(result, t)
	}
	sort.Strings(result)
	return result
}

func configuredModelMapping(model string, mappings []modelMapping) (modelMapping, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, mapping := range mappings {
		if strings.EqualFold(strings.TrimSpace(mapping.PublicModel), model) {
			return mapping, true
		}
	}
	return modelMapping{}, false
}

func configuredModelTone(model string, mappings []modelMapping) (string, bool) {
	mapping, ok := configuredModelMapping(model, mappings)
	if !ok {
		return "", false
	}
	return mapping.UpstreamTone, true
}

func configuredModelSpecs(mappings []modelMapping) []modelSpec {
	models := append([]modelSpec(nil), gatewayModels...)
	for _, mapping := range mappings {
		spec := modelSpec{
			ID: strings.TrimSpace(mapping.PublicModel), Owner: "microsoft-365", Tools: true,
			DisplayName: strings.TrimSpace(mapping.DisplayName), DefaultReasoningLevel: strings.TrimSpace(mapping.DefaultReasoningLevel),
		}
		replaced := false
		for i := range models {
			if strings.EqualFold(models[i].ID, spec.ID) {
				models[i] = spec
				replaced = true
				break
			}
		}
		if !replaced {
			models = append(models, spec)
		}
	}
	return models
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
	if tone, ok := configuredModelTone(model, currentSettings().ModelMappings); ok {
		return tone, nil
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
	case "gpt-5.2":
		return "Gpt_5_2_Reasoning", nil
	case "gpt-5.3":
		return "Gpt_5_3_Reasoning", nil
	case "gpt-5.4":
		return "Gpt_5_4_Reasoning", nil
	case "gpt-5.5":
		return "Gpt_5_5_Reasoning", nil
	case "gpt-5.6":
		return "Gpt_5_6_Reasoning", nil
	default:
		return "Gpt_5_5_Reasoning", nil
	}
}
func modelCatalog() []map[string]any {
	l := configuredModelLimits()
	models := configuredModelSpecs(currentSettings().ModelMappings)
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		// Keep capability fields both at the top level and under capabilities:
		// different OpenAI-compatible clients inspect different locations.
		features := []string{"tools", "function_calling", "streaming", "reasoning", "vision"}
		modalities := []string{"text", "image"}
		caps := map[string]any{
			"chat_completions": true, "responses": true, "streaming": true,
			"tools": true, "reasoning": true,
			"reasoning_efforts": advertisedReasoningEfforts, "supported_reasoning_levels": advertisedReasoningEfforts,
			"reasoning_mode": "gateway_tone_routing", "supports_tools": true, "tool_calls": true,
			"function_calling": true, "supports_function_calling": true, "supports_vision": true,
			"vision": true, "modalities": modalities, "input_modalities": modalities,
			"output_modalities": []string{"text"}, "supported_features": features,
		}
		displayName := m.DisplayName
		if displayName == "" {
			displayName = m.ID
		}
		defaultReasoningLevel := m.DefaultReasoningLevel
		if defaultReasoningLevel == "" {
			defaultReasoningLevel = "medium"
		}
		out = append(out, map[string]any{
			"id": m.ID, "slug": m.ID, "display_name": displayName, "description": "Public model endpoint.",
			"base_instructions": gatewayCodexBaseInstructions, "model_messages": codexModelMessages(),
			"default_reasoning_level": defaultReasoningLevel, "object": "model", "owned_by": "gateway",
			"shell_type": "shell_command", "visibility": "list", "supported_in_api": true, "priority": 1,
			"additional_speed_tiers": []string{}, "service_tiers": []any{},
			"availability_nux": nil, "upgrade": nil, "include_skills_usage_instructions": false,
			"supports_reasoning_summaries": true, "default_reasoning_summary": "none",
			"support_verbosity": true, "default_verbosity": "low", "apply_patch_tool_type": "freeform",
			"web_search_tool_type": "text_and_image", "truncation_policy": map[string]any{"mode": "tokens", "limit": 10000},
			"supports_parallel_tool_calls": true, "supports_image_detail_original": true,
			"max_context_window": l.ContextWindow, "effective_context_window_percent": 95,
			"experimental_supported_tools": []any{}, "supports_search_tool": true, "use_responses_lite": false,
			"tool_mode": "code_mode_only", "multi_agent_version": "v2",
			"context_window": l.ContextWindow, "max_input_tokens": l.MaxInputTokens, "max_output_tokens": l.MaxOutputTokens,
			"capabilities": caps, "supports_tools": true, "tool_calls": true,
			"supported_reasoning_levels": advertisedReasoningEfforts,
			"function_calling":           true, "supports_function_calling": true, "supports_vision": true,
			"vision": true, "modalities": modalities, "input_modalities": modalities,
			"output_modalities": []string{"text"}, "supported_features": features,
		})
	}
	return out
}
