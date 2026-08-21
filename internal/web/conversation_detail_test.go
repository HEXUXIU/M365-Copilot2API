package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m365-copilot2api/internal/auth"
)

func TestConversationListAndDetailUseCompleteLocalHistory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("M365_SESSION_CACHE", filepath.Join(dir, "sessions.json"))
	t.Setenv("M365_CONVERSATION_CACHE", filepath.Join(dir, "conversations.json"))
	t.Setenv("M365_API_KEYS", filepath.Join(dir, "api-keys.json"))
	store, err := auth.OpenStore(filepath.Join(dir, "accounts.json"))
	if err != nil {
		t.Fatal(err)
	}
	keyStore := openAPIKeys()
	keyRecord, _, err := keyStore.create("console-key")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{tokens: store, sessionResolver: openSessionResolver(), apiKeys: keyStore}

	oldCloudClient := m365CloudClient
	m365CloudClient = nil
	defer func() { m365CloudClient = oldCloudClient }()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(sessionHeaderName, "session-detail")
	body := &oaiReq{Messages: []oaiMsg{
		{Role: "user", Content: "show the complete answer"},
		{Role: "assistant", Content: "complete body", ReasoningContent: "complete reasoning"},
	}}
	s.sessionResolver.Bind("", "conversation-detail", "account-a", keyRecord.ID, body, "", req)

	// 无 key 归因的会话（JWT 请求或升级前的旧数据）。
	// 注意用不带显式会话头的请求，否则会命中上面 session-detail 的更新分支。
	anonReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	s.sessionResolver.Bind("", "conversation-anon", "account-a", "",
		&oaiReq{Messages: []oaiMsg{{Role: "user", Content: "anonymous"}}}, "", anonReq)

	listRecorder := httptest.NewRecorder()
	s.handleM365Conversations(listRecorder, httptest.NewRequest(http.MethodGet, "/api/m365/conversations", nil))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var list struct {
		Count int              `json:"count"`
		Data  []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Count != 2 {
		t.Fatalf("list response=%s", listRecorder.Body.String())
	}
	rows := map[string]map[string]any{}
	for _, row := range list.Data {
		rows[row["conversationId"].(string)] = row
	}
	if rows["conversation-detail"]["messageCount"] != float64(2) {
		t.Fatalf("list response=%s", listRecorder.Body.String())
	}
	if rows["conversation-detail"]["apiKeyId"] != keyRecord.ID || rows["conversation-detail"]["apiKeyName"] != "console-key" {
		t.Fatalf("attributed row=%v", rows["conversation-detail"])
	}
	if name, has := rows["conversation-anon"]["apiKeyName"]; has && name != "" {
		t.Fatalf("anonymous row must not carry apiKeyName: %v", rows["conversation-anon"])
	}

	detailRecorder := httptest.NewRecorder()
	s.handleM365ConversationDetail(detailRecorder, httptest.NewRequest(http.MethodGet, "/api/m365/conversations/detail?id=conversation-detail", nil))
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detailRecorder.Code, detailRecorder.Body.String())
	}
	var detail struct {
		ConversationID string   `json:"conversationId"`
		APIKeyName     string   `json:"apiKeyName"`
		Messages       []oaiMsg `json:"messages"`
	}
	if err := json.Unmarshal(detailRecorder.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ConversationID != "conversation-detail" || len(detail.Messages) != 2 {
		t.Fatalf("detail response=%s", detailRecorder.Body.String())
	}
	if detail.APIKeyName != "console-key" {
		t.Fatalf("detail apiKeyName=%q, want console-key", detail.APIKeyName)
	}
	if detail.Messages[1].ReasoningContent != "complete reasoning" || contentToString(detail.Messages[1].Content) != "complete body" {
		t.Fatalf("assistant message=%#v", detail.Messages[1])
	}
}

func TestConversationDetailPageContainsCompleteViews(t *testing.T) {
	body, err := os.ReadFile("../../web/conversation.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, needle := range []string{
		`id="conversationView"`,
		`id="jsonView"`,
		"reasoning_content",
		"tool_calls",
		"/api/m365/conversations/detail?id=",
	} {
		if !strings.Contains(page, needle) {
			t.Fatalf("conversation page missing %q", needle)
		}
	}
}

func TestConversationTimestampPrefersUpdateTime(t *testing.T) {
	created := time.Now().Add(-time.Hour).UnixMilli()
	updated := time.Now().UnixMilli()
	if got := conversationTimestamp(map[string]any{"createTimeUtc": created, "updateTimeUtc": updated}); got != updated {
		t.Fatalf("timestamp=%d want %d", got, updated)
	}
}
