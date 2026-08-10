package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m365-copilot2api/internal/auth"
)

func TestUpstreamErrorClassification(t *testing.T) {
	cases := []struct {
		err      error
		limited  bool
		authFail bool
		retry    int
		status   int
	}{
		{&UpstreamHTTPError{Status: 429, RetryAfter: 90}, true, false, 90, http.StatusTooManyRequests},
		{&UpstreamHTTPError{Status: 503}, true, false, 0, http.StatusTooManyRequests},
		{&UpstreamHTTPError{Status: 401}, false, true, 0, http.StatusUnauthorized},
		{&UpstreamHTTPError{Status: 403}, false, true, 0, http.StatusUnauthorized},
		{&UpstreamHTTPError{Status: 502}, false, false, 0, http.StatusBadGateway},
		{fmt.Errorf("upstream http 429"), true, false, 0, http.StatusTooManyRequests},
		{fmt.Errorf("Too many requests, slow down"), true, false, 0, http.StatusTooManyRequests},
		{fmt.Errorf("random failure"), false, false, 0, http.StatusBadGateway},
	}
	for _, c := range cases {
		if got := IsRateLimited(c.err); got != c.limited {
			t.Errorf("IsRateLimited(%v)=%v want %v", c.err, got, c.limited)
		}
		if got := IsAuthFailure(c.err); got != c.authFail {
			t.Errorf("IsAuthFailure(%v)=%v want %v", c.err, got, c.authFail)
		}
		if got := RetryAfterSeconds(c.err); got != c.retry {
			t.Errorf("RetryAfterSeconds(%v)=%d want %d", c.err, got, c.retry)
		}
		if got := upstreamStatus(c.err); got != c.status {
			t.Errorf("upstreamStatus(%v)=%d want %d", c.err, got, c.status)
		}
	}
}

func TestAccountHealthLifecycle(t *testing.T) {
	h := newAccountHealth()
	const id = "acct-1"

	if !h.Available(id) {
		t.Fatal("fresh account must be available")
	}
	h.MarkFailure(id, &UpstreamHTTPError{Status: 429}, 10*time.Minute)
	if h.Available(id) {
		t.Fatal("rate-limited account must be in cooldown")
	}
	h.MarkSuccess(id)
	if !h.Available(id) {
		t.Fatal("MarkSuccess must lift the cooldown")
	}

	h.MarkFailure(id, &UpstreamHTTPError{Status: 401}, 0)
	if h.Available(id) {
		t.Fatal("auth-failed account must stay unusable")
	}
	h.MarkSuccess(id)
	if !h.Available(id) {
		t.Fatal("MarkSuccess must clear auth failure")
	}

	h.MarkFailure(id, &UpstreamHTTPError{Status: 429}, 10*time.Minute)
	until := h.Snapshot()[id]
	if until == nil || until["available"].(bool) || until["cooldownUntil"] == nil {
		t.Fatalf("snapshot should report cooldown until: %v", h.Snapshot())
	}
}

func testAccountFiles(t *testing.T) *auth.Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "accounts.json")
	toks := map[string]auth.TokenSet{
		"u-1": {HomeOID: "u-1", Email: "one@example.com", AccessToken: "tok1", RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour)},
		"u-2": {HomeOID: "u-2", Email: "two@example.com", AccessToken: "tok2", RefreshToken: "r2", ExpiresAt: time.Now().Add(time.Hour)},
		"u-3": {HomeOID: "u-3", Email: "three@example.com", AccessToken: "tok3", RefreshToken: "r3", ExpiresAt: time.Now().Add(time.Hour)},
	}
	b, _ := os.ReadFile(path)
	_ = b
	store, err := auth.OpenStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	for _, tok := range toks {
		if _, err := store.Upsert(tok); err != nil {
			t.Fatalf("upsert %s: %v", tok.HomeOID, err)
		}
	}
	return store
}

func TestWriteUpstreamErrorHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	writeUpstreamError(w, &UpstreamHTTPError{Status: 429, RetryAfter: 90})
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "90" {
		t.Fatalf("Retry-After=%q want 90", got)
	}
	if strings.Contains(w.Body.String(), "429") {
		t.Fatalf("client-visible body must not leak upstream status: %q", w.Body.String())
	}

	w = httptest.NewRecorder()
	writeUpstreamError(w, &UpstreamHTTPError{Status: 502})
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d want 502", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After=%q want empty for non-rate-limited errors", got)
	}
}

func TestResolveAccountSkipsUnhealthy(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store, accountPool: newAccountHealth()}

	s.accountPool.MarkFailure("u-1", &UpstreamHTTPError{Status: 429}, 10*time.Minute)
	acc, err := s.resolveAccount("")
	if err != nil {
		t.Fatalf("resolveAccount: %v", err)
	}
	if acc.ID == "u-1" {
		t.Fatalf("resolveAccount must skip the cooling-down account, got %s", acc.ID)
	}
	if acc.Email == "" {
		t.Fatal("resolveAccount should return a validated account")
	}
}

func TestResolveAccountAllUnhealthy(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store, accountPool: newAccountHealth()}
	for _, id := range []string{"u-1", "u-2", "u-3"} {
		s.accountPool.MarkFailure(id, &UpstreamHTTPError{Status: 429}, 10*time.Minute)
	}
	if _, err := s.resolveAccount(""); err == nil {
		t.Fatal("resolveAccount must fail when every account is cooling down")
	}
}

func TestNextHealthyAccount(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store, accountPool: newAccountHealth()}

	s.accountPool.MarkFailure("u-2", &UpstreamHTTPError{Status: 429}, 10*time.Minute)
	acc, err := s.nextHealthyAccount("u-1")
	if err != nil {
		t.Fatalf("nextHealthyAccount: %v", err)
	}
	if acc.ID == "u-1" || acc.ID == "u-2" {
		t.Fatalf("nextHealthyAccount must skip the avoided and the unhealthy account, got %s", acc.ID)
	}
	if acc.ID != "u-3" {
		t.Fatalf("expected u-3, got %s", acc.ID)
	}

	for _, id := range []string{"u-1", "u-2", "u-3"} {
		s.accountPool.MarkFailure(id, &UpstreamHTTPError{Status: 429}, 10*time.Minute)
	}
	if _, err := s.nextHealthyAccount(""); err == nil {
		t.Fatal("nextHealthyAccount must fail when no healthy account remains")
	}
}