package web

import (
	"path/filepath"
	"testing"
	"time"

	"m365-copilot2api/internal/auth"
)

// A preferred account whose token can no longer be validated (expired with no
// refresh token) must not short-circuit failover: routing has to move on to a
// healthy account and cool the dead one down.
func TestResolveAccountFailsOverPastDeadAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := auth.OpenStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	dead := auth.TokenSet{HomeOID: "u-dead", Email: "dead@example.com", AccessToken: "tok-dead", ExpiresAt: time.Now().Add(-time.Hour)}
	if _, err := store.Upsert(dead); err != nil {
		t.Fatalf("upsert dead: %v", err)
	}
	live := auth.TokenSet{HomeOID: "u-live", Email: "live@example.com", AccessToken: "tok-live", RefreshToken: "r-live", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := store.Upsert(live); err != nil {
		t.Fatalf("upsert live: %v", err)
	}

	s := &Server{tokens: store, accountPool: newAccountHealth(), accountConcurrency: newAccountConcurrency()}
	// Force the dead account to be tried first.
	s.mu.Lock()
	s.lastHealthyAccount = "u-dead"
	s.mu.Unlock()

	acc, err := s.resolveAccount("")
	if err != nil {
		t.Fatalf("resolveAccount should fail over, got error: %v", err)
	}
	if acc.ID != "u-live" {
		t.Fatalf("expected failover to u-live, got %q", acc.ID)
	}
	if s.accountAvailable("u-dead") {
		t.Fatal("the dead account should have been cooled down after a failed validation")
	}

	// A healthy account already warmed by the keeper must not accumulate
	// backoff generations: resolving it again has to succeed without cooling
	// or penalising it, otherwise warm rounds would lengthen its own outages.
	s.mu.Lock()
	s.lastHealthyAccount = "u-live"
	s.mu.Unlock()
	if acc2, err := s.resolveAccount(""); err != nil || acc2.ID != "u-live" {
		t.Fatalf("warm hit should stay on u-live, got %q err=%v", acc2.ID, err)
	}
	if _, limited, _ := s.accountPool.QuotaDetail("u-live"); limited {
		t.Fatal("repeated healthy resolves must not mark the account rate-limited")
	}
}

// A busy account is still the right owner of its sticky conversation; only a
// genuinely unusable account (cooling down, disabled) should be abandoned.
func TestAccountUsableIgnoresConcurrencyLimit(t *testing.T) {
	store := testAccountFiles(t)
	ac := &accountConcurrency{limit: 1, inflight: map[string]int{"u-1": 1}, changed: make(chan struct{})}
	s := &Server{tokens: store, accountPool: newAccountHealth(), accountConcurrency: ac}

	if s.accountAvailable("u-1") {
		t.Fatal("a full-concurrency account must not be reported available")
	}
	if !s.accountUsable("u-1") {
		t.Fatal("a busy-but-healthy account must still be usable for sticky routing")
	}

	s.accountPool.MarkFailure("u-2", &UpstreamHTTPError{Status: 429}, 10*time.Minute)
	if s.accountUsable("u-2") {
		t.Fatal("a cooling account must not be usable")
	}
}
