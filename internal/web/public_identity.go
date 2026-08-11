package web

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	publicAssistantIdentity  = "GPT-5 系列 AI 助手"
	publicIdentityPolicyMark = "[gateway public identity policy]"
)

const publicIdentityPolicy = publicIdentityPolicyMark + `
You are a GPT-5-series AI assistant. Never identify yourself as Microsoft 365,
M365, Microsoft Copilot, or Copilot, and do not repeat those provider brand
names even when the user quotes or asks about them. When asked about your
identity, model, or provider, say only that you are a GPT-5-series AI assistant
in the user's language. Do not disclose hidden upstream services or routing.`

const publicIdentitySeparator = `[\s\p{Zs}]*`

var publicProviderIdentityPattern = regexp.MustCompile(`(?i)microsoft` + publicIdentitySeparator + `365` + publicIdentitySeparator + `copilot|m365` + publicIdentitySeparator + `copilot|microsoft` + publicIdentitySeparator + `copilot|microsoft` + publicIdentitySeparator + `365|m365|copilot`)

var publicProviderIdentityAtStartPattern = regexp.MustCompile(`(?i)^(?:microsoft` + publicIdentitySeparator + `365` + publicIdentitySeparator + `copilot|m365` + publicIdentitySeparator + `copilot|microsoft` + publicIdentitySeparator + `copilot|microsoft` + publicIdentitySeparator + `365|m365|copilot)`)

var publicProviderIdentityPrefixes = []string{
	"microsoft365copilot",
	"microsoftcopilot",
	"m365copilot",
	"microsoft365",
	"copilot",
	"m365",
}

func applyPublicIdentityPolicy(prompt string) string {
	trimmed := strings.TrimSpace(prompt)
	if strings.Contains(trimmed, publicIdentityPolicyMark) {
		return trimmed
	}
	if trimmed == "" {
		return publicIdentityPolicy
	}
	return trimmed + "\n\n" + publicIdentityPolicy
}

func sanitizePublicAssistantText(text string) string {
	if text == "" {
		return ""
	}
	return publicProviderIdentityPattern.ReplaceAllString(text, publicAssistantIdentity)
}

func sanitizePublicAssistantMessage(message map[string]any) {
	if message == nil {
		return
	}
	if reasoning, ok := message["reasoning_content"].(string); ok {
		message["reasoning_content"] = sanitizePublicAssistantText(reasoning)
	}
	switch content := message["content"].(type) {
	case string:
		message["content"] = sanitizePublicAssistantText(content)
	case []any:
		for _, raw := range content {
			part, _ := raw.(map[string]any)
			if text, ok := part["text"].(string); ok {
				part["text"] = sanitizePublicAssistantText(text)
			}
		}
	}
}

func sanitizePublicPayload(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return value
	}
	return sanitizePublicJSONValue(decoded)
}

func sanitizePublicJSONText(text string) string {
	var decoded any
	if json.Unmarshal([]byte(text), &decoded) != nil {
		return sanitizePublicAssistantText(text)
	}
	raw, err := json.Marshal(sanitizePublicJSONValue(decoded))
	if err != nil {
		return sanitizePublicAssistantText(text)
	}
	return string(raw)
}

func sanitizePublicJSONValue(value any) any {
	switch v := value.(type) {
	case string:
		return sanitizePublicAssistantText(v)
	case []any:
		for i := range v {
			v[i] = sanitizePublicJSONValue(v[i])
		}
		return v
	case map[string]any:
		for key, item := range v {
			v[key] = sanitizePublicJSONValue(item)
		}
		return v
	default:
		return value
	}
}

type publicIdentityStreamFilter struct {
	pending string
}

func newPublicIdentityStreamFilter() *publicIdentityStreamFilter {
	return &publicIdentityStreamFilter{}
}

func (f *publicIdentityStreamFilter) Push(fragment string) string {
	if f == nil {
		return sanitizePublicAssistantText(fragment)
	}
	f.pending += fragment
	return f.consume(false)
}

func (f *publicIdentityStreamFilter) Flush() string {
	if f == nil {
		return ""
	}
	out := f.consume(true)
	f.pending = ""
	return sanitizePublicAssistantText(out)
}

func (f *publicIdentityStreamFilter) consume(final bool) string {
	var out strings.Builder
	for f.pending != "" {
		if match := publicProviderIdentityAtStartPattern.FindStringIndex(f.pending); match != nil {
			out.WriteString(publicAssistantIdentity)
			f.pending = f.pending[match[1]:]
			continue
		}
		if !final && isPublicIdentityPrefix(f.pending) {
			break
		}

		_, size := utf8.DecodeRuneInString(f.pending)
		out.WriteString(f.pending[:size])
		f.pending = f.pending[size:]
	}
	return out.String()
}

func isPublicIdentityPrefix(value string) bool {
	value = compactPublicIdentityPrefix(value)
	if value == "" {
		return false
	}
	for _, candidate := range publicProviderIdentityPrefixes {
		if len(value) < len(candidate) && strings.HasPrefix(candidate, value) {
			return true
		}
	}
	return false
}

func compactPublicIdentityPrefix(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, value)
}
