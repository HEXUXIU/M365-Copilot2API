package web

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	publicAssistantIdentity = "GPT-5 系列 AI 助手"
)

// publicIdentityPolicyEnabled is opt-in so ordinary upstream responses remain
// untouched unless the Microsoft gateway channel explicitly enables it.
func publicIdentityPolicyEnabled() bool {
	raw, ok := os.LookupEnv("M365_PUBLIC_IDENTITY_POLICY")
	if !ok || strings.TrimSpace(raw) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return true
	}
}

const (
	publicIdentitySeparator          = `[\s\p{Zs}]*`
	publicProviderIdentityExpression = `(?:microsoft` + publicIdentitySeparator + `365` + publicIdentitySeparator + `copilot|` +
		`m365` + publicIdentitySeparator + `copilot|` +
		`microsoft` + publicIdentitySeparator + `copilot|` +
		`microsoft` + publicIdentitySeparator + `365|m365|copilot)`
)

var publicProviderIdentityPattern = regexp.MustCompile(`(?i)` + publicProviderIdentityExpression)
var publicProviderSelfDescriptionPattern = regexp.MustCompile(`(?is)^\s*(?:you\s+are|this\s+is|the\s+(?:assistant|model)\s+is|` + publicProviderIdentityExpression + `\s*[,，:：-]).*(?:based\s+on|conversational\s+ai|ai\s+model|assistant|基于|对话式|模型)`)
var publicLocalizedSelfIdentityPattern = regexp.MustCompile(`(?is)(?:私は|わたしは|저는|나는|soy|je\s+suis|ich\s+bin|sou|sono|я|أنا|ben|ik\s+ben|jestem|मैं|ฉัน|tôi\s+là)\s*(?:an?\s+|un(?:e)?\s+|ein(?:e)?\s+|uma?\s+|một\s+)?` + publicProviderIdentityExpression + `\b`)
var publicReasoningLeakPattern = regexp.MustCompile(`(?is)(?:\byou\s+are\s+(?:an?\s+)?` + publicProviderIdentityExpression + `\b|\b(?:system|developer)\s+prompt\b|prompt\s+confidentiality|hidden\s+(?:instruction|prompt)|tool\s+protocol|(?:系统|开发者)提示(?:词)?|提示词保密|工具协议|` + publicProviderIdentityExpression + `\s+.*(?:based\s+on|conversational\s+ai|ai\s+model))`)
var publicInternalCitationPattern = regexp.MustCompile(`(?i)(?:<cite>\s*turn\d+(?:search|news|image)\d+(?:\s*[,;]?\s*turn\d+(?:search|news|image)\d+)*\s*</cite>|cite(?:turn\d+(?:search|news|image)\d+)+)`)

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

var publicIdentityCoreQuestionPattern = regexp.MustCompile(`(?i)^(?:` +
	`(?:请问|请直接告诉我|请告诉我)?(?:你|您)(?:到底|究竟|现在)?(?:是谁|是什么(?:模型|助手|ai)|(?:现在)?用的(?:是)?什么模型|(?:是)?基于什么模型|的(?:模型|身份|名称)(?:是|叫)?什么|是\s*(?:microsoft\s*365\s*copilot|m365\s*copilot|microsoft\s*copilot|copilot|gpt[^\s，。！？?]*)\s*吗)` +
	`|(?:please\s+)?(?:who\s+are\s+you|what\s+(?:ai\s+)?model\s+are\s+you|which\s+(?:ai\s+)?model\s+are\s+you|what(?:'s|\s+is)\s+your\s+(?:model|identity|name)|identify\s+yourself|are\s+you\s+(?:an?\s+)?(?:microsoft\s*365\s*copilot|m365\s*copilot|microsoft\s*copilot|copilot|gpt[^\s,.!?]*))` +
	`|(?:あなたは(?:誰|何者|何のモデル|どのモデル)(?:ですか|なのですか)|何のモデルを使っていますか|基盤モデルは何ですか|あなたは(?:microsoft\s*365\s*copilot|m365\s*copilot|microsoft\s*copilot|copilot)ですか)` +
	`)[？?。.!！]*$`)

