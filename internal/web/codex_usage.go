package web

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"

	tiktoken "github.com/tiktoken-go/tokenizer"

	"m365-copilot2api/internal/chathub"
)

const (
	usageSourceTiktoken  = "tiktoken_o200k_base_estimate"
	usageSourceHeuristic = "heuristic_character_estimate"

	// These cover visible request framing not represented by literal message text.
	// They are conservative estimates, not ChatHub billing-token claims.
	requestProtocolTokens    = 4
	messageProtocolTokens    = 4
	toolProtocolTokens       = 6
	toolChoiceProtocolTokens = 2
	replyPrimingTokens       = 3
	outputProtocolTokens     = 3
)

var (
	gptTokenizerOnce sync.Once
	gptTokenizer     tiktoken.Codec
	gptTokenizerErr  error
)

func getGPTTokenizer() (tiktoken.Codec, error) {
	gptTokenizerOnce.Do(func() {
		// The vocabulary is embedded in the binary, so this never needs network or cache state.
		gptTokenizer, gptTokenizerErr = tiktoken.Get(tiktoken.O200kBase)
	})
	return gptTokenizer, gptTokenizerErr
}

func heuristicTokenCount(text string) int {
	ascii, other := 0, 0
	for _, r := range text {
		if unicode.IsSpace(r) {
			continue
		}
		if r <= 0x7f {
			ascii++
		} else {
			other++
		}
	}
	if ascii == 0 && other == 0 {
		return 0
	}
	return (ascii+3)/4 + other
}

type responsesUsageEstimate struct {
	Values map[string]any
	Source string
}

func tokenEstimator(model string) (func(string) int, string) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-") {
		if enc, err := getGPTTokenizer(); err == nil {
			return func(text string) int {
				ids, _, err := enc.Encode(text)
				if err != nil {
					return heuristicTokenCount(text)
				}
				return len(ids)
			}, usageSourceTiktoken
		}
	}
	return heuristicTokenCount, usageSourceHeuristic
}

func serializedTokenCount(v any, count func(string) int) int {
	if s, ok := v.(string); ok {
		return count(s)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return count(fmt.Sprint(v))
	}
	return count(string(b))
}

func responsesMessageTokenCount(message oaiMsg, count func(string) int) int {
	total := messageProtocolTokens
	total += count(message.Role)
	total += serializedTokenCount(message.Content, count)
	total += count(message.Name)
	total += count(message.ToolCallID)
	for _, call := range message.ToolCalls {
		total += serializedTokenCount(call, count)
	}
	return total
}

// estimateResponsesUsage is a local Codex context estimate, never billing data.
func estimateResponsesUsage(model string, input []oaiMsg, tools []chathub.Tool, toolChoice any, output string) responsesUsageEstimate {
	count, source := tokenEstimator(model)
	in := requestProtocolTokens + replyPrimingTokens
	for _, message := range input {
		in += responsesMessageTokenCount(message, count)
	}
	for _, tool := range tools {
		in += toolProtocolTokens + serializedTokenCount(tool, count)
	}
	if toolChoice != nil {
		in += toolChoiceProtocolTokens + serializedTokenCount(toolChoice, count)
	}
	out := count(output)
	if output != "" {
		out += outputProtocolTokens
	}
	return responsesUsageEstimate{Values: map[string]any{"input_tokens": in, "output_tokens": out, "total_tokens": in + out}, Source: source}
}

func estimateResponsesUsageWithCache(model string, input []oaiMsg, tools []chathub.Tool, toolChoice any, output string, cachedTokens int) responsesUsageEstimate {
	estimate := estimateResponsesUsage(model, input, tools, toolChoice, output)
	if cachedTokens <= 0 {
		return estimate
	}
	inputTokens, _ := estimate.Values["input_tokens"].(int)
	cachedTokens = min(max(cachedTokens, 0), inputTokens)
	if cachedTokens > 0 {
		estimate.Values["input_tokens_details"] = map[string]any{"cached_tokens": cachedTokens}
	}
	return estimate
}

func localUsageMetadata(source string) map[string]any {
	return map[string]any{
		"usage_source":               source,
		"usage_values_are_estimates": true,
		"usage_estimate_scope":       "visible_request_and_completion",
		"usage_includes":             []string{"message_content", "message_framing", "tool_schemas", "tool_choice", "tool_calls", "completion_framing"},
	}
}
