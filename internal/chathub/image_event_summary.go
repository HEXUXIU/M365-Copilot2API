package chathub

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	maxImageSummaryDepth = 32
	maxImageSummaryNodes = 1024
)

var imageProtocolEnums = map[string]struct{}{
	"update": {},
	"Chat":   {}, "Suggestion": {}, "InternalSearchQuery": {}, "Disengaged": {},
	"InternalLoaderMessage": {}, "Progress": {}, "GeneratedCode": {},
	"RenderCardRequest": {}, "AdsQuery": {}, "SemanticSerp": {},
	"GenerateContentQuery": {}, "GenerateGraphicArt": {}, "SearchQuery": {},
	"ConfirmationCard": {}, "AuthError": {}, "DeveloperLogs": {},
	"TriggerPlugin": {}, "HintInvocation": {}, "MemoryUpdate": {},
	"EndOfRequest": {}, "TriggerConfirmation": {}, "ResumeInvokeAction": {},
	"ResumeUserInputRequest": {}, "TriggerUserInputRequest": {}, "EscapeHatch": {},
	"TriggerPluginAuth": {}, "ResumePluginAuth": {}, "SideBySide": {},
	"ReferencesListComplete": {}, "SwitchRespondingEndpoint": {},
	"image": {}, "GraphicArt": {}, "SearchResults": {}, "Code": {}, "ToolCall": {},
	"ChainOfThought": {}, "ChainOfThoughtSummary": {}, "Reasoning": {}, "Text": {},
	"ImageGeneration": {}, "DeepLeo": {}, "Tool": {}, "Assistant": {}, "Generated": {},
	"Pending": {}, "InProgress": {}, "Completed": {}, "Failed": {}, "Success": {}, "Succeeded": {},
	"ImageGenInsufficientTokensThrottled": {}, "ImageGenSystemCapacityThrottled": {},
	"DesignerUserLimitThrottlingError": {}, "BICRequestInProgressError": {},
	"ErrorDisallowedAADUser": {},
}

type ImageEventSummary struct {
	Index               int                    `json:"index"`
	Bytes               int                    `json:"bytes"`
	Parsed              bool                   `json:"parsed"`
	EventType           int                    `json:"event_type,omitempty"`
	Target              string                 `json:"target,omitempty"`
	TopLevelKeys        []string               `json:"top_level_keys,omitempty"`
	Arguments           []ImageArgumentSummary `json:"arguments,omitempty"`
	ImageCandidateCount int                    `json:"image_candidate_count"`
	ResultClass         string                 `json:"result_class,omitempty"`
	Metering            []ImageMeterSummary    `json:"metering,omitempty"`
}

type ImageArgumentSummary struct {
	Keys     []string              `json:"keys,omitempty"`
	Messages []ImageMessageSummary `json:"messages,omitempty"`
	Metering []ImageMeterSummary   `json:"metering,omitempty"`
}

type ImageMessageSummary struct {
	MessageType   string                 `json:"message_type,omitempty"`
	ContentType   string                 `json:"content_type,omitempty"`
	ContentOrigin string                 `json:"content_origin,omitempty"`
	Keys          []string               `json:"keys,omitempty"`
	Progress      []ImageProgressSummary `json:"progress,omitempty"`
}

type ImageProgressSummary struct {
	ContentType string   `json:"content_type,omitempty"`
	Status      string   `json:"status,omitempty"`
	Keys        []string `json:"keys,omitempty"`
	URLCount    int      `json:"url_count"`
}

