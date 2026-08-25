package web

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func TestRedisAffinityStoreRoundTripCASAndTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := newRedisAffinityStore("redis://"+mr.Addr()+"/0", 4, time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := store.SetAccount(ctx, "tenant", "affinity", "account-a", time.Hour); err != nil {
		t.Fatal(err)
	}
	if account, ok, err := store.GetAccount(ctx, "tenant", "affinity"); err != nil || !ok || account != "account-a" {
		t.Fatalf("account=%q ok=%v err=%v", account, ok, err)
	}
	if err := store.SetResponse(ctx, "tenant", "response", "binding", time.Hour); err != nil {
		t.Fatal(err)
	}
	if bindingID, ok, err := store.GetResponse(ctx, "tenant", "response"); err != nil || !ok || bindingID != "binding" {
		t.Fatalf("response binding=%q ok=%v err=%v", bindingID, ok, err)
	}
	health := affinityAccountHealth{CooldownUntil: time.Now().Add(10 * time.Minute)}
	if err := store.SetAccountHealth(ctx, "account-a", health, time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := store.GetAccountHealth(ctx, "account-a"); err != nil || !ok || got.CooldownUntil.IsZero() {
		t.Fatalf("health=%+v ok=%v err=%v", got, ok, err)
	}
	if err := store.ClearAccountHealth(ctx, "account-a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetAccountHealth(ctx, "account-a"); err != nil || ok {
		t.Fatalf("cleared health still present: ok=%v err=%v", ok, err)
	}

	binding := affinityBinding{ID: "binding", TenantHash: "tenant", AccountID: "account-a", ConversationID: "conv-a", SessionID: "sess-a", HistoryDigest: "history-a", HistoryCount: 2, Generation: 1}
	if err := store.PutBinding(ctx, binding, time.Hour); err != nil {
		t.Fatal(err)
	}
	got, index, ok, err := store.FindHistory(ctx, "tenant", []string{"missing", "history-a"})
	if err != nil || !ok || index != 1 || got.ConversationID != "conv-a" {
		t.Fatalf("history lookup got=%+v index=%d ok=%v err=%v", got, index, ok, err)
	}

	replacement := binding
	replacement.AccountID = "account-b"
	replacement.ConversationID = "conv-b"
	replacement.HistoryDigest = "history-b"
	replacement.Generation = 2
	if ok, err := store.CompareAndSwapBinding(ctx, binding.ID, 1, replacement, time.Hour); err != nil || !ok {
		t.Fatalf("CAS ok=%v err=%v", ok, err)
	}
	if _, _, ok, err := store.FindHistory(ctx, "tenant", []string{"history-a"}); err != nil || ok {
		t.Fatalf("old history index survived migration: ok=%v err=%v", ok, err)
	}
	if got, _, ok, err := store.FindHistory(ctx, "tenant", []string{"history-b"}); err != nil || !ok || got.AccountID != "account-b" {
		t.Fatalf("new history lookup got=%+v ok=%v err=%v", got, ok, err)
	}

	mr.FastForward(2 * time.Hour)
	if _, ok, err := store.GetAccount(ctx, "tenant", "affinity"); err != nil || ok {
		t.Fatalf("expired account affinity still present: ok=%v err=%v", ok, err)
	}
	if _, ok, err := store.GetBinding(ctx, binding.ID); err != nil || ok {
		t.Fatalf("expired binding still present: ok=%v err=%v", ok, err)
	}
}

func TestRedisAffinityLockChecksOwner(t *testing.T) {
	mr := miniredis.RunT(t)
	store, err := newRedisAffinityStore("redis://"+mr.Addr()+"/0", 4, time.Hour, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	release, err := store.Acquire(ctx, "tenant:binding", time.Minute, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Acquire(ctx, "tenant:binding", time.Minute, 30*time.Millisecond); err == nil {
		t.Fatal("second owner acquired a held lock")
	}
	release()
	secondRelease, err := store.Acquire(ctx, "tenant:binding", time.Minute, time.Second)
	if err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
	secondRelease()
}
