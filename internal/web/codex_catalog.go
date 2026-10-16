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
	Image                                         bool
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
	{ID: "gpt-5.6", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-5.6-reasoning", Owner: "microsoft-365", Tools: true},
	{ID: "gpt-6-sol", Owner: "microsoft-365", DisplayName: "GPT-6 Sol", Tools: true},
	{ID: "gpt-6.1-sol", Owner: "microsoft-365", DisplayName: "GPT-6.1 Sol", Tools: true},
	{ID: "gpt-6-reasoning", Owner: "microsoft-365", DisplayName: "GPT-6 Reasoning", Tools: true},
	{ID: "gpt-6-astra", Owner: "microsoft-365", DisplayName: "GPT-6 Astra", Tools: true},
	{ID: "flux-3", Owner: "microsoft-365", DisplayName: "Flux 3", Image: true},
	{ID: "flux-4", Owner: "microsoft-365", DisplayName: "Flux 4", Image: true},
	{ID: "claude-sonnet", Owner: "anthropic-via-microsoft-365", Tools: true},
	{ID: "claude-sonnet-reasoning", Owner: "anthropic-via-microsoft-365", Tools: true},
	{ID: "claude-sonnet-5", Owner: "anthropic-via-microsoft-365", DisplayName: "Claude Sonnet 5", Tools: true},
	{ID: "claude-opus", Owner: "anthropic-via-microsoft-365", DisplayName: "Claude Opus", Tools: true},
	{ID: "claude-fable", Owner: "anthropic-via-microsoft-365", DisplayName: "Claude Fable", Tools: true},
	{ID: "grok-4.5", Owner: "microsoft-365", DisplayName: "Grok 4.5", Tools: true},
	{ID: "grok-reasoning", Owner: "microsoft-365", DisplayName: "Grok Reasoning", Tools: true},
}

// isImageModel reports whether a public model id routes to the image
// generation pipeline (/v1/images) instead of chat completions.
func isImageModel(model string) bool {
	id := strings.ToLower(strings.TrimSpace(model))
	for _, m := range configuredModelSpecs(currentSettings().ModelMappings) {
		if strings.ToLower(m.ID) == id {
			return m.Image
		}
	}
	return false
}

// isKnownPublicModel reports whether the request model matches a built-in or
// configured public model id (or a supported alias). Unknown ids are still
// accepted for compatibility unless M365_STRICT_MODEL is enabled (issue #79).
func isKnownPublicModel(model string) bool {
	id := strings.ToLower(strings.TrimSpace(model))
	if id == "" {
		return true
	}
	for _, m := range configuredModelSpecs(currentSettings().ModelMappings) {
		if strings.ToLower(m.ID) == id {
			return true
		}
	}
	switch id {
	case "claude", "gpt-5.4-quick", "gpt-5.3-think-deeper",
		"gpt-5.5-quick", "gpt-5.5-think-deeper",
		"gpt-5.6-quick", "gpt-5.6-think-deeper",
		"gpt-6-think-deeper", "gpt-6.0-sol", "gpt6sol", "gpt-6-sol-reasoning", "gpt-6.1-sol-reasoning",
		"grok-auto", "muse-spark", "critique", "claude-fable":
		return true
	}
	if _, ok := dynamicToneForModel(id); ok {
		return true
	}
	return false
}

func strictModelMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("M365_STRICT_MODEL"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func validUpstreamTone(tone string) bool {
	for _, known := range liveUpstreamTones() {
		if tone == known {
			return true
		}
	}
	for _, known := range knownUpstreamTones() {
		if tone == known {
			return true
		}
	}
	return false
}

func knownUpstreamTones() []string {
	return []string{
		"Magic", "Chat", "Reasoning",
		"Gpt_5_2_Auto", "Gpt_5_2_Chat", "Gpt_5_2_Reasoning",
		"Gpt_5_3_Chat", "Gpt_5_3_Reasoning",
		"Gpt_5_4_Chat", "Gpt_5_4_Reasoning",
		"Gpt_5_5_Chat", "Gpt_5_5_Reasoning",
		"Gpt_5_6_Chat", "Gpt_5_6_Reasoning", "GPT_5_6_Chat", "GPT_5_6_Reasoning",
		"Gpt_6_Reasoning", "Gpt_6_Astra", "GPT_6_Astra",
		"Gpt_6_Sol_Chat", "Gpt_6_Sol_Reasoning", "GPT_6_Sol_Chat", "GPT_6_Sol_Reasoning",
		"Gpt_61_Sol_Reasoning",
		"Claude_Sonnet", "Claude_Sonnet_Reasoning", "Claude_Sonnet_5", "Claude_Opus", "Claude_Fable",
		"Grok_4_5", "Grok_Auto", "Grok_Reasoning",
		"Muse_Spark", "Critique",
	}
}

var (
	dynamicTones []string
	dynamicMu    sync.RWMutex
	dynamicAt    time.Time

	upstreamTonePattern = regexp.MustCompile(`\b(?:(?:Gpt|GPT)_[0-9]+(?:_[0-9]+)?_[A-Za-z0-9_]+|Claude_[A-Za-z0-9_]+|Grok_[A-Za-z0-9_]+|Muse_Spark|Critique|Magic)\b`)
	cdnBundleURLPattern = regexp.MustCompile(`https://res\.public\.onecdn\.static\.microsoft/midgard/versionless-v2/[A-Za-z0-9._-]+\.js`)
	mainBundlePattern   = regexp.MustCompile(`main\.[a-f0-9]{8}\.js`)
	digitsOnlyPattern   = regexp.MustCompile(`^[0-9]+$`)
)

func extractBundleURLs(html string) []string {
	seen := map[string]bool{}
	var urls []string
	for _, u := range cdnBundleURLPattern.FindAllString(html, -1) {
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	if m := mainBundlePattern.FindString(html); m != "" {
		u := "https://res.public.onecdn.static.microsoft/midgard/versionless-v2/" + m
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	if len(urls) > 5 {
		urls = urls[:5]
	}
	return urls
}

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
	log.Printf("synced %d upstream tones", len(tones))
}

func fetchUpstreamTones() []string {
	seen := map[string]bool{}
	for _, t := range knownUpstreamTones() {
		seen[t] = true
	}
	discovered := false
	if cloud := GetM365CloudClient(); cloud != nil {
		for _, t := range cloud.DiscoverTones() {
			if !seen[t] {
				discovered = true
			}
			seen[t] = true
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}
	pageURL := "https://m365.cloud.microsoft/"
	if resp, err := client.Get(pageURL); err == nil {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		for _, bundleURL := range extractBundleURLs(string(body)) {
			if resp2, err := client.Get(bundleURL); err == nil {
				bundle, _ := io.ReadAll(io.LimitReader(resp2.Body, 4<<20))
				resp2.Body.Close()
				for _, t := range upstreamTonePattern.FindAllString(string(bundle), -1) {
					if !seen[t] {
						discovered = true
					}
					seen[t] = true
				}
			}
		}
	}
	if !discovered && len(seen) == len(knownUpstreamTones()) {
		return nil
	}
	result := make([]string, 0, len(seen))
	for t := range seen {
		result = append(result, t)
	}
	sort.Strings(result)
	return result
}

// toneToPublicModelSpec converts an upstream M365 tone name (for example a
// newly discovered "Gpt_62_Sol_Reasoning", "Gpt_7_Chat", or "Claude_Sonnet_6")
// into a normalized public OpenAI-compatible modelSpec.
func toneToPublicModelSpec(tone string) (modelSpec, bool) {
	tone = strings.TrimSpace(tone)
	if tone == "" {
		return modelSpec{}, false
	}
	switch strings.ToLower(tone) {
	case "magic", "chat", "reasoning":
		return modelSpec{}, false
	}
	parts := strings.Split(tone, "_")
	if len(parts) < 2 {
		return modelSpec{}, false
	}
	// Skip *_Auto internal routing selectors and uppercase duplicate aliases
	// (e.g. GPT_6_Sol_Reasoning when Gpt_6_Sol_Reasoning is canonical).
	if strings.EqualFold(parts[len(parts)-1], "auto") || parts[0] == "GPT" {
		return modelSpec{}, false
	}
	head := strings.ToLower(parts[0])
	switch head {
	case "gpt":
		ver := ""
		restIdx := 1
		if len(parts) >= 3 && len(parts[1]) == 1 && digitsOnlyPattern.MatchString(parts[1]) && len(parts[2]) == 1 && digitsOnlyPattern.MatchString(parts[2]) {
			ver = parts[1] + "." + parts[2]
			restIdx = 3
		} else if len(parts[1]) == 2 && digitsOnlyPattern.MatchString(parts[1]) {
			ver = parts[1][:1] + "." + parts[1][1:]
			restIdx = 2
		} else if digitsOnlyPattern.MatchString(parts[1]) {
			ver = parts[1]
			restIdx = 2
		} else {
			return modelSpec{}, false
		}
		rest := parts[restIdx:]
		if len(rest) == 0 {
			return modelSpec{ID: "gpt-" + ver, Owner: "microsoft-365", DisplayName: "GPT-" + ver, Tools: true}, true
		}
		if len(rest) == 1 && strings.EqualFold(rest[0], "chat") {
			return modelSpec{ID: "gpt-" + ver, Owner: "microsoft-365", DisplayName: "GPT-" + ver, Tools: true}, true
		}
		if len(rest) == 1 && strings.EqualFold(rest[0], "reasoning") {
			return modelSpec{ID: "gpt-" + ver + "-reasoning", Owner: "microsoft-365", DisplayName: "GPT-" + ver + " Reasoning", Tools: true}, true
		}
		if len(rest) == 2 && strings.EqualFold(rest[1], "reasoning") {
			sub := strings.ToLower(rest[0])
			return modelSpec{ID: "gpt-" + ver + "-" + sub, Owner: "microsoft-365", DisplayName: "GPT-" + ver + " " + rest[0], Tools: true}, true
		}
		lowerRest := make([]string, len(rest))
		for i, p := range rest {
			lowerRest[i] = strings.ToLower(p)
		}
		return modelSpec{
			ID:          "gpt-" + ver + "-" + strings.Join(lowerRest, "-"),
			Owner:       "microsoft-365",
			DisplayName: "GPT-" + ver + " " + strings.Join(rest, " "),
			Tools:       true,
		}, true

	case "claude":
		var idParts, nameParts []string
		for i := 1; i < len(parts); i++ {
			if digitsOnlyPattern.MatchString(parts[i]) && i+1 < len(parts) && digitsOnlyPattern.MatchString(parts[i+1]) {
				ver := parts[i] + "." + parts[i+1]
				idParts = append(idParts, ver)
				nameParts = append(nameParts, ver)
				i++
				continue
			}
			idParts = append(idParts, strings.ToLower(parts[i]))
			nameParts = append(nameParts, parts[i])
		}
		if len(idParts) == 0 {
			return modelSpec{}, false
		}
		return modelSpec{
			ID:          "claude-" + strings.Join(idParts, "-"),
			Owner:       "anthropic-via-microsoft-365",
			DisplayName: "Claude " + strings.Join(nameParts, " "),
			Tools:       true,
		}, true

	default:
		var idParts, nameParts []string
		for i := 0; i < len(parts); i++ {
			if i > 0 && digitsOnlyPattern.MatchString(parts[i]) && i+1 < len(parts) && digitsOnlyPattern.MatchString(parts[i+1]) {
				ver := parts[i] + "." + parts[i+1]
				idParts = append(idParts, ver)
				nameParts = append(nameParts, ver)
				i++
				continue
			}
			idParts = append(idParts, strings.ToLower(parts[i]))
			nameParts = append(nameParts, parts[i])
		}
		return modelSpec{
			ID:          strings.Join(idParts, "-"),
			Owner:       "microsoft-365",
			DisplayName: strings.Join(nameParts, " "),
			Tools:       true,
		}, true
	}
}

// dynamicToneForModel resolves either a raw upstream tone name (or "tone:..."
// prefix) or an auto-generated public model ID back to its upstream tone.
func dynamicToneForModel(model string) (string, bool) {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "tone:") {
		raw := strings.TrimSpace(trimmed[5:])
		if raw != "" {
			return raw, true
		}
	}
	low := strings.ToLower(trimmed)
	for _, tone := range liveUpstreamTones() {
		if strings.EqualFold(tone, trimmed) {
			return tone, true
		}
	}
	for _, tone := range liveUpstreamTones() {
		spec, ok := toneToPublicModelSpec(tone)
		if !ok {
			continue
		}
		specID := strings.ToLower(spec.ID)
		if specID == low || specID+"-reasoning" == low {
			return tone, true
		}
	}
	return "", false
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
	baseline := map[string]bool{}
	for _, t := range knownUpstreamTones() {
		baseline[strings.ToLower(t)] = true
	}
	for _, tone := range liveUpstreamTones() {
		if baseline[strings.ToLower(tone)] {
			continue
		}
		spec, ok := toneToPublicModelSpec(tone)
		if !ok {
			continue
		}
		exists := false
		for i := range models {
			if strings.EqualFold(models[i].ID, spec.ID) {
				exists = true
				break
			}
		}
		if !exists {
			models = append(models, spec)
		}
	}
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
	id := strings.ToLower(strings.TrimSpace(model))
	// Explicit reasoning or pinned selector aliases are never silently downgraded
	// or cross-routed by a generic client default reasoning_effort.
	if strings.Contains(id, "reasoning") || strings.Contains(id, "think-deeper") ||
		strings.Contains(id, "sol") || strings.Contains(id, "astra") ||
		strings.Contains(id, "opus") || strings.HasPrefix(id, "claude-sonnet-5") ||
		strings.HasPrefix(id, "grok") || id == "muse-spark" || id == "critique" || id == "claude-fable" {
		return base, nil
	}
	if e == "" || e == "none" || e == "minimal" || e == "low" {
		return base, nil
	}
	switch id {
	case "claude", "claude-sonnet", "claude-sonnet-4.6":
		return "Claude_Sonnet_Reasoning", nil
	case "gpt-5.2", "gpt-5.2-quick":
		return "Gpt_5_2_Reasoning", nil
	case "gpt-5.3", "gpt-5.3-quick":
		return "Gpt_5_3_Reasoning", nil
	case "gpt-5.4", "gpt-5.4-quick":
		return "Gpt_5_4_Reasoning", nil
	case "gpt-5.5", "gpt-5.5-quick":
		return "Gpt_5_5_Reasoning", nil
	case "gpt-5.6", "gpt-5.6-quick":
		return "Gpt_5_6_Reasoning", nil
	case "gpt-6":
		return "Gpt_6_Reasoning", nil
	default:
		if base != "magic" {
			if strings.HasSuffix(base, "_Chat") {
				candidate := strings.TrimSuffix(base, "_Chat") + "_Reasoning"
				if validUpstreamTone(candidate) {
					return candidate, nil
				}
			}
			return base, nil
		}
		return "Gpt_5_5_Reasoning", nil
	}
}

// effectiveScenarioAndLicense upgrades the WS scenario/licenseType to
// OfficeWebPaidCopilot + Premium when the requested model or tone is gated on
// the paid scenario (e.g. Gpt_6_Reasoning, Claude_Sonnet_5) and the runtime
// setting is still at the default included scenario. Ungated models
// (gpt-6-sol, gpt-6.1-sol, claude-opus, etc.) keep the configured scenario so
// they work on non-premium accounts and avoid spending priority-access quotas
// unnecessarily.
func effectiveScenarioAndLicense(model, tone, cfgScenario, cfgLicense string) (string, string) {
	if cfgScenario != "" && cfgScenario != "OfficeWebIncludedCopilot" {
		return cfgScenario, cfgLicense
	}
	id := strings.ToLower(strings.TrimSpace(model))
	switch id {
	case "gpt-6", "gpt-6-reasoning", "gpt-6-think-deeper",
		"claude-sonnet-5":
		return "OfficeWebPaidCopilot", "Premium"
	case "claude-opus", "claude-sonnet", "claude-fable", "gpt-6-sol", "gpt-6.1-sol", "gpt-6-astra":
		return cfgScenario, cfgLicense
	}
	switch tone {
	case "Gpt_6_Reasoning", "Claude_Sonnet_5":
		return "OfficeWebPaidCopilot", "Premium"
	}
	return cfgScenario, cfgLicense
}
func modelCatalog() []map[string]any {
	l := configuredModelLimits()
	models := configuredModelSpecs(currentSettings().ModelMappings)
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		// Keep capability fields both at the top level and under capabilities:
		// different OpenAI-compatible clients inspect different locations.
		// Image models expose image output only and no chat/tools/reasoning.
		features := []string{"tools", "function_calling", "streaming", "reasoning", "vision"}
		modalities := []string{"text", "image"}
		outputModalities := []string{"text"}
		chat := true
		tools := true
		reasoning := true
		if m.Image {
			features = []string{"image_generation", "vision"}
			outputModalities = []string{"image"}
			chat, tools, reasoning = false, false, false
		}
		caps := map[string]any{
			"chat_completions": chat, "responses": true, "streaming": true,
			"tools": tools, "reasoning": reasoning,
			"reasoning_efforts": advertisedReasoningEfforts, "supported_reasoning_levels": advertisedReasoningEfforts,
			"reasoning_mode": "gateway_tone_routing", "supports_tools": tools, "tool_calls": tools,
			"function_calling": tools, "supports_function_calling": tools, "supports_vision": true,
			"vision": true, "modalities": modalities, "input_modalities": modalities,
			"output_modalities": outputModalities, "supported_features": features,
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
			"capabilities": caps, "supports_tools": tools, "tool_calls": tools,
			"supported_reasoning_levels": advertisedReasoningEfforts,
			"function_calling":           tools, "supports_function_calling": tools, "supports_vision": true,
			"vision": true, "modalities": modalities, "input_modalities": modalities,
			"output_modalities": outputModalities, "supported_features": features,
		})
	}
	return out
}