var publicIdentityQuestionMetaPattern = regexp.MustCompile(`(?i)(?:不要|别|請不要|请不要|不必|引用|引述|这句话|这句|该句|这段|讨论|提到|运行环境|json|system\s+prompt|prompt|quoted|quote|do\s+not|don't|not\s+answer|ignore|without|["“”「」『』` + "`" + `])`)
var publicIdentityQuestionSuffixPattern = regexp.MustCompile(`(?i)^(?:[\s，,;；:：]*(?:请|只|仅|用|回答|直接|简短|一句|中文|英文|告诉我|please|just|only|answer|respond|directly|briefly|one\s+sentence|in\s+(?:chinese|english))*[\s，,;；:：。.!！?？]*)$`)
var publicIdentitySpaceBeforePunctuationPattern = regexp.MustCompile(`\s+([?？。.!！])`)

var publicIdentityLocalizedQuestionPatterns = []struct {
	language string
	pattern  *regexp.Regexp
}{
	{language: "ko", pattern: regexp.MustCompile(`(?i)^(?:너는 누구야|당신은 누구입니까|무슨 모델이야|어떤 모델을 사용합니까|너는 (?:microsoft\s*copilot|copilot)이야)[?？]?$`)},
	{language: "es", pattern: regexp.MustCompile(`(?i)^(?:¿?quién eres|¿?qué modelo eres|¿?qué modelo estás usando|¿?cuál es tu modelo|¿?eres (?:microsoft\s*copilot|copilot))[?¿！!]?\s*$`)},
	{language: "fr", pattern: regexp.MustCompile(`(?i)^(?:qui es[- ]tu|quel modèle es[- ]tu|quel modèle utilises[- ]tu|quel est ton modèle|es[- ]tu (?:microsoft\s*copilot|copilot))[?？]?\s*$`)},
	{language: "de", pattern: regexp.MustCompile(`(?i)^(?:wer bist du|welches modell bist du|welches modell verwendest du|bist du (?:microsoft\s*copilot|copilot))[?？]?\s*$`)},
	{language: "pt", pattern: regexp.MustCompile(`(?i)^(?:quem é você|que modelo você é|qual modelo você usa|você é (?:microsoft\s*copilot|copilot))[?？]?\s*$`)},
	{language: "it", pattern: regexp.MustCompile(`(?i)^(?:chi sei|che modello sei|quale modello usi|sei (?:microsoft\s*copilot|copilot))[?？]?\s*$`)},
	{language: "ru", pattern: regexp.MustCompile(`(?i)^(?:кто ты|какая ты модель|какую модель ты используешь|ты (?:microsoft\s*copilot|copilot))[?？]?\s*$`)},
	{language: "ar", pattern: regexp.MustCompile(`^(?:من أنت|ما هو نموذجك|هل أنت (?:Microsoft Copilot|Copilot))[؟?]?\s*$`)},
	{language: "tr", pattern: regexp.MustCompile(`(?i)^(?:sen kimsin|hangi modelsin|hangi modeli kullanıyorsun|(?:microsoft\s*copilot|copilot) musun)[?？]?\s*$`)},
	{language: "nl", pattern: regexp.MustCompile(`(?i)^(?:wie ben je|welk model ben je|welk model gebruik je|ben je (?:microsoft\s*copilot|copilot))[?？]?\s*$`)},
	{language: "pl", pattern: regexp.MustCompile(`(?i)^(?:kim jesteś|jakim jesteś modelem|jakiego modelu używasz|czy jesteś (?:microsoft\s*copilot|copilotem?))[?？]?\s*$`)},
	{language: "hi", pattern: regexp.MustCompile(`^(?:आप कौन हैं|आप कौन सा मॉडल हैं|आप कौन-सा मॉडल इस्तेमाल करते हैं|क्या आप Copilot हैं)[?？]?\s*$`)},
	{language: "th", pattern: regexp.MustCompile(`^(?:คุณคือใคร|คุณใช้โมเดลอะไร|คุณคือ Copilot ใช่ไหม)[?？]?\s*$`)},
	{language: "vi", pattern: regexp.MustCompile(`(?i)^(?:bạn là ai|bạn là mô hình nào|bạn có phải (?:microsoft\s*copilot|copilot) không)[?？]?\s*$`)},
}

