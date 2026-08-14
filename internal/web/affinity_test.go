package web

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"m365-copilot2api/internal/auth"
)

func TestAffinityKeyIsDeterministicAndTenantIsolated(t *testing.T) {
	body := &oaiReq{
		Model: "gpt-5.6-reasoning",
		Messages: []oaiMsg{
			{Role: "system", Content: "be precise"},
			{Role: "user", Content: "hello"},
		},
	}

	a := deriveAffinityKey("tenant-a", body, httptest.NewRequest("POST", "/v1/chat/completions", nil))
	b := deriveAffinityKey("tenant-a", body, httptest.NewRequest("POST", "/v1/chat/completions", nil))
	c := deriveAffinityKey("tenant-b", body, httptest.NewRequest("POST", "/v1/chat/completions", nil))
	if a.Hash == "" || a.Hash != b.Hash {
		t.Fatalf("affinity hash is not deterministic: %q != %q", a.Hash, b.Hash)
	}
	if a.Hash == c.Hash || a.TenantHash == c.TenantHash {
		t.Fatal("affinity key must be isolated by tenant")
	}
}

func TestExplicitSessionTakesPriorityOverContentSeed(t *testing.T) {
	body := &oaiReq{Model: "gpt-5.6", PromptCacheKey: "cache-key", Messages: []oaiMsg{{Role: "user", Content: "hello"}}}
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set(sessionHeaderName, "session-123")

	key := deriveAffinityKey("tenant-a", body, r)
	if key.Reason != "explicit_session" {
		t.Fatalf("reason=%q want explicit_session", key.Reason)
	}
	if key.BindingID == "" {
		t.Fatal("explicit session must produce a stable binding id")
	}
}

func TestStatelessFirstTurnsShareAccountButDoNotShareConversation(t *testing.T) {
	body := &oaiReq{Model: "gpt-5.6", Messages: []oaiMsg{{Role: "user", Content: "same first turn"}}}
	keyA := deriveAffinityKey("tenant-a", body, httptest.NewRequest("POST", "/v1/chat/completions", nil))
	keyB := deriveAffinityKey("tenant-a", body, httptest.NewRequest("POST", "/v1/chat/completions", nil))
	if keyA.Hash != keyB.Hash {
		t.Fatal("identical first turns should share account affinity")
	}
	if keyA.BindingID != "" || keyB.BindingID != "" {
		t.Fatal("content affinity alone must not claim a conversation binding")
	}
}

func TestHistoryLookupRequiresAssistantBearingExactPrefix(t *testing.T) {
	store := newMemoryAffinityStore(time.Hour, 100)
	ctx := context.Background()
	tenant := hashString("tenant-a")
	history := []oaiMsg{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "hi"}}
	digest := historyDigest(history)
	binding := affinityBinding{ID: "binding-1", TenantHash: tenant, AccountID: "acc-1", ConversationID: "conv-1", SessionID: "sess-1", HistoryDigest: digest, HistoryCount: len(history), Generation: 1}
	if err := store.PutBinding(ctx, binding, time.Hour); err != nil {
		t.Fatal(err)
	}

	resolved, prefix, ok, err := resolveHistoryBinding(ctx, store, tenant, append(history, oaiMsg{Role: "user", Content: "continue"}), 64)
	if err != nil || !ok {
		t.Fatalf("exact prefix not resolved: ok=%v err=%v", ok, err)
	}
	if resolved.ID != binding.ID || prefix != len(history) {
		t.Fatalf("resolved=%+v prefix=%d", resolved, prefix)
	}

	diverged := []oaiMsg{{Role: "user", Content: "hello"}, {Role: "assistant", Content: "different"}, {Role: "user", Content: "continue"}}
	if _, _, ok, err := resolveHistoryBinding(ctx, store, tenant, diverged, 64); err != nil || ok {
		t.Fatalf("divergent history must not resolve: ok=%v err=%v", ok, err)
	}

	userOnly := []oaiMsg{{Role: "user", Content: "hello"}, {Role: "user", Content: "continue"}}
	if _, _, ok, err := resolveHistoryBinding(ctx, store, tenant, userOnly, 64); err != nil || ok {
		t.Fatalf("assistant-free prefix must not resolve: ok=%v err=%v", ok, err)
	}
}

