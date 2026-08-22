package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUpsertAndList(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	acc, err := store.Upsert(TokenSet{
		AccessToken:  "a",
		RefreshToken: "r",
		Email:        "a@example.com",
		DisplayName:  "A",
		HomeOID:      "oid-1",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if acc.Email != "a@example.com" {
		t.Fatalf("unexpected email: %s", acc.Email)
	}
	list := store.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 account, got %d", len(list))
	}
}

func TestScheduleEnabledPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	token := TokenSet{AccessToken: "a", RefreshToken: "r", Email: "a@example.com", HomeOID: "oid-1", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := store.Upsert(token); err != nil {
		t.Fatal(err)
	}
	if !store.ScheduleEnabled("oid-1") {
		t.Fatal("new account scheduling disabled")
	}
	if err := store.SetScheduleEnabled("oid-1", false); err != nil {
		t.Fatal(err)
	}
	if store.ScheduleEnabled("oid-1") {
		t.Fatal("account scheduling still enabled")
	}
	if _, err := store.Upsert(token); err != nil {
		t.Fatal(err)
	}
	if store.ScheduleEnabled("oid-1") {
		t.Fatal("upsert reset scheduling state")
	}
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.ScheduleEnabled("oid-1") {
		t.Fatal("scheduling state was not persisted")
	}
}

func TestRefreshAllDueRefreshesBeforeRequestPath(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-access",
			"refresh_token": "refreshed-refresh",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()
	t.Setenv("M365_TOKEN_ENDPOINT", tokenServer.URL)

	store, err := OpenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(TokenSet{
		AccessToken: "old-access", RefreshToken: "old-refresh", Email: "a@example.com",
		HomeOID: "oid-1", ExpiresAt: time.Now().Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	results := store.RefreshAllDue(5*time.Minute, 2)
	if len(results) != 1 || !results[0].Success {
		t.Fatalf("unexpected refresh results: %#v", results)
	}
	acc, err := store.EnsureValid("oid-1")
	if err != nil {
		t.Fatal(err)
	}
	if acc.AccessToken != "refreshed-access" {
		t.Fatalf("request path saw stale token %q", acc.AccessToken)
	}
	mu.Lock()
	gotRequests := requests
	mu.Unlock()
	if gotRequests != 1 {
		t.Fatalf("expected one OAuth request, got %d", gotRequests)
	}
}

func TestRefreshAllDueCoalescesWithConcurrentEnsure(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh", "refresh_token": "rotated", "expires_in": 3600,
		})
	}))
	defer tokenServer.Close()
	t.Setenv("M365_TOKEN_ENDPOINT", tokenServer.URL)

	store, err := OpenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(TokenSet{
		AccessToken: "old", RefreshToken: "refresh", Email: "a@example.com",
		HomeOID: "oid-1", ExpiresAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		_, err := store.EnsureValid("oid-1")
		errs <- err
	}()
	go func() {
		<-start
		results := store.RefreshAllDue(5*time.Minute, 1)
		if len(results) != 1 {
			errs <- errors.New("background refresh did not select account")
			return
		}
		if results[0].Success {
			errs <- nil
			return
		}
		errs <- errors.New(results[0].Error)
	}()
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	gotRequests := requests
	mu.Unlock()
	if gotRequests != 1 {
		t.Fatalf("expected one coalesced OAuth request, got %d", gotRequests)
	}
}