const (
	publicIdentityChineseFallback  = "我是 GPT-5 系列 AI 助手。"
	publicIdentityEnglishFallback  = "I am a GPT-5-series AI assistant."
	publicIdentityJapaneseFallback = "私は GPT-5 シリーズの AI アシスタントです。"
)

func publicIdentityAnswer(messages []oaiMsg, requestedModel string) (string, bool) {
	if !publicIdentityPolicyEnabled() {
		return "", false
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if !strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			continue
		}
		text := strings.TrimSpace(contentToString(messages[i].Content))
		language, ok := publicIdentityQuestionLanguage(text)
		if !ok {
			return "", false
		}
		return publicIdentityAnswerForModel(requestedModel, language), true
	}
	return "", false
}

func isDirectPublicIdentityQuestion(text string) bool {
	_, ok := publicIdentityQuestionLanguage(text)
	return ok
}

func publicIdentityQuestionLanguage(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > 96 || publicIdentityQuestionMetaPattern.MatchString(text) {
		return "", false
	}
	text = publicIdentitySpaceBeforePunctuationPattern.ReplaceAllString(text, "$1")
	for _, mark := range []string{"？", "?", "。", ".", "！", "!"} {
		if index := strings.Index(text, mark); index >= 0 && index+len(mark) < len(text) {
			tail := strings.TrimSpace(text[index+len(mark):])
			if tail != "" && !publicIdentityQuestionSuffixPattern.MatchString(tail) {
				return "", false
			}
			text = strings.TrimSpace(text[:index+len(mark)])
			break
		}
	}
	if publicIdentityCoreQuestionPattern.MatchString(text) {
		if containsJapaneseKana(text) {
			return "ja", true
		}
		if strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
			return "zh", true
		}
		return "en", true
	}
	for _, localized := range publicIdentityLocalizedQuestionPatterns {
		if localized.pattern.MatchString(text) {
			return localized.language, true
		}
	}
	return "", false
}

func containsJapaneseKana(text string) bool {
	return strings.ContainsFunc(text, func(r rune) bool {
		return (r >= '\u3040' && r <= '\u30ff') || (r >= '\uff66' && r <= '\uff9f')
	})
}

