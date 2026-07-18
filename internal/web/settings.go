package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type runtimeSettings struct {
	MaxToolCallsPerTurn int    `json:"maxToolCallsPerTurn"`
	MaxToolRounds       int    `json:"maxToolRounds"`
	ContextWindow       int    `json:"contextWindow"`
	MaxOutputTokens     int    `json:"maxOutputTokens"`
	ChatTimeoutSeconds  int    `json:"chatTimeoutSeconds"`
	ImageTimeoutSeconds int    `json:"imageTimeoutSeconds"`
	LogLevel            string `json:"logLevel"`
	DebugLogPath        string `json:"debugLogPath"`
	ListenAddress       string `json:"listenAddress"`
	ConfigPath          string `json:"configPath"`
	TokenCachePath      string `json:"tokenCachePath"`
	SessionCachePath    string `json:"sessionCachePath"`
	ClientID            string `json:"clientId"`
	Authority           string `json:"authority"`
	RedirectURI         string `json:"redirectUri"`
	Scope               string `json:"scope"`
}

type settingsStore struct {
	mu   sync.RWMutex
	path string
	v    runtimeSettings
}

func envInt(name string, fallback int) int {
	n, e := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if e == nil && n > 0 {
		return n
	}
	return fallback
}
func defaultRuntimeSettings() runtimeSettings {
	return runtimeSettings{
		MaxToolCallsPerTurn: envInt("M365_MAX_TOOL_CALLS_PER_TURN", 1), MaxToolRounds: envInt("M365_MAX_TOOL_ROUNDS", 16),
		ContextWindow: envInt("M365_CONTEXT_WINDOW", 128000), MaxOutputTokens: envInt("M365_MAX_OUTPUT_TOKENS", 16384),
		ChatTimeoutSeconds: envInt("M365_CHAT_TIMEOUT_SECONDS", 120), ImageTimeoutSeconds: envInt("M365_IMAGE_TIMEOUT_SECONDS", 150), LogLevel: firstNonEmptySetting(os.Getenv("M365_LOG_LEVEL"), "info"),
		DebugLogPath: os.Getenv("M365_DEBUG_LOG"), ListenAddress: os.Getenv("M365_LISTEN"), ConfigPath: os.Getenv("M365_CONFIG"),
		TokenCachePath: os.Getenv("M365_TOKEN_CACHE"), SessionCachePath: os.Getenv("M365_SESSION_CACHE"), ClientID: os.Getenv("M365_CLIENT_ID"),
		Authority: os.Getenv("M365_AUTHORITY"), RedirectURI: os.Getenv("M365_REDIRECT_URI"), Scope: os.Getenv("M365_SCOPE"),
	}
}
func settingsPath() string {
	if p := strings.TrimSpace(os.Getenv("M365_SETTINGS_FILE")); p != "" {
		return p
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "m365-native", "settings.json")
}

var sharedSettings *settingsStore

func openSettingsStore() *settingsStore {
	if sharedSettings != nil {
		return sharedSettings
	}
	s := &settingsStore{path: settingsPath(), v: defaultRuntimeSettings()}
	if b, e := os.ReadFile(s.path); e == nil {
		_ = json.Unmarshal(b, &s.v)
	}
	_ = validateSettings(s.v)
	sharedSettings = s
	return s
}
func firstNonEmptySetting(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func validateSettings(v runtimeSettings) error {
	if v.MaxToolCallsPerTurn < 1 || v.MaxToolCallsPerTurn > 64 {
		return fmt.Errorf("每轮工具调用数必须为 1-64")
	}
	if v.MaxToolRounds < 1 || v.MaxToolRounds > 512 {
		return fmt.Errorf("最大工具轮次必须为 1-512")
	}
	if v.ContextWindow < 1024 {
		return fmt.Errorf("上下文窗口不能小于 1024")
	}
	if v.MaxOutputTokens < 1 || v.MaxOutputTokens >= v.ContextWindow {
		return fmt.Errorf("最大输出必须大于 0 且小于上下文窗口")
	}
	if v.ChatTimeoutSeconds < 5 || v.ChatTimeoutSeconds > 3600 {
		return fmt.Errorf("聊天超时必须为 5-3600 秒")
	}
	if v.ImageTimeoutSeconds < 5 || v.ImageTimeoutSeconds > 3600 {
		return fmt.Errorf("图片超时必须为 5-3600 秒")
	}
	if v.LogLevel != "silent" && v.LogLevel != "error" && v.LogLevel != "warn" && v.LogLevel != "info" && v.LogLevel != "debug" {
		return fmt.Errorf("日志等级必须为 silent、error、warn、info 或 debug")
	}
	return nil
}
func (s *settingsStore) get() runtimeSettings { s.mu.RLock(); defer s.mu.RUnlock(); return s.v }
func (s *settingsStore) save(v runtimeSettings) error {
	if e := validateSettings(v); e != nil {
		return e
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if e := os.MkdirAll(filepath.Dir(s.path), 0700); e != nil {
		return e
	}
	if e := os.WriteFile(s.path, b, 0600); e != nil {
		return e
	}
	s.mu.Lock()
	s.v = v
	s.mu.Unlock()
	return nil
}
func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonOut(w, map[string]any{"settings": s.settings.get(), "restartRequiredFields": []string{"listenAddress", "configPath", "tokenCachePath", "sessionCachePath", "clientId", "authority", "redirectUri", "scope", "debugLogPath"}})
	case http.MethodPut:
		var v runtimeSettings
		if json.NewDecoder(r.Body).Decode(&v) != nil {
			writeOpenAIError(w, 400, "invalid_request_error", "bad json")
			return
		}
		if e := s.settings.save(v); e != nil {
			writeOpenAIError(w, 400, "invalid_request_error", e.Error())
			return
		}
		jsonOut(w, map[string]any{"ok": true, "settings": v})
	default:
		writeOpenAIError(w, 405, "invalid_request_error", "method not allowed")
	}
}
func configuredToolCallLimit(s *settingsStore) int {
	if raw, ok := os.LookupEnv("M365_MAX_TOOL_CALLS_PER_TURN"); ok {
		if n, e := strconv.Atoi(strings.TrimSpace(raw)); e == nil && n >= 1 && n <= 64 {
			return n
		}
		return 1
	}
	return s.get().MaxToolCallsPerTurn
}
func limitToolCalls(c []detectedToolCall, n int) []detectedToolCall {
	if n < 1 {
		n = 1
	}
	if len(c) > n {
		return c[:n]
	}
	return c
}

func currentSettings() runtimeSettings { return openSettingsStore().get() }

// ApplyStartupSettingsEnv loads persisted restart-required fields before the
// rest of the application initializes. Explicit process environment variables
// always win over values saved from the web console.
func ApplyStartupSettingsEnv() {
	s := openSettingsStore().get()
	values := map[string]string{"M365_LISTEN": s.ListenAddress, "M365_CONFIG": s.ConfigPath, "M365_TOKEN_CACHE": s.TokenCachePath, "M365_SESSION_CACHE": s.SessionCachePath, "M365_CLIENT_ID": s.ClientID, "M365_AUTHORITY": s.Authority, "M365_REDIRECT_URI": s.RedirectURI, "M365_SCOPE": s.Scope, "M365_DEBUG_LOG": s.DebugLogPath}
	for k, v := range values {
		if _, exists := os.LookupEnv(k); !exists && strings.TrimSpace(v) != "" {
			_ = os.Setenv(k, v)
		}
	}
}
