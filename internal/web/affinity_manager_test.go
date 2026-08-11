package web

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"m365-copilot2api/internal/auth"
)

type responseReadErrorStore struct {
	affinityStore
}

func (s responseReadErrorStore) GetResponse(context.Context, string, string) (string, bool, error) {
	return "", false, errors.New("temporary response mapping read failure")
}

func TestAffinityManagerConfirmsOnlySuccessfulExactContinuation(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second, AccountConcurrency: 8})
	defer manager.close()
	accounts := []auth.AccountToken{{ID: "a"}, {ID: "b"}}
	available := func(string) bool { return true }
	ctx := context.Background()

	firstBody := &oaiReq{Model: "gpt-5.6", Messages: []oaiMsg{{Role: "user", Content: "hello"}}}
	first, err := manager.begin(ctx, "tenant", firstBody, httptest.NewRequest("POST", "/v1/chat/completions", nil), accounts, available)
	if err != nil {
		t.Fatal(err)
	}
	first.apply(firstBody)
	if first.accountID == "" || first.incremental {
		t.Fatalf("invalid cold state: %s", first)
	}
	usage := first.complete(ctx, firstBody, first.accountID, "conv-1", "sess-1", oaiMsg{Role: "assistant", Content: "hi"}, 10, 2)
	first.close()
	if usage.Confirmed || usage.CachedTokens != 0 {
		t.Fatalf("cold request claimed cache: %+v", usage)
	}

	secondBody := &oaiReq{Model: "gpt-5.6", Messages: []oaiMsg{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi"}, {Role: "user", Content: "continue"}}}
	second, err := manager.begin(ctx, "tenant", secondBody, httptest.NewRequest("POST", "/v1/chat/completions", nil), accounts, available)
	if err != nil {
		t.Fatal(err)
	}
	defer second.close()
	second.apply(secondBody)
	if !second.incremental || secondBody.ConversationID != "conv-1" || second.accountID != first.accountID {
		t.Fatalf("warm continuation was not pinned: state=%s conversation=%q", second, secondBody.ConversationID)
	}
	usage = second.complete(ctx, secondBody, second.accountID, "conv-1", "sess-1", oaiMsg{Role: "assistant", Content: "more"}, 18, 3)
	if !usage.Confirmed || usage.CachedTokens <= 0 {
		t.Fatalf("successful exact continuation did not report cache: %+v", usage)
	}
}

func TestAffinityManagerMigrationNeverClaimsCache(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second, AccountConcurrency: 8})
	defer manager.close()
	ctx := context.Background()
	accounts := []auth.AccountToken{{ID: "a"}, {ID: "b"}}
	available := func(string) bool { return true }
	body := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "hello"}}}
	first, _ := manager.begin(ctx, "tenant", body, httptest.NewRequest("POST", "/", nil), accounts, available)
	first.apply(body)
	first.complete(ctx, body, first.accountID, "conv-a", "sess-a", oaiMsg{Role: "assistant", Content: "hi"}, 10, 2)
	first.close()

	nextBody := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi"}, {Role: "user", Content: "more"}}}
	next, _ := manager.begin(ctx, "tenant", nextBody, httptest.NewRequest("POST", "/", nil), accounts, available)
	next.apply(nextBody)
	next.markMigration("rate_limit")
	usage := next.complete(ctx, nextBody, "b", "conv-b", "sess-b", oaiMsg{Role: "assistant", Content: "migrated"}, 20, 3)
	next.close()
	if usage.Confirmed || usage.CachedTokens != 0 {
		t.Fatalf("migration claimed cache: %+v", usage)
	}
}