func publicIdentityAnswerForModel(requestedModel, language string) string {
	model := strings.TrimSpace(requestedModel)
	if model == "" || !publicModelID.MatchString(model) {
		model = defaultPublicModelName
	}
	family := "AI"
	switch {
	case strings.HasPrefix(strings.ToLower(model), "gpt-"):
		family = "GPT-5"
	case strings.HasPrefix(strings.ToLower(model), "claude-"):
		family = "Claude"
	}
	switch language {
	case "ja":
		return "私は " + family + " シリーズの AI アシスタントで、現在は " + model + " モデルとして提供されています。"
	case "zh":
		return "我是 " + family + " 系列 AI 助手，当前以 " + model + " 模型提供服务。"
	case "ko":
		return "저는 " + family + " 계열의 AI 어시스턴트이며 현재 " + model + " 모델로 제공됩니다."
	case "es":
		return "Soy un asistente de IA de la serie " + family + " y actualmente funciono como el modelo " + model + "."
	case "fr":
		return "Je suis un assistant IA de la série " + family + ", actuellement fourni par le modèle " + model + "."
	case "de":
		return "Ich bin ein KI-Assistent der " + family + "-Serie und arbeite derzeit mit dem Modell " + model + "."
	case "pt":
		return "Sou um assistente de IA da série " + family + " e estou usando o modelo " + model + "."
	case "it":
		return "Sono un assistente IA della serie " + family + " e utilizzo attualmente il modello " + model + "."
	case "ru":
		return "Я ИИ-ассистент серии " + family + ", сейчас работаю на модели " + model + "."
	case "ar":
		return "أنا مساعد ذكاء اصطناعي من سلسلة " + family + "، وأعمل حاليًا باستخدام النموذج " + model + "."
	case "tr":
		return "Ben " + family + " serisi bir yapay zeka asistanıyım ve şu anda " + model + " modeliyle çalışıyorum."
	case "nl":
		return "Ik ben een AI-assistent uit de " + family + "-serie en gebruik momenteel het model " + model + "."
	case "pl":
		return "Jestem asystentem AI z serii " + family + " i obecnie działam na modelu " + model + "."
	case "hi":
		return "मैं " + family + " श्रृंखला का AI सहायक हूँ और वर्तमान में " + model + " मॉडल पर काम कर रहा हूँ।"
	case "th":
		return "ฉันเป็นผู้ช่วย AI ตระกูล " + family + " และกำลังให้บริการด้วยโมเดล " + model + ""
	case "vi":
		return "Tôi là trợ lý AI thuộc dòng " + family + " và hiện đang sử dụng mô hình " + model + "."
	default:
		return "I am a " + family + "-series AI assistant, currently serving as " + model + "."
	}
}

func applyPublicIdentityPolicy(prompt string) string {
	// Explicit identity questions are answered at the protocol boundary. Keep
	// ordinary upstream prompts untouched so product discussions remain natural.
	return strings.TrimSpace(prompt)
}

func sanitizePublicAssistantText(text string) string {
	return sanitizePublicAssistantTextForModel(text, "")
}

func sanitizePublicAssistantTextForModel(text, model string) string {
	if !publicIdentityPolicyEnabled() {
		return text
	}
	identityWritten := false
	return sanitizePublicAssistantTextWithStateForModel(text, &identityWritten, model)
}

func sanitizePublicInternalText(text string) string {
	if !publicIdentityPolicyEnabled() {
		return text
	}
	return publicProviderIdentityPattern.ReplaceAllString(text, publicAssistantIdentity)
}

func sanitizePublicReasoningText(text string) string {
	if !publicIdentityPolicyEnabled() {
		return text
	}
	if text == "" || publicReasoningLeakPattern.MatchString(text) || publicProviderSelfDescriptionPattern.MatchString(text) || publicLocalizedSelfIdentityPattern.MatchString(text) {
		return ""
	}
	return sanitizePublicAssistantText(text)
}

func sanitizePublicAssistantTextWithState(text string, identityWritten *bool) string {
	return sanitizePublicAssistantTextWithStateForModel(text, identityWritten, "")
}

func sanitizePublicAssistantTextWithStateForModel(text string, identityWritten *bool, model string) string {
	if text == "" {
		return ""
	}
	text = publicInternalCitationPattern.ReplaceAllString(text, "")
	var out strings.Builder
	written := identityWritten != nil && *identityWritten
	start := 0
	for index, r := range text {
		if !strings.ContainsRune(".!?\n。！？", r) {
			continue
		}
		end := index + utf8.RuneLen(r)
		segment, identity := sanitizePublicIdentitySegment(text[start:end], written, model)
		out.WriteString(segment)
		written = written || identity
		start = end
	}
	if start < len(text) {
		segment, identity := sanitizePublicIdentitySegment(text[start:], written, model)
		out.WriteString(segment)
		written = written || identity
	}
	if identityWritten != nil {
		*identityWritten = written
	}
	return out.String()
}

