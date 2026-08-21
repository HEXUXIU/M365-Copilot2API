package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"m365-copilot2api/internal/auth"
)

func TestRedisAffinityConfigReadsAdapterEnvironment(t *testing.T) {
	t.Setenv("M365_AFFINITY_MODE", "observe")
	t.Setenv("M365_REDIS_URL", "redis://127.0.0.1:6379/3")
	t.Setenv("M365_REDIS_POOL_SIZE", "17")
	config := loadAffinityConfig()
	if config.Mode != affinityObserve || config.RedisURL != "redis://127.0.0.1:6379/3" || config.RedisPoolSize != 17 {
		t.Fatalf("unexpected Redis affinity config: %+v", config)
	}
}

func TestRequestCancellationDoesNotDegradeRedisAffinity(t *testing.T) {
	mr := miniredis.RunT(t)
	manager := openAffinityManager(affinityConfig{
		Mode: affinityEnforce, RedisURL: "redis://" + mr.Addr() + "/0",
		RedisPoolSize: 2, TTL: time.Hour, MaxSessions: 100,
		LockTTL: time.Minute, LockWait: time.Second,
	})
	defer manager.close()

	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		if store := manager.markStoreError(err); store != manager.primary {
			t.Fatalf("request cancellation selected fallback store: %v", err)
		}
		status := manager.status()
		if status["degraded"] != false || status["store"] != "redis" {
			t.Fatalf("request cancellation degraded Redis: err=%v status=%v", err, status)
		}
	}

	manager.markStoreError(errors.New("redis connection failed"))
	status := manager.status()
	if status["degraded"] != true || status["store"] != "memory_fallback" {
		t.Fatalf("real store error did not select memory fallback: %v", status)
	}
}

func TestRedisOutageFallsBackWithoutCacheClaim(t *testing.T) {
	mr := miniredis.RunT(t)
	manager := openAffinityManager(affinityConfig{
		Mode: affinityEnforce, RedisURL: "redis://" + mr.Addr() + "/0",
		RedisPoolSize: 2, TTL: time.Hour, MaxSessions: 100,
		LockTTL: time.Minute, LockWait: time.Second,
	})
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