func TestExplicitSessionContinuesWithoutResendingHistory(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second, AccountConcurrency: 8})
	defer manager.close()
	ctx := context.Background()
	accounts := []auth.AccountToken{{ID: "a"}, {ID: "b"}}
	available := func(string) bool { return true }

	firstRequest := httptest.NewRequest("POST", "/", nil)
	firstRequest.Header.Set(sessionHeaderName, "client-session")
	firstBody := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "first"}}}
	first, err := manager.begin(ctx, "tenant", firstBody, firstRequest, accounts, available)
	if err != nil {
		t.Fatal(err)
	}
	first.apply(firstBody)
	first.complete(ctx, firstBody, first.accountID, "conv-a", "sess-a", oaiMsg{Role: "assistant", Content: "answer"}, 5, 2)
	first.close()

	nextRequest := httptest.NewRequest("POST", "/", nil)
	nextRequest.Header.Set(sessionHeaderName, "client-session")
	nextBody := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "next only"}}}
	next, err := manager.begin(ctx, "tenant", nextBody, nextRequest, accounts, available)
	if err != nil {
		t.Fatal(err)
	}
	defer next.close()
	next.apply(nextBody)
	if !next.incremental || nextBody.ConversationID != "conv-a" || nextBody.SessionID != "sess-a" {
		t.Fatalf("explicit continuation lost cloud binding: state=%s body=%+v", next, nextBody)
	}
	usage := next.complete(ctx, nextBody, next.accountID, "conv-a", "sess-a", oaiMsg{Role: "assistant", Content: "next answer"}, 3, 2)
	if usage.Confirmed || usage.CachedTokens != 0 {
		t.Fatalf("explicit continuation without a proven prefix claimed cache: %+v", usage)
	}
}

func TestSharedAccountHealthExcludesCoolingAccount(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second, AccountConcurrency: 8})
	defer manager.close()
	ctx := context.Background()
	if err := manager.fallback.SetAccountHealth(ctx, "a", affinityAccountHealth{CooldownUntil: time.Now().Add(time.Hour)}, time.Hour); err != nil {
		t.Fatal(err)
	}
	body := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "hello"}}}
	state, err := manager.begin(ctx, "tenant", body, httptest.NewRequest("POST", "/", nil), []auth.AccountToken{{ID: "a"}, {ID: "b"}}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer state.close()
	if state.accountID != "b" {
		t.Fatalf("cooling account selected: %q", state.accountID)
	}
}

func TestPreviousResponseAliasResolvesCloudBinding(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second, AccountConcurrency: 8})
	defer manager.close()
	ctx := context.Background()
	accounts := []auth.AccountToken{{ID: "a"}}
	rootID := "resp-root"
	firstRequest := httptest.NewRequest("POST", "/v1/responses", nil)
	firstRequest.Header.Set(previousResponseHeader, rootID)
	firstBody := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "first"}}}
	first, err := manager.begin(ctx, "tenant", firstBody, firstRequest, accounts, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	first.apply(firstBody)
	first.complete(ctx, firstBody, first.accountID, "conv-a", "sess-a", oaiMsg{Role: "assistant", Content: "answer"}, 5, 2)
	first.close()
	manager.bindResponse(ctx, "tenant", "resp-next", rootID)

	nextRequest := httptest.NewRequest("POST", "/v1/responses", nil)
	nextRequest.Header.Set(previousResponseHeader, "resp-next")
	nextBody := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "continue after restart"}}}
	next, err := manager.begin(ctx, "tenant", nextBody, nextRequest, accounts, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer next.close()
	next.apply(nextBody)
	if !next.incremental || nextBody.ConversationID != "conv-a" || nextBody.SessionID != "sess-a" {
		t.Fatalf("response alias did not restore binding: state=%s body=%+v", next, nextBody)
	}
}

