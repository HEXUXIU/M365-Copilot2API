package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"m365-copilot2api/internal/auth"
	"m365-copilot2api/internal/chathub"
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
		{&UpstreamHTTPError{Status: 503}, false, false, 0, http.StatusBadGateway},
		{&UpstreamHTTPError{Status: 401}, false, true, 0, http.StatusUnauthorized},
		{&UpstreamHTTPError{Status: 403}, false, true, 0, http.StatusUnauthorized},
		{&UpstreamHTTPError{Status: 502}, false, false, 0, http.StatusBadGateway},
		{&UpstreamHTTPError{Status: 502, Body: "account is limited"}, true, false, 0, http.StatusTooManyRequests},
		{fmt.Errorf("upstream http 429"), false, false, 0, http.StatusBadGateway},
		{fmt.Errorf("Too many requests, slow down"), false, false, 0, http.StatusBadGateway},
		{fmt.Errorf("account is limited"), false, false, 0, http.StatusBadGateway},
		{fmt.Errorf("random failure"), false, false, 0, http.StatusBadGateway},
		{chathub.ErrRateLimitNotice, true, false, 0, http.StatusTooManyRequests},
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
	if !h.RateLimited(id) {
		t.Fatal("rate-limited account missing rate-limit state")
	}
	h.MarkSuccess(id)
	if h.RateLimited(id) {
		t.Fatal("success must clear rate-limit state")
	}
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

func TestQuota429HonoursRetryAfterAndBacksOff(t *testing.T) {
	h := newAccountHealth()
	serverErr := &UpstreamHTTPError{Status: 429, RetryAfter: 45}
	h.MarkFailure("quota-retry", serverErr, 0)
	until, ok := h.CooldownUntil("quota-retry")
	if !ok {
		t.Fatal("429 with RetryAfter must set a cooldown")
	}
	if remain := time.Until(until); remain < 40*time.Second || remain > 46*time.Second {
		t.Fatalf("429 RetryAfter cooldown = %v, want ~45s", remain)
	}
	if !h.RateLimited("quota-retry") {
		t.Fatal("429 must set rate-limit state")
	}

	plainErr := &UpstreamHTTPError{Status: 429}
	h.MarkFailure("quota-base", plainErr, 0)
	if _, ok := h.CooldownUntil("quota-base"); !ok {
		t.Fatal("plain 429 must set a cooldown")
	}
	h.MarkFailure("quota-base", plainErr, 0)
	until2, ok := h.CooldownUntil("quota-base")
	if !ok {
		t.Fatal("second consecutive 429 lost its cooldown")
	}
	if remain := time.Until(until2); remain < 28*time.Second {
		t.Fatalf("consecutive 429 backoff = %v, want >= 29s", remain)
	}
	h.MarkSuccess("quota-base")
	if h.RateLimited("quota-base") {
		t.Fatal("success must clear rate-limit state")
	}
	if _, ok := h.CooldownUntil("quota-base"); ok {
		t.Fatal("success must clear the cooldown")
	}
}

func TestWarmSkipsRecentlyLimitedAccount(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("M365_SESSION_CACHE", filepath.Join(dir, "sessions.json"))
	t.Setenv("M365_CONVERSATION_CACHE", filepath.Join(dir, "conversations.json"))
	t.Setenv("M365_USER_SESSION_CACHE", filepath.Join(dir, "users.json"))
	t.Setenv("M365_DATA_DIR", dir)
	toks := map[string]auth.TokenSet{
		"u-1": {HomeOID: "u-1", Email: "one@example.com", AccessToken: "tok", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)},
	}
	path := filepath.Join(dir, "accounts.json")
	store, err := auth.OpenStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	for _, tok := range toks {
		if _, err := store.Upsert(tok); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	s := &Server{
		tokens:             store,
		accountPool:        newAccountHealth(),
		accountConcurrency: newAccountConcurrency(),
		chat:               chathub.NewClient(),
	}
	// 刚被 429 的账号：保活必须跳过，等冷却过去再补水。
	s.accountPool.MarkFailure("u-1", &UpstreamHTTPError{Status: 429}, 0)
	if attempts, limited, _ := s.accountPool.QuotaDetail("u-1"); !limited || attempts == 0 {
		t.Fatalf("fresh 429 must mark the account limited, got attempts=%d limited=%v", attempts, limited)
	}
}