func TestRendezvousSelectionIsStableAndSkipsUnavailableAccounts(t *testing.T) {
	accounts := []auth.AccountToken{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	available := func(id string) bool { return id != "b" }
	first, ok := selectRendezvousAccount("affinity", accounts, available)
	if !ok || first.ID == "b" {
		t.Fatalf("unexpected selection: %+v ok=%v", first, ok)
	}
	for i := 0; i < 20; i++ {
		next, ok := selectRendezvousAccount("affinity", accounts, available)
		if !ok || next.ID != first.ID {
			t.Fatalf("selection changed: first=%q next=%q", first.ID, next.ID)
		}
	}
	available = func(id string) bool { return id != "b" && id != first.ID }
	next, ok := selectRendezvousAccount("affinity", accounts, available)
	if !ok || next.ID == first.ID || next.ID == "b" {
		t.Fatalf("unavailable account was selected: %+v", next)
	}
}

func TestBindingMigrationUsesGenerationCAS(t *testing.T) {
	store := newMemoryAffinityStore(time.Hour, 100)
	ctx := context.Background()
	binding := affinityBinding{ID: "binding-1", TenantHash: "tenant", AccountID: "a", ConversationID: "c1", SessionID: "s1", HistoryDigest: "h1", HistoryCount: 2, Generation: 1}
	if err := store.PutBinding(ctx, binding, time.Hour); err != nil {
		t.Fatal(err)
	}
	migrated := binding
	migrated.AccountID = "b"
	migrated.ConversationID = "c2"
	migrated.SessionID = "s2"
	migrated.HistoryDigest = "h2"
	migrated.Generation = 2
	if ok, err := store.CompareAndSwapBinding(ctx, binding.ID, 1, migrated, time.Hour); err != nil || !ok {
		t.Fatalf("first CAS failed: ok=%v err=%v", ok, err)
	}
	stale := migrated
	stale.AccountID = "c"
	if ok, err := store.CompareAndSwapBinding(ctx, binding.ID, 1, stale, time.Hour); err != nil || ok {
		t.Fatalf("stale CAS must lose: ok=%v err=%v", ok, err)
	}
}

func TestConfirmedReuseUsageFields(t *testing.T) {
	u := reuseUsage{PromptTokens: 120, CompletionTokens: 30, CachedTokens: 80, Confirmed: true}
	chat := chatUsage(u)
	details := chat["prompt_tokens_details"].(map[string]any)
	if details["cached_tokens"] != int64(80) || chat["total_tokens"] != int64(150) {
		t.Fatalf("unexpected chat usage: %#v", chat)
	}
	responses := responsesUsage(u)
	inputDetails := responses["input_tokens_details"].(map[string]any)
	if inputDetails["cached_tokens"] != int64(80) {
		t.Fatalf("unexpected responses usage: %#v", responses)
	}
	if got := anthropicUsage(u)["cache_read_input_tokens"]; got != int64(80) {
		t.Fatalf("unexpected anthropic cached tokens: %#v", got)
	}

	u.Confirmed = false
	if got := chatUsage(u)["prompt_tokens_details"].(map[string]any)["cached_tokens"]; got != int64(0) {
		t.Fatalf("unconfirmed reuse reported cached tokens: %#v", got)
	}
}

func TestMultimodalHistoryDigestIncludesImageContent(t *testing.T) {
	first := []oaiMsg{{Role: "user", Content: []any{
		map[string]any{"type": "text", "text": "inspect"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/a.png"}},
	}}}
	second := []oaiMsg{{Role: "user", Content: []any{
		map[string]any{"type": "text", "text": "inspect"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/b.png"}},
	}}}
	if historyDigest(first) == historyDigest(second) {
		t.Fatal("different images produced the same history digest")
	}
}

func TestToolCallHistoryDigestIgnoresProtocolRoundTripNoise(t *testing.T) {
	stored := []oaiMsg{{
		Role: "assistant", Content: nil,
		ToolCalls: []map[string]any{{
			"id": "call_gateway", "type": "function",
			"function": map[string]any{"name": "RunCommand", "arguments": `{"cwd":"C:\\work","blocking":true}`},
		}},
	}}
	roundTripped := []oaiMsg{{
		Role: "assistant", Content: "",
		ToolCalls: []map[string]any{{
			"id": "call_client", "index": float64(0), "type": "function",
			"function": map[string]any{"name": "RunCommand", "arguments": `{"blocking":true,"cwd":"C:\\work"}`},
		}},
	}}
	if historyDigest(stored) != historyDigest(roundTripped) {
		t.Fatal("protocol-equivalent tool calls produced different history digests")
	}
	if !messagesEqual(stored[0], roundTripped[0]) {
		t.Fatal("session resolver rejected a protocol-equivalent tool call")
	}
}
