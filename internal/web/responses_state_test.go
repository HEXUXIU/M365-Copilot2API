package web

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func newResponseStateTestServer(config affinityConfig) *Server {
	if config.TTL == 0 {
		config.TTL = time.Hour
	}
	if config.LockTTL == 0 {
		config.LockTTL = time.Minute
	}
	if config.MaxSessions == 0 {
		config.MaxSessions = 100
	}
	return &Server{
		responseMessages: map[string]map[string]*RespNode{},
		affinity:         openAffinityManager(config),
	}
}

func putResponseStateParent(t *testing.T, server *Server, namespace, responseID string) {
	t.Helper()
	node := &RespNode{
		At: time.Now(), Tenant: "tenant", SessionID: "session", Version: 1,
		Messages: []oaiMsg{{Role: "assistant", ToolCalls: []map[string]any{{
			"id": "call-1", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{}`},
		}}}},
		ToolCalls: map[string]*ToolCallRecord{"call-1": {CallID: "call-1", Name: "lookup", Arguments: `{}`, Type: "function"}},
	}
	server.responseMu.Lock()
	server.persistResponseNodeLocked(context.Background(), namespace, responseID, node)
	server.responseMu.Unlock()
}

func TestResponseStateFailureReleasesLeaseEvenAfterCancellation(t *testing.T) {
	server := newResponseStateTestServer(affinityConfig{})
	defer server.affinity.close()
	namespace := responseNamespace("tenant", "session")
	putResponseStateParent(t, server, namespace, "resp-parent")
	body := responsesRequest{Model: "gpt-test", PreviousResponseID: "resp-parent", Input: []any{map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "ok"}}}
	digest := responseRequestDigest(body)
	claim, replay, err := server.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
	if err != nil || replay != nil || claim == nil {
		t.Fatalf("first claim=%#v replay=%#v err=%v", claim, replay, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	server.releaseResponseClaim(canceled, claim)
	next, replay, err := server.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
	if err != nil || replay != nil || next == nil || next.LeaseID == claim.LeaseID {
		t.Fatalf("retry claim=%#v replay=%#v err=%v", next, replay, err)
	}
	server.releaseResponseClaim(context.Background(), next)
}

func TestResponseStateCompletedRequestReplaysExactly(t *testing.T) {
	server := newResponseStateTestServer(affinityConfig{})
	defer server.affinity.close()
	namespace := responseNamespace("tenant", "session")
	putResponseStateParent(t, server, namespace, "resp-parent")
	body := responsesRequest{Model: "gpt-test", Stream: true, PreviousResponseID: "resp-parent", Input: []any{map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "ok"}}}
	digest := responseRequestDigest(body)
	claim, _, err := server.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := responseReplay{Status: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: []byte("event: response.completed\ndata: {}\n\n")}
	if err := server.finishResponseClaim(context.Background(), claim, "resp-child", want); err != nil {
		t.Fatal(err)
	}
	duplicate, replay, err := server.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
	if err != nil || duplicate != nil || replay == nil || string(replay.Body) != string(want.Body) || replay.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("duplicate=%#v replay=%#v err=%v", duplicate, replay, err)
	}
	body.Input = []any{map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "different"}}
	if _, _, err := server.claimResponse(context.Background(), namespace, "resp-parent", responseRequestDigest(body), "tenant", "session", []string{"call-1"}); err == nil {
		t.Fatal("different request reused a consumed parent")
	}
}

func TestResponseStateConcurrentDuplicatesWaitForSingleResult(t *testing.T) {
	server := newResponseStateTestServer(affinityConfig{})
	defer server.affinity.close()
	namespace := responseNamespace("tenant", "session")
	putResponseStateParent(t, server, namespace, "resp-parent")
	body := responsesRequest{Model: "gpt-test", PreviousResponseID: "resp-parent", Input: []any{map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "ok"}}}
	digest := responseRequestDigest(body)
	owner, _, err := server.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
	if err != nil {
		t.Fatal(err)
	}
	const workers = 10
	var claims atomic.Int64
	var replays atomic.Int64
	var failures atomic.Int64
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim, replay, claimErr := server.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
			if claimErr != nil {
				failures.Add(1)
				return
			}
			if claim != nil {
				claims.Add(1)
			}
			if replay != nil && string(replay.Body) == "same-result" {
				replays.Add(1)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	if err := server.finishResponseClaim(context.Background(), owner, "resp-child", responseReplay{Status: 200, Body: []byte("same-result")}); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if claims.Load() != 0 || replays.Load() != workers || failures.Load() != 0 {
		t.Fatalf("claims=%d replays=%d failures=%d", claims.Load(), replays.Load(), failures.Load())
	}
}

func TestResponseStateRedisSurvivesServerRestart(t *testing.T) {
	redisServer := miniredis.RunT(t)
	config := affinityConfig{
		Mode: affinityEnforce, RedisURL: "redis://" + redisServer.Addr() + "/0",
		RedisPoolSize: 4, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute,
	}
	namespace := responseNamespace("tenant", "session")
	first := newResponseStateTestServer(config)
	putResponseStateParent(t, first, namespace, "resp-parent")
	first.affinity.close()

	second := newResponseStateTestServer(config)
	body := responsesRequest{Model: "gpt-test", PreviousResponseID: "resp-parent", Input: []any{map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "ok"}}}
	digest := responseRequestDigest(body)
	claim, replay, err := second.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
	if err != nil || claim == nil || replay != nil {
		t.Fatalf("restored claim=%#v replay=%#v err=%v", claim, replay, err)
	}
	if err := second.finishResponseClaim(context.Background(), claim, "resp-child", responseReplay{Status: 200, Body: []byte("persisted-result")}); err != nil {
		t.Fatal(err)
	}
	second.affinity.close()

	third := newResponseStateTestServer(config)
	defer third.affinity.close()
	claim, replay, err = third.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
	if err != nil || claim != nil || replay == nil || string(replay.Body) != "persisted-result" {
		t.Fatalf("restored replay claim=%#v replay=%#v err=%v", claim, replay, err)
	}
	for _, key := range redisServer.Keys() {
		if key == "tenant" || key == "resp-parent" || key == namespace {
			t.Fatalf("Redis key exposed raw identity: %q", key)
		}
	}
}

func TestResponseStateLockContentionDoesNotDegradeRedis(t *testing.T) {
	redisServer := miniredis.RunT(t)
	server := newResponseStateTestServer(affinityConfig{
		Mode: affinityEnforce, RedisURL: "redis://" + redisServer.Addr() + "/0",
		RedisPoolSize: 4, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute,
	})
	defer server.affinity.close()
	namespace := responseNamespace("tenant", "session")
	key := "responses-state:" + hashString(namespace) + ":" + hashString("resp-parent")
	store := server.affinity.store(context.Background())
	release, err := store.Acquire(context.Background(), key, 10*time.Second, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := server.responseStateLock(ctx, namespace, "resp-parent"); !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, errAffinityLockTimeout) {
		t.Fatalf("responseStateLock() error = %v", err)
	}
	status := server.affinity.status()
	if degraded, _ := status["degraded"].(bool); degraded {
		t.Fatalf("ordinary lock contention degraded Redis: %#v", status)
	}
}

func TestResponseStateExpiredLeaseCanBeReclaimed(t *testing.T) {
	server := newResponseStateTestServer(affinityConfig{LockTTL: 20 * time.Millisecond})
	defer server.affinity.close()
	namespace := responseNamespace("tenant", "session")
	putResponseStateParent(t, server, namespace, "resp-parent")
	body := responsesRequest{Model: "gpt-test", PreviousResponseID: "resp-parent", Input: []any{map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "ok"}}}
	digest := responseRequestDigest(body)
	first, _, err := server.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	second, replay, err := server.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
	if err != nil || replay != nil || second == nil || second.LeaseID == first.LeaseID {
		t.Fatalf("expired lease was not reclaimed: first=%#v second=%#v replay=%#v err=%v", first, second, replay, err)
	}
	server.releaseResponseClaim(context.Background(), second)
}

func TestResponseStateRedisSerializesAcrossServers(t *testing.T) {
	redisServer := miniredis.RunT(t)
	config := affinityConfig{
		Mode: affinityEnforce, RedisURL: "redis://" + redisServer.Addr() + "/0",
		RedisPoolSize: 8, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute,
	}
	first := newResponseStateTestServer(config)
	defer first.affinity.close()
	second := newResponseStateTestServer(config)
	defer second.affinity.close()
	namespace := responseNamespace("tenant", "session")
	putResponseStateParent(t, first, namespace, "resp-parent")
	body := responsesRequest{Model: "gpt-test", PreviousResponseID: "resp-parent", Input: []any{map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "ok"}}}
	digest := responseRequestDigest(body)
	owner, _, err := first.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		claim  *responseClaim
		replay *responseReplay
		err    error
	}
	done := make(chan result, 1)
	go func() {
		claim, replay, err := second.claimResponse(context.Background(), namespace, "resp-parent", digest, "tenant", "session", []string{"call-1"})
		done <- result{claim: claim, replay: replay, err: err}
	}()
	time.Sleep(50 * time.Millisecond)
	if err := first.finishResponseClaim(context.Background(), owner, "resp-child", responseReplay{Status: 200, Body: []byte("shared-result")}); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.claim != nil || got.replay == nil || string(got.replay.Body) != "shared-result" {
		t.Fatalf("cross-server result=%#v", got)
	}
}
