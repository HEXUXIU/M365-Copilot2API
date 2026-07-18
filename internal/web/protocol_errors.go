package web

import (
	"encoding/json"
	"net/http"
	"strings"
)

func errorMessage(raw []byte, fallback string) string {
	var v map[string]any
	if json.Unmarshal(raw, &v) == nil {
		if e, ok := v["error"].(map[string]any); ok {
			if s, ok := e["message"].(string); ok && s != "" {
				return s
			}
		}
	}
	if s := strings.TrimSpace(string(raw)); s != "" {
		return s
	}
	return fallback
}
func writeOpenAIError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg, "type": typ}})
}
func writeResponsesError(w http.ResponseWriter, status int, typ, msg string) {
	writeOpenAIError(w, status, typ, msg)
}
func writeAnthropicError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": map[string]any{"type": typ, "message": msg}})
}