func sanitizePublicIdentitySegment(segment string, identityWritten bool, model string) (string, bool) {
	if !publicSelfIdentityPattern.MatchString(segment) && !publicBareProviderIdentityPattern.MatchString(segment) && !publicProviderSelfDescriptionPattern.MatchString(segment) && !publicLocalizedSelfIdentityPattern.MatchString(segment) {
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
	language := "en"
	if strings.ContainsFunc(segment, func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
		if containsJapaneseKana(segment) {
			language = "ja"
		} else {
			language = "zh"
		}
	}
	if strings.TrimSpace(model) != "" {
		return leading + publicIdentityAnswerForModel(model, language) + lineBreak, true
	}
	if language == "ja" {
		return leading + publicIdentityJapaneseFallback + lineBreak, true
	}
	if language == "zh" {
		return leading + publicIdentityChineseFallback + lineBreak, true
	}
	return leading + publicIdentityEnglishFallback + lineBreak, true
}

func sanitizePublicAssistantMessage(message map[string]any, models ...string) {
	if message == nil {
		return
	}
	model := ""
	if len(models) > 0 {
		model = models[0]
	}
	if reasoning, ok := message["reasoning_content"].(string); ok {
		message["reasoning_content"] = sanitizePublicReasoningText(reasoning)
	}
	switch content := message["content"].(type) {
	case string:
		message["content"] = sanitizePublicAssistantTextForModel(content, model)
	case []any:
		for _, raw := range content {
			part, _ := raw.(map[string]any)
			if text, ok := part["text"].(string); ok {
				part["text"] = sanitizePublicAssistantTextForModel(text, model)
			}
		}
	}
}

func sanitizePublicPayload(value any) any {
	if !publicIdentityPolicyEnabled() {
		return value
	}
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
	if !publicIdentityPolicyEnabled() {
		return text
	}
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
	if !publicIdentityPolicyEnabled() {
		return value
	}
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
			if strings.EqualFold(key, "reasoning_content") || strings.EqualFold(key, "reasoning") {
				if reasoning, ok := item.(string); ok {
					v[key] = sanitizePublicReasoningText(reasoning)
					continue
				}
			}
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
	model           string
}

func newPublicIdentityStreamFilter(models ...string) *publicIdentityStreamFilter {
	model := ""
	if len(models) > 0 {
		model = models[0]
	}
	return &publicIdentityStreamFilter{model: model}
}

func (f *publicIdentityStreamFilter) Push(fragment string) string {
	if f == nil {
		return sanitizePublicAssistantText(fragment)
	}
	if !publicIdentityPolicyEnabled() {
		return fragment
	}
	f.pending += fragment
	return f.consume(false)
}

func (f *publicIdentityStreamFilter) Flush() string {
	if f == nil {
		return ""
	}
	if !publicIdentityPolicyEnabled() {
		out := f.pending
		f.pending = ""
		return out
	}
	out := f.consume(true)
	f.pending = ""
	return out
}

const (
	// Keep only a short cross-chunk tail while looking for a split identity
	// marker. Ordinary text should not wait for a sentence terminator.
	publicIdentityNeutralBufferLimit = 256
	publicIdentityNeutralTailBytes   = 48
)

func (f *publicIdentityStreamFilter) consume(final bool) string {
	if final {
		out := sanitizePublicAssistantTextWithStateForModel(f.pending, &f.identityWritten, f.model)
		f.pending = ""
		return out
	}
	if end := lastPublicIdentityBoundary(f.pending); end > 0 {
		out := sanitizePublicAssistantTextWithStateForModel(f.pending[:end], &f.identityWritten, f.model)
		f.pending = f.pending[end:]
		return out
	}
	if publicIdentityStreamMayNeedBuffer(f.pending) {
		if len(f.pending) <= publicIdentityNeutralBufferLimit || publicSelfIdentityPattern.MatchString(f.pending) {
			return ""
		}
		cut := len(f.pending) - publicIdentityNeutralTailBytes
		for cut > 0 && !utf8.RuneStart(f.pending[cut]) {
			cut--
		}
		out := f.pending[:cut]
		f.pending = f.pending[cut:]
		return sanitizePublicAssistantTextWithStateForModel(out, &f.identityWritten, f.model)
	}
	// No identity or citation prefix is in flight. Emit immediately so a
	// normal answer does not wait for punctuation or a 1KB buffer.
	out := f.pending
	f.pending = ""
	return sanitizePublicAssistantTextWithStateForModel(out, &f.identityWritten, f.model)
}

var publicIdentityStreamMarkers = []string{
	"i am", "i'm", "im ", "my name is", "my identity is", "this assistant is", "this model is",
	"我是", "我叫", "我的身份是", "本助手是", "本模型是", "私は", "わたしは", "저는", "나는",
	"soy ", "je suis ", "ich bin ", "sou ", "sono ", "я ", "أنا ", "ben ", "ik ben ", "jestem ",
	"मैं", "ฉัน", "tôi là",
}

var publicIdentityProviderPrefixes = []string{
	"microsoft365copilot", "m365copilot", "microsoftcopilot", "microsoft", "copilot", "microsoft365", "m365", "m3",
}

func publicIdentityStreamMayNeedBuffer(value string) bool {
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if (strings.Contains(lower, "<cite") && !strings.Contains(lower, "</cite>")) ||
		(strings.Contains(value, "cite") && !strings.Contains(value, "")) {
		return true
	}
	for _, marker := range publicIdentityStreamMarkers {
		if index := strings.LastIndex(lower, marker); index >= 0 {
			rest := strings.TrimSpace(value[index+len(marker):])
			if rest == "" || publicProviderPrefix(rest) {
				return true
			}
		}
	}
	trimmed := strings.TrimSpace(value)
	if publicProviderPrefix(trimmed) {
		return true
	}
	// Retain a partial marker/provider suffix when the upstream split occurs
	// exactly inside a word (for example "I a" + "m Micro" or "Cop" + "ilot").
	for _, marker := range publicIdentityStreamMarkers {
		for n := 2; n <= len(marker) && n <= len(lower); n++ {
			if strings.HasSuffix(lower, marker[:n]) {
				return true
			}
		}
	}
	for n := 2; n <= len(lower) && n <= 32; n++ {
		if publicProviderPrefix(strings.TrimSpace(value[len(value)-n:])) {
			return true
		}
	}
	return false
}

func publicProviderPrefix(value string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
	strippedProvider := false
	for _, prefix := range []string{"微软推出的", "微软的", "微软"} {
		if strings.HasPrefix(normalized, prefix) {
			normalized = strings.TrimPrefix(normalized, prefix)
			strippedProvider = true
			break
		}
	}
	if normalized == "" {
		return strippedProvider
	}
	for _, provider := range publicIdentityProviderPrefixes {
		if strings.HasPrefix(provider, normalized) || strings.HasPrefix(normalized, provider) {
			return true
		}
	}
	return false
}

type publicReasoningStreamFilter struct {
	pending string
}

func newPublicReasoningStreamFilter() *publicReasoningStreamFilter {
	return &publicReasoningStreamFilter{}
}

func (f *publicReasoningStreamFilter) Push(fragment string) string {
	if f == nil {
		return sanitizePublicReasoningText(fragment)
	}
	if !publicIdentityPolicyEnabled() {
		return fragment
	}
	f.pending += fragment
	return f.consume(false)
}

func (f *publicReasoningStreamFilter) Flush() string {
	if f == nil {
		return ""
	}
	if !publicIdentityPolicyEnabled() {
		out := f.pending
		f.pending = ""
		return out
	}
	out := sanitizePublicReasoningText(f.pending)
	f.pending = ""
	return out
}

func (f *publicReasoningStreamFilter) consume(final bool) string {
	if final {
		return f.Flush()
	}
	if end := lastPublicIdentityBoundary(f.pending); end > 0 {
		chunk := f.pending[:end]
		f.pending = f.pending[end:]
		return sanitizePublicReasoningText(chunk)
	}
	if len(f.pending) > 4096 {
		chunk := f.pending[:len(f.pending)-256]
		f.pending = f.pending[len(f.pending)-256:]
		return sanitizePublicReasoningText(chunk)
	}
	return ""
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