func TestBindResponseSkipsAliasWhenBindingCannotBeVerified(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second, AccountConcurrency: 8})
	defer manager.close()
	ctx := context.Background()
	tenant := "tenant"
	tenantHash := hashString(tenant)
	rootHash := affinityExplicitHash(tenantHash, "previous_response", "resp-root")
	aliasHash := affinityExplicitHash(tenantHash, "previous_response", "resp-alias")
	nextHash := affinityExplicitHash(tenantHash, "previous_response", "resp-next")
	primary := newMemoryAffinityStore(time.Hour, 100)
	if err := primary.PutBinding(ctx, affinityBinding{ID: rootHash, TenantHash: tenantHash, AccountID: "a", ConversationID: "conv-a", SessionID: "sess-a"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := primary.SetResponse(ctx, tenantHash, aliasHash, rootHash, time.Hour); err != nil {
		t.Fatal(err)
	}
	manager.primary = responseReadErrorStore{affinityStore: primary}

	manager.bindResponse(ctx, tenant, "resp-next", "resp-alias")

	if bindingID, ok, err := primary.GetResponse(ctx, tenantHash, nextHash); err != nil || ok {
		t.Fatalf("unverified response alias was stored: binding=%q ok=%t err=%v", bindingID, ok, err)
	}
}

func TestFiftyExplicitSessionsRespectAccountConcurrency(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second, AccountConcurrency: 8})
	defer manager.close()
	accounts := make([]auth.AccountToken, 7)
	for i := range accounts {
		accounts[i].ID = fmt.Sprintf("account-%d", i)
	}
	states := make([]*affinityRequest, 0, 50)
	counts := map[string]int{}
	for i := 0; i < 50; i++ {
		request := httptest.NewRequest("POST", "/", nil)
		request.Header.Set(sessionHeaderName, fmt.Sprintf("session-%d", i))
		body := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: fmt.Sprintf("request-%d", i)}}}
		state, err := manager.begin(context.Background(), "tenant", body, request, accounts, func(string) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, state)
		counts[state.accountID]++
		if counts[state.accountID] > 8 {
			t.Fatalf("account %s exceeded concurrency: %d", state.accountID, counts[state.accountID])
		}
	}
	for _, state := range states {
		state.close()
	}
}

func TestFiftyContinuationsSerializeOnOneLock(t *testing.T) {
	store := newMemoryAffinityStore(time.Hour, 100)
	var active int64
	var maximum int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := store.Acquire(context.Background(), "tenant:one-binding", time.Second, 5*time.Second)
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			current := atomic.AddInt64(&active, 1)
			for {
				old := atomic.LoadInt64(&maximum)
				if current <= old || atomic.CompareAndSwapInt64(&maximum, old, current) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt64(&active, -1)
			release()
		}()
	}
	wg.Wait()
	if maximum != 1 {
		t.Fatalf("same binding ran concurrently: max=%d", maximum)
	}
}

func TestObserveModeDoesNotChangeRouting(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityObserve, TTL: time.Hour, MaxSessions: 100, AccountConcurrency: 8})
	defer manager.close()
	body := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "observe"}}}
	state, err := manager.begin(context.Background(), "tenant", body, httptest.NewRequest("POST", "/", nil), []auth.AccountToken{{ID: "a"}}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer state.close()
	state.apply(body)
	if state.proposedAccount != "a" || body.AccountID != "" || body.ConversationID != "" {
		t.Fatalf("observe mode changed routing: state=%s body=%+v", state, body)
	}
}

func TestRedisOutageFallsBackWithoutCacheClaim(t *testing.T) {
	mr := miniredis.RunT(t)
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, RedisURL: "redis://" + mr.Addr() + "/0", RedisPoolSize: 2, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second, AccountConcurrency: 8})
	defer manager.close()
	mr.Close()
	request := httptest.NewRequest("POST", "/", nil)
	request.Header.Set(sessionHeaderName, "outage-session")
	body := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "hello"}}}
	state, err := manager.begin(context.Background(), "tenant", body, request, []auth.AccountToken{{ID: "a"}}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer state.close()
	if state.store != manager.fallback || state.accountID != "a" {
		t.Fatalf("redis outage did not use fallback: state=%s", state)
	}
	state.apply(body)
	usage := state.complete(context.Background(), body, "a", "conv-a", "sess-a", oaiMsg{Role: "assistant", Content: "answer"}, 5, 2)
	if usage.Confirmed || usage.CachedTokens != 0 {
		t.Fatalf("fallback cold request claimed cache: %+v", usage)
	}
}
