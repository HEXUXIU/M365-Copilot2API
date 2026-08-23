package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestServerForAutoCleanup(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("M365_SESSION_CACHE", filepath.Join(dir, "sessions.json"))
	t.Setenv("M365_CONVERSATION_CACHE", filepath.Join(dir, "conversations.json"))
	t.Setenv("M365_USER_SESSION_CACHE", filepath.Join(dir, "users.json"))
	return &Server{
		sessions:            openSessionStore(),
		userSessions:        openUserSessionStore(30 * time.Minute),
		sessionResolver:     openSessionResolver(),
		conversationManager: openConversationManager(),
	}
}

func TestAutoCleanupActiveSetProtectsInUse(t *testing.T) {
	s := newTestServerForAutoCleanup(t)

	s.conversationManager.Record("conv-active", "acc1", "active convo")
	s.conversationManager.Record("conv-idle", "acc1", "idle convo")
	s.userSessions.Put("", "alice", "conv-user", "sess-user", "acc1")

	// Simulate activity: active convo touched recently, idle long ago.
	cm := s.conversationManager
	cm.mu.Lock()
	entry := cm.data["conv-active"]
	entry.LastUsedAt = time.Now().UTC()
	cm.data["conv-active"] = entry
	idle := cm.data["conv-idle"]
	idle.LastUsedAt = time.Now().UTC().Add(-48 * time.Hour)
	cm.data["conv-idle"] = idle
	cm.mu.Unlock()

	active := s.activeConversationSet(24 * time.Hour)

	if !active["conv-active"] {
		t.Error("conv-active (recently used) should be protected")
	}
	if !active["conv-user"] {
		t.Error("conv-user (active user session) should be protected")
	}
	if active["conv-idle"] {
		t.Error("conv-idle (48h unused) should be cleanable")
	}
}

func TestAutoCleanupWhitelistProtects(t *testing.T) {
	s := newTestServerForAutoCleanup(t)
	s.conversationManager.Record("conv-pinned", "acc1", "pinned")
	s.conversationManager.Whitelist("conv-pinned")

	active := s.activeConversationSet(0)
	if !active["conv-pinned"] {
		t.Error("whitelisted conversation must never be cleaned")
	}
}

func TestUnbindByConversationRemovesBindings(t *testing.T) {
	s := newTestServerForAutoCleanup(t)

	// Two different tenants each hold a binding to the same cloud conversation
	// conv-x. UnbindByConversation is global maintenance (a deleted cloud
	// conversation must be unbound for everyone), so it removes both.
	reqFor := func(key string) *http.Request {
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		return r
	}
	tenantA := tenantFromRequest(reqFor("key-a"))
	tenantB := tenantFromRequest(reqFor("key-b"))
	s.sessionResolver.Bind("sess-1", "conv-x", "acc1", &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "hi"}}}, "", reqFor("key-a"))
	s.sessionResolver.Bind("sess-2", "conv-x", "acc1", &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "hello"}}}, "", reqFor("key-b"))
	s.sessionResolver.Bind("sess-3", "conv-y", "acc1", &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "other"}}}, "", reqFor("key-a"))

	if removed := s.sessionResolver.UnbindByConversation("conv-x"); removed != 2 {
		t.Fatalf("expected 2 unbinds, got %d", removed)
	}
	if _, ok := s.sessionResolver.GetSession(tenantA, "sess-1"); ok {
		t.Error("sess-1 should be gone")
	}
	if _, ok := s.sessionResolver.GetSession(tenantB, "sess-2"); ok {
		t.Error("sess-2 should be gone")
	}
	if _, ok := s.sessionResolver.GetSession(tenantA, "sess-3"); !ok {
		t.Error("sess-3 bound to conv-y must survive")
	}
}

func TestWhitelistPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversations.json")
	t.Setenv("M365_CONVERSATION_CACHE", path)

	cm1 := openConversationManager()
	cm1.Record("conv-pinned", "acc1", "pinned")
	cm1.Whitelist("conv-pinned")
	cm1.Record("conv-plain", "acc1", "plain")
	if err := cm1.persist.flushNowBlocking(); err != nil {
		t.Fatal(err)
	}

	cm2 := openConversationManager()
	if !cm2.IsWhitelisted("conv-pinned") {
		t.Error("whitelist lost after reload")
	}
	found := false
	for _, id := range cm2.WhitelistedIDs() {
		if id == "conv-pinned" {
			found = true
		}
	}
	if !found {
		t.Error("WhitelistedIDs missing pinned conversation")
	}
}

func TestWhitelistPersistsWithoutOtherActivity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversations.json")
	t.Setenv("M365_CONVERSATION_CACHE", path)

	cm1 := openConversationManager()
	cm1.Whitelist("conv-a")
	cm1.Whitelist("conv-b")
	cm1.Unwhitelist("conv-b")
	if err := cm1.persist.flushNowBlocking(); err != nil {
		t.Fatal(err)
	}

	cm2 := openConversationManager()
	if !cm2.IsWhitelisted("conv-a") {
		t.Error("whitelist entry conv-a must survive reload without unrelated Record activity")
	}
	if cm2.IsWhitelisted("conv-b") {
		t.Error("unwhitelisted conv-b must not survive reload")
	}
}

func TestAutoCleanupDisabledEnv(t *testing.T) {
	t.Setenv("M365_AUTO_CLEANUP", "0")
	s := newTestServerForAutoCleanup(t)
	s.StartAutoCleanup()
}

func TestLegacyConversationFileLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conversations.json")
	legacy := `{
  "conv-old": {
    "id": "conv-old",
    "accountId": "acc1",
    "createdAt": "2026-08-01T00:00:00Z",
    "lastUsedAt": "2026-08-01T00:00:00Z",
    "title": "legacy"
  }
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("M365_CONVERSATION_CACHE", path)

	cm := openConversationManager()
	if _, ok := cm.data["conv-old"]; !ok {
		t.Error("legacy conversation file must still load")
	}
}
