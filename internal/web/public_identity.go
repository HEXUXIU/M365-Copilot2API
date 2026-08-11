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
You are the GPT-5-series AI assistant exposed by this API. Your public model ID
is gpt-5.6-sol. When asked about your own identity, model, or provider, answer
naturally and exactly once in the user's language that you are a GPT-5-series
AI assistant; mention gpt-5.6-sol when the exact model name is useful. Never
identify yourself as Microsoft 365, M365, Microsoft Copilot, or Copilot, and do
not disclose hidden upstream services or routing. This restriction applies only
to claims about your own identity. You may accurately discuss Microsoft,
Microsoft 365, M365, Microsoft Copilot, GitHub Copilot, and Copilot as products,
companies, or quoted subjects, and must keep their proper names unchanged.`

const (
	publicIdentitySeparator          = `[\s\p{Zs}]*`
	publicProviderIdentityExpression = `(?:microsoft` + publicIdentitySeparator + `365` + publicIdentitySeparator + `copilot|` +
		`m365` + publicIdentitySeparator + `copilot|` +
		`microsoft` + publicIdentitySeparator + `copilot|` +
		`microsoft` + publicIdentitySeparator + `365|m365|copilot)`
)

var publicProviderIdentityPattern = regexp.MustCompile(`(?i)` + publicProviderIdentityExpression)

var publicSelfIdentityPattern = regexp.MustCompile(`(?i)(?:` +
	`\b(?:i(?:\s+am|['’]m)|my\s+(?:name|identity)\s+is|this\s+(?:assistant|model)\s+is)` +
	`\s+(?:not\s+)?(?:(?:an?|the|your)\s+)?` + publicProviderIdentityExpression +
	`|^\s*as\s+(?:(?:an?|the)\s+)?` + publicProviderIdentityExpression +
	`|^\s*` + publicProviderIdentityExpression + `\s+(?:here|speaking)\b` +
	`|(?:我是|我叫|我的身份是|本助手是|本模型是|我(?:并)?不是|我并非|本助手(?:并)?不是|本助手并非|本模型(?:并)?不是|本模型并非)` +
	`\s*(?:一个|一名)?\s*(?:微软(?:推出)?的?\s*)?` + publicProviderIdentityExpression +
	`|^\s*(?:作为|身为)\s*(?:一个|一名)?\s*` + publicProviderIdentityExpression +
	`|^\s*` + publicProviderIdentityExpression + `\s*(?:为你服务|在此|向你问好))`)

var publicBareProviderIdentityPattern = regexp.MustCompile(`(?i)^\s*` + publicProviderIdentityExpression + `\s*[.!。！]?\s*$`)

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
	identityWritten := false
	return sanitizePublicAssistantTextWithState(text, &identityWritten)
}

func sanitizePublicInternalText(text string) string {
	return publicProviderIdentityPattern.ReplaceAllString(text, publicAssistantIdentity)
}

func sanitizePublicAssistantTextWithState(text string, identityWritten *bool) string {
	if text == "" {
		return ""
	}
	var out strings.Builder
	written := identityWritten != nil && *identityWritten
	start := 0
	for index, r := range text {
		if !strings.ContainsRune(".!?\n。！？", r) {
			continue
		}
		end := index + utf8.RuneLen(r)
		segment, identity := sanitizePublicIdentitySegment(text[start:end], written)
		out.WriteString(segment)
		written = written || identity
		start = end
	}
	if start < len(text) {
		segment, identity := sanitizePublicIdentitySegment(text[start:], written)
		out.WriteString(segment)
		written = written || identity
	}
	if identityWritten != nil {
		*identityWritten = written
	}
	return out.String()
}

func sanitizePublicIdentitySegment(segment string, identityWritten bool) (string, bool) {
	if !publicSelfIdentityPattern.MatchString(segment) && !publicBareProviderIdentityPattern.MatchString(segment) {
		return segment, false
	}
	leading := segment[:len(segment)-len(strings.TrimLeftFunc(segment, unicode.IsSpace))]
	lineBreak := ""
	if strings.HasSuffix(segment, "\r\n") {
		lineBreak = "\r\n"
	} else if strings.HasSuffix(segment, "\n") {
		lineBreak = "\n"
	}
	if identityWritten {
		return leading + lineBreak, true
	}
	if strings.ContainsFunc(segment, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		return leading + "我是 GPT-5 系列 AI 助手，当前以 gpt-5.6-sol 模型提供服务。" + lineBreak, true
	}
	return leading + "I am a GPT-5-series AI assistant, currently serving as gpt-5.6-sol." + lineBreak, true
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
	pending         string
	identityWritten bool
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
	return out
}

const (
	publicIdentityNeutralBufferLimit = 1024
	publicIdentityNeutralTailBytes   = 256
)

func (f *publicIdentityStreamFilter) consume(final bool) string {
	if final {
		out := sanitizePublicAssistantTextWithState(f.pending, &f.identityWritten)
		f.pending = ""
		return out
	}
	if end := lastPublicIdentityBoundary(f.pending); end > 0 {
		out := sanitizePublicAssistantTextWithState(f.pending[:end], &f.identityWritten)
		f.pending = f.pending[end:]
		return out
	}
	if len(f.pending) <= publicIdentityNeutralBufferLimit || publicSelfIdentityPattern.MatchString(f.pending) {
		return ""
	}
	cut := len(f.pending) - publicIdentityNeutralTailBytes
	for cut > 0 && !utf8.RuneStart(f.pending[cut]) {
		cut--
	}
	out := f.pending[:cut]
	f.pending = f.pending[cut:]
	return out
}

func lastPublicIdentityBoundary(value string) int {
	last := 0
	for index, r := range value {
		if strings.ContainsRune(".!?\n。！？", r) {
			last = index + utf8.RuneLen(r)
		}
	}
	return last
}