type ImageMeterSummary struct {
	Capability string `json:"capability,omitempty"`
	HasAccess  *bool  `json:"has_access,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
}

func SummarizeImageEvents(raw []json.RawMessage) []ImageEventSummary {
	out := make([]ImageEventSummary, 0, len(raw))
	for i, event := range raw {
		summary := ImageEventSummary{Index: i, Bytes: len(event)}
		var value any
		if err := json.Unmarshal(event, &value); err != nil {
			out = append(out, summary)
			continue
		}
		summary.Parsed = true
		nodes := 0
		summary.ImageCandidateCount = countImageCandidates(value, 0, &nodes)

		object, ok := value.(map[string]any)
		if !ok {
			out = append(out, summary)
			continue
		}
		summary.TopLevelKeys = safeSortedKeys(object)
		summary.EventType = intValue(object["type"])
		summary.Target = protocolEnum(object["target"])

		if args, ok := object["arguments"].([]any); ok {
			for _, rawArg := range args {
				arg, ok := rawArg.(map[string]any)
				if !ok {
					continue
				}
				argSummary := ImageArgumentSummary{Keys: safeSortedKeys(arg)}
				argSummary.Messages = summarizeImageMessages(arg["messages"])
				argSummary.Metering = summarizeImageMetering(arg["meteringInformation"])
				summary.Arguments = append(summary.Arguments, argSummary)
			}
		}

		if item, ok := object["item"].(map[string]any); ok {
			if result, ok := item["result"].(map[string]any); ok {
				summary.ResultClass = resultValueClass(result["value"])
				summary.Metering = summarizeImageMetering(result["meteringInformation"])
			}
		}
		out = append(out, summary)
	}
	return out
}

func summarizeImageMessages(value any) []ImageMessageSummary {
	messages, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]ImageMessageSummary, 0, len(messages))
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		summary := ImageMessageSummary{
			MessageType:   protocolEnum(message["messageType"]),
			ContentType:   protocolEnum(message["contentType"]),
			ContentOrigin: protocolEnum(message["contentOrigin"]),
			Keys:          safeSortedKeys(message),
		}
		if progress, ok := message["contentGenerationProgressList"].([]any); ok {
			for _, rawProgress := range progress {
				item, ok := rawProgress.(map[string]any)
				if !ok {
					continue
				}
				nodes := 0
				summary.Progress = append(summary.Progress, ImageProgressSummary{
					ContentType: protocolEnum(item["contentType"]),
					Status:      protocolStatus(item["status"]),
					Keys:        safeSortedKeys(item),
					URLCount:    countImageCandidates(item, 0, &nodes),
				})
			}
		}
		out = append(out, summary)
	}
	return out
}

func summarizeImageMetering(value any) []ImageMeterSummary {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]ImageMeterSummary, 0, len(items))
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		summary := ImageMeterSummary{
			Capability: protocolEnum(firstSummaryValue(item, "feature", "capability", "name")),
			ErrorClass: protocolEnum(firstSummaryValue(item, "meterError", "errorCode", "code")),
		}
		if access, ok := item["hasAccess"].(bool); ok {
			summary.HasAccess = &access
		}
		if summary.Capability != "" || summary.ErrorClass != "" || summary.HasAccess != nil {
			out = append(out, summary)
		}
	}
	return out
}

func firstSummaryValue(item map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := item[key]; ok {
			return value
		}
	}
	return nil
}

func resultValueClass(value any) string {
	text, _ := value.(string)
	switch {
	case strings.TrimSpace(text) == "":
		return "empty"
	case strings.EqualFold(strings.TrimSpace(text), "success"):
		return "success"
	default:
		return "other"
	}
}

func intValue(value any) int {
	switch number := value.(type) {
	case float64:
		return int(number)
	case json.Number:
		n, _ := strconv.Atoi(number.String())
		return n
	default:
		return 0
	}
}

func protocolStatus(value any) string {
	switch status := value.(type) {
	case float64:
		return strconv.FormatInt(int64(status), 10)
	case json.Number:
		return status.String()
	case string:
		return protocolEnum(status)
	case nil:
		return ""
	default:
		return "other"
	}
}

func protocolEnum(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if !safeIdentifier(text) {
		return "other"
	}
	if _, ok := imageProtocolEnums[text]; !ok {
		return "other"
	}
	return text
}

func safeIdentifier(text string) bool {
	if len(text) > 64 || strings.ContainsAny(text, ":/@?=&") || looksLikeUUID(text) {
		return false
	}
	for _, r := range text {
		if unicode.IsSpace(r) || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func looksLikeUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

func safeSortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	seen := make(map[string]struct{}, len(object))
	for key := range object {
		safe := key
		if !safeIdentifier(safe) {
			safe = "other"
		}
		if _, ok := seen[safe]; ok {
			continue
		}
		seen[safe] = struct{}{}
		keys = append(keys, safe)
	}
	sort.Strings(keys)
	return keys
}

func countImageCandidates(value any, depth int, nodes *int) int {
	if depth > maxImageSummaryDepth || *nodes >= maxImageSummaryNodes {
		return 0
	}
	*nodes++
	switch typed := value.(type) {
	case []any:
		total := 0
		for _, item := range typed {
			total += countImageCandidates(item, depth+1, nodes)
		}
		return total
	case map[string]any:
		total := 0
		for _, item := range typed {
			total += countImageCandidates(item, depth+1, nodes)
		}
		return total
	case string:
		if strings.HasPrefix(typed, "https://") && isImageURL(typed) {
			return 1
		}
		if strings.HasPrefix(typed, "data:image/") && strings.Contains(typed, ",") && isImageURL(typed) {
			return 1
		}
	}
	return 0
}
