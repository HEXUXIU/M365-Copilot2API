package web

import (
	"m365-native/internal/chathub"
	"os"
	"strings"
)

func envTrue(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func compatM365Metadata(res chathub.Result) map[string]any {
	m := map[string]any{
		"conversationId": res.ConversationID,
		"sessionId":      res.SessionID,
		"requestId":      res.RequestID,
		"usage_source":   "unavailable_from_chathub",
	}
	if envTrue("M365_INCLUDE_UPSTREAM_EVENTS") {
		m["events"] = res.Events
	}
	return m
}

func normalizedToolChoiceMode(choice any) string {
	if choice == nil {
		return "auto"
	}
	if s, ok := choice.(string); ok {
		return s
	}
	if m, ok := choice.(map[string]any); ok {
		if f, ok := m["function"].(map[string]any); ok {
			if n, ok := f["name"].(string); ok {
				return "named:" + n
			}
		}
		if n, ok := m["name"].(string); ok {
			return "named:" + n
		}
	}
	return "auto"
}
