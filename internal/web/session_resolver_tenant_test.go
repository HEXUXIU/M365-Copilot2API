package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// tenantReq builds a chat request that carries an API key (so it maps to a
// tenant) plus a fixed IP/UA, so the only thing that differs between two
// callers in these tests is the API key -- isolating the tenant dimension.
func tenantReq(apiKey, ip, ua string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	r.RemoteAddr = ip + ":12345"
	r.Header.Set("User-Agent", ua)
	r.Header.Set("Authorization", "Bearer "+apiKey)
	return r
}

func newTenantResolver(t *testing.T) *sessionResolver {
	t.Helper()
	t.Setenv("M365_SESSION_CACHE", filepath.Join(t.TempDir(), "sessions.json"))
	return openSessionResolver()
}

// TestContentKeyDoesNotCrossTenant is the core regression for the cross-tenant
// history-reuse defect: two API keys sending the *identical* message prefix
// from the same IP/UA must not resolve onto each other's cloud conversation.
func TestContentKeyDoesNotCrossTenant(t *testing.T) {
	sr := newTenantResolver(t)
	msgs := []oaiMsg{{Role: "user", Content: "secret prefix"}, {Role: "assistant", Content: "ok"}}

	sr.Bind("", "conv-alice", "acc-alice",
		&oaiReq{Messages: msgs}, "",
		tenantReq("key-alice", "203.0.113.9", "same-ua"))

	// Same IP/UA, same content, DIFFERENT key -> must be treated as new.
	res := sr.Resolve(tenantReq("key-bob", "203.0.113.9", "same-ua"),
		&oaiReq{Messages: append(append([]oaiMsg{}, msgs...), oaiMsg{Role: "user", Content: "more"})})
	if !res.IsNew {
		t.Fatalf("tenant bob reused tenant alice's conversation %q (matched %q)", res.ConversationID, res.MatchedBy)
	}

	// Same key -> still reuses, proving isolation didn't break normal caching.
	res = sr.Resolve(tenantReq("key-alice", "203.0.113.9", "same-ua"),
		&oaiReq{Messages: append(append([]oaiMsg{}, msgs...), oaiMsg{Role: "user", Content: "more"})})
	if res.IsNew || res.ConversationID != "conv-alice" {
		t.Fatalf("same-tenant reuse broken: IsNew=%v conv=%q", res.IsNew, res.ConversationID)
	}
}

// TestExplicitSessionIDIsolatedByTenant proves two tenants may use the same
// X-M365-Session-Id string without colliding, and neither can resume the
// other's binding via that id.
func TestExplicitSessionIDIsolatedByTenant(t *testing.T) {
	sr := newTenantResolver(t)

	bind := func(key, conv string) {
		r := tenantReq(key, "203.0.113.9", "ua")
		r.Header.Set("X-M365-Session-Id", "shared-id")
		sr.Bind("", conv, "acc-"+key, &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "hi " + key}}}, "", r)
	}
	resolve := func(key string) ResolveResult {
		r := tenantReq(key, "203.0.113.9", "ua")
		r.Header.Set("X-M365-Session-Id", "shared-id")
		return sr.Resolve(r, &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "hi " + key}}})
	}

	bind("alice", "conv-alice")
	bind("bob", "conv-bob")

	if got := resolve("alice"); got.IsNew || got.ConversationID != "conv-alice" {
		t.Fatalf("alice explicit resume: IsNew=%v conv=%q, want conv-alice", got.IsNew, got.ConversationID)
	}
	if got := resolve("bob"); got.IsNew || got.ConversationID != "conv-bob" {
		t.Fatalf("bob explicit resume: IsNew=%v conv=%q, want conv-bob", got.IsNew, got.ConversationID)
	}
}

// TestGetDeleteListScopedByTenant covers the /v1/sessions read/delete surface:
// one tenant can neither read, enumerate, nor delete another tenant's binding.
func TestGetDeleteListScopedByTenant(t *testing.T) {
	sr := newTenantResolver(t)
	tenantA := tenantFromRequest(tenantReq("key-a", "203.0.113.1", "ua"))
	tenantB := tenantFromRequest(tenantReq("key-b", "203.0.113.2", "ua"))

	sr.Bind("sess-a", "conv-a", "acc-a", &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "a"}}}, "", tenantReq("key-a", "203.0.113.1", "ua"))
	sr.Bind("sess-b", "conv-b", "acc-b", &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "b"}}}, "", tenantReq("key-b", "203.0.113.2", "ua"))

	// Cross-tenant reads must miss.
	if _, ok := sr.GetSession(tenantB, "sess-a"); ok {
		t.Error("tenant B read tenant A's session")
	}
	if _, ok := sr.GetSession(tenantA, "sess-b"); ok {
		t.Error("tenant A read tenant B's session")
	}
	// Own reads must hit.
	if _, ok := sr.GetSession(tenantA, "sess-a"); !ok {
		t.Error("tenant A cannot read its own session")
	}

	// Listing is scoped.
	if list := sr.ListSessionsForTenant(tenantA); len(list) != 1 || list[0].SessionID != "sess-a" {
		t.Errorf("tenant A listing leaked or lost sessions: %+v", list)
	}

	// Cross-tenant delete must be denied and leave the victim intact.
	if sr.DeleteSession(tenantB, "sess-a") {
		t.Error("tenant B deleted tenant A's session")
	}
	if _, ok := sr.GetSession(tenantA, "sess-a"); !ok {
		t.Error("tenant A's session was destroyed by a cross-tenant delete")
	}
	// Own delete works.
	if !sr.DeleteSession(tenantA, "sess-a") {
		t.Error("tenant A could not delete its own session")
	}
}
