package auth

import (
	"path/filepath"
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

func TestPriorityPersistsAcrossUpsertAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	token := TokenSet{AccessToken: "a", RefreshToken: "r", Email: "a@example.com", HomeOID: "oid-1", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := store.Upsert(token); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPriority("oid-1", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(token); err != nil {
		t.Fatal(err)
	}
	acc, ok := store.Get("oid-1")
	if !ok || acc.Priority != 10 {
		t.Fatalf("upsert reset priority: %+v", acc)
	}
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	acc, ok = reopened.Get("oid-1")
	if !ok || acc.Priority != 10 {
		t.Fatalf("priority was not persisted: %+v", acc)
	}
}

func TestSetPriorityRejectsNegativeValue(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPriority("missing", -1); err == nil {
		t.Fatal("negative priority was accepted")
	}
}