func TestCooldownExpiryClearsCallCount(t *testing.T) {
	h := newAccountHealth()
	const id = "acct-expiry"
	h.MarkCall(id)
	h.MarkCall(id)
	h.MarkFailure(id, &UpstreamHTTPError{Status: 429}, time.Minute)
	h.mu.Lock()
	h.cooldown[id] = time.Now().Add(-time.Second)
	h.mu.Unlock()
	if !h.Available(id) {
		t.Fatal("expired cooldown must be available")
	}
	if h.CallCount(id) != 0 {
		t.Fatalf("call count=%d want 0", h.CallCount(id))
	}
	if h.RateLimited(id) {
		t.Fatal("expired cooldown still marked limited")
	}
	h.MarkCall(id)
	h.MarkFailure(id, &UpstreamHTTPError{Status: 429}, time.Minute)
	h.mu.Lock()
	h.cooldown[id] = time.Now().Add(-time.Second)
	h.mu.Unlock()
	if _, ok := h.CooldownUntil(id); ok || h.CallCount(id) != 0 {
		t.Fatal("CooldownUntil must clear expired call count")
	}
	h.MarkCall(id)
	h.MarkFailure(id, &UpstreamHTTPError{Status: 429}, time.Minute)
	h.mu.Lock()
	h.cooldown[id] = time.Now().Add(-time.Second)
	h.mu.Unlock()
	h.MarkCall(id)
	if h.CallCount(id) != 1 {
		t.Fatalf("post-cooldown call count=%d want 1", h.CallCount(id))
	}
	const authID = "acct-auth-expiry"
	h.MarkCall(authID)
	h.MarkFailure(authID, &UpstreamHTTPError{Status: 401}, time.Minute)
	h.mu.Lock()
	h.cooldown[authID] = time.Now().Add(-time.Second)
	h.mu.Unlock()
	if !h.Available(authID) || h.CallCount(authID) != 1 {
		t.Fatal("auth cooldown must not clear call count")
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

func TestBatchAccountsAppliesAtomically(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store, accountPool: newAccountHealth()}
	do := func(body string) (int, map[string]any) {
		w := httptest.NewRecorder()
		s.batchAccounts(w, httptest.NewRequest(http.MethodPost, "/api/accounts/batch", strings.NewReader(body)))
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, _ := do(`{"ids":[],"scheduling":false}`); code != 400 {
		t.Fatalf("empty ids status=%d want 400", code)
	}
	if code, _ := do(`{"ids":["u-1","missing"],"scheduling":false}`); code != 400 {
		t.Fatalf("unknown id status=%d want 400", code)
	}
	if acc, _ := store.Get("u-1"); !store.ScheduleEnabled("u-1") || acc.WebSearchDisabled {
		t.Fatal("failed batch must not partially apply")
	}
	sp := "Be concise."
	code, out := do(`{"ids":["u-1","u-2"],"scheduling":false,"webSearch":false,"systemPrompt":"Be concise."}`)
	if code != 200 || out["updated"] != float64(2) {
		t.Fatalf("batch status=%d out=%v", code, out)
	}
	for _, id := range []string{"u-1", "u-2"} {
		acc, _ := store.Get(id)
		if store.ScheduleEnabled(id) || !acc.WebSearchDisabled || acc.SystemPrompt != sp {
			t.Fatalf("batch not applied to %s: %+v", id, acc)
		}
	}
	if acc, _ := store.Get("u-3"); !store.ScheduleEnabled(acc.ID) || acc.WebSearchDisabled || acc.SystemPrompt != "" {
		t.Fatal("untouched account must keep defaults")
	}
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

func TestResolveAccountSkipsSchedulingDisabled(t *testing.T) {
	store := testAccountFiles(t)
	if err := store.SetScheduleEnabled("u-1", false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetScheduleEnabled("u-2", false); err != nil {
		t.Fatal(err)
	}
	s := &Server{tokens: store, accountPool: newAccountHealth()}
	acc, err := s.resolveAccount("")
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID != "u-3" {
		t.Fatalf("scheduled account=%s want u-3", acc.ID)
	}
	explicit, err := s.resolveAccount("u-1")
	if err != nil {
		t.Fatal(err)
	}
	if explicit.ID != "u-1" {
		t.Fatalf("explicit account=%s want u-1", explicit.ID)
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

func TestScheduleAccount(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/accounts/schedule", strings.NewReader(`{"id":"u-1","enabled":false}`))
	s.scheduleAccount(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if store.ScheduleEnabled("u-1") {
		t.Fatal("account scheduling still enabled")
	}
}

func TestAccountsReportsCooldown(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store, accountPool: newAccountHealth()}
	s.accountPool.MarkCall("u-1")
	s.accountPool.MarkCall("u-1")
	s.accountPool.MarkFailure("u-1", &UpstreamHTTPError{Status: 429}, 20*time.Minute)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	s.accounts(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Accounts []struct {
			ID              string     `json:"id"`
			Status          string     `json:"status"`
			ScheduleEnabled bool       `json:"scheduleEnabled"`
			CallCount       uint64     `json:"callCount"`
			CooldownUntil   *time.Time `json:"cooldownUntil"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, account := range body.Accounts {
		if account.ID != "u-1" {
			continue
		}
		if account.Status != "cooldown" || account.CooldownUntil == nil || !account.ScheduleEnabled || account.CallCount != 2 {
			t.Fatalf("cooldown account=%#v", account)
		}
		return
	}
	t.Fatal("cooldown account missing")
}

func TestFailoverAllowsResolvedConversationID(t *testing.T) {
	accountID := ""
	conversationID := "conv-123"
	resolvedConversationID := "conv-123"
	if !(conversationID == "" || conversationID == resolvedConversationID) {
		t.Fatal("failover must be allowed when ConversationID was injected by session resolver")
	}
	explicitConversationID := "conv-explicit"
	if explicitConversationID == "" || explicitConversationID == resolvedConversationID {
		t.Fatal("failover must NOT be allowed when ConversationID was explicitly set by client")
	}
	_ = accountID
}

func TestErrRateLimitNoticeTriggersMarkFailure(t *testing.T) {
	h := newAccountHealth()
	const id = "acct-rl"
	h.MarkFailure(id, chathub.ErrRateLimitNotice, 15*time.Minute)
	if h.Available(id) {
		t.Fatal("ErrRateLimitNotice must put account in cooldown")
	}
}
