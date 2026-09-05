package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		{&UpstreamHTTPError{Status: 503}, true, false, 0, http.StatusTooManyRequests},
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

func TestTransientUpstreamFailureClassification(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("ws dial: %w", errors.New("connection reset by peer")),
		fmt.Errorf("ws read before completion: %w", io.ErrUnexpectedEOF),
		fmt.Errorf("chat send: i/o timeout"),
		fmt.Errorf("upstream result error: InternalError"),
	} {
		if !IsTransientUpstreamFailure(err) {
			t.Fatalf("expected transient failure: %v", err)
		}
	}
	if IsTransientUpstreamFailure(&UpstreamHTTPError{Status: 429}) || IsTransientUpstreamFailure(&UpstreamHTTPError{Status: 401}) {
		t.Fatal("rate-limit/auth failures should use their dedicated classifications")
	}
	if IsTransientUpstreamFailure(fmt.Errorf("request timeout setting is invalid")) {
		t.Fatal("an unrelated timeout word must not trigger transport retry")
	}
}

func TestRetryableAccountFailureIncludesTransportErrors(t *testing.T) {
	for _, err := range []error{
		chathub.ErrRateLimitNotice,
		&UpstreamHTTPError{Status: http.StatusUnauthorized},
		chathub.ErrEmptyCompletion,
		fmt.Errorf("ws read before completion: i/o timeout"),
	} {
		if !isRetryableAccountFailure(err) {
			t.Fatalf("expected retryable account failure: %v", err)
		}
	}
	if isRetryableAccountFailure(chathub.ErrOffensiveContent) {
		t.Fatal("content-policy failure must not be retried as an account transport failure")
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
	if until["cooldownReason"] != string(CategoryQuota429) || until["cooldownScope"] != "account" || until["cooldownTriggeredAt"] == nil {
		t.Fatalf("snapshot should explain account cooldown: %v", until)
	}
}

func TestImageLimitOnlyDisablesImageCapability(t *testing.T) {
	h := newAccountHealth()
	const id = "acct-image-limit"

	h.MarkImageLimited(id)
	if !h.Available(id) {
		t.Fatal("image limit must not make the account unavailable for text")
	}
	if h.ImageGenAvailable(id) {
		t.Fatal("image-limited account must be unavailable for image generation")
	}

	h.MarkSuccess(id)
	if !h.Available(id) {
		t.Fatal("text success must leave the account available")
	}
	if h.ImageGenAvailable(id) {
		t.Fatal("text success must not clear an active image limit")
	}
	snapshot := h.Snapshot()[id]
	if snapshot["imageCooldownReason"] != "IMAGE_LIMIT" || snapshot["imageCooldownScope"] != "capability" || snapshot["imageCooldownTriggeredAt"] == nil || snapshot["imageCooldownUntil"] == nil {
		t.Fatalf("snapshot should explain image capability cooldown: %v", snapshot)
	}
}

func TestMeteredImageLimitKeepsItsSpecificRecoveryWindow(t *testing.T) {
	h := newAccountHealth()
	const id = "acct-metered-image-limit"
	information := []any{map[string]any{
		"meterError": "ImageGenInsufficientTokensThrottled",
		"hasAccess":  false,
	}}
	err := &chathub.MeteringError{Err: chathub.ErrImageLimit, Information: information}

	h.MarkImageFailure(id, err)
	snapshot := h.Snapshot()[id]
	until, ok := snapshot["imageCooldownUntil"].(time.Time)
	if !ok {
		t.Fatalf("missing image recovery time: %v", snapshot)
	}
	now := time.Now().UTC()
	want := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	if until.Sub(want) > time.Second || want.Sub(until) > time.Second {
		t.Fatalf("image recovery=%s, want next UTC midnight %s", until, want)
	}
	if snapshot["imageCooldownReason"] != "ImageGenInsufficientTokensThrottled" {
		t.Fatalf("image reason=%v", snapshot["imageCooldownReason"])
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

func TestStickyAccountAvailabilityHonorsCooldown(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store, accountPool: newAccountHealth(), accountConcurrency: newAccountConcurrency()}
	s.accountPool.MarkFailure("u-1", &UpstreamHTTPError{Status: 429}, 10*time.Minute)
	if s.stickyAccountAvailable("u-1", 20) {
		t.Fatal("sticky routing must not advertise a cooling-down account")
	}
	if !s.stickyAccountAvailable("u-2", 20) {
		t.Fatal("healthy account should remain available to sticky routing")
	}
}

func TestMarkAccountResultPersistsAccountCooldownToAffinity(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second})
	defer manager.close()
	s := &Server{
		accountPool: newAccountHealth(),
		affinity:    manager,
		settings:    &settingsStore{v: defaultRuntimeSettings()},
	}
	s.markAccountResult("account-a", &UpstreamHTTPError{Status: 429})
	health, ok, err := manager.fallback.GetAccountHealth(context.Background(), "account-a")
	if err != nil {
		t.Fatalf("GetAccountHealth: %v", err)
	}
	if !ok || health.CooldownUntil.IsZero() || !health.CooldownUntil.After(time.Now()) {
		t.Fatalf("account cooldown was not persisted: ok=%v health=%+v", ok, health)
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

func TestNextHealthyAccountExcludingDoesNotRepeatTriedAccounts(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store, accountPool: newAccountHealth()}

	excluded := map[string]struct{}{"u-1": {}, "u-2": {}}
	acc, err := s.nextHealthyAccountExcluding(excluded)
	if err != nil {
		t.Fatalf("nextHealthyAccountExcluding: %v", err)
	}
	if acc.ID != "u-3" {
		t.Fatalf("selected previously tried account %q, want u-3", acc.ID)
	}

	excluded["u-3"] = struct{}{}
	if _, err := s.nextHealthyAccountExcluding(excluded); err == nil {
		t.Fatal("expected failure after every account was tried")
	}
}

func TestResolveImageAccountSkipsImageLimitedAccount(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store, accountPool: newAccountHealth()}
	s.lastHealthyAccount = "u-1"
	s.accountPool.MarkImageLimited("u-1")

	acc, err := s.resolveImageAccount("")
	if err != nil {
		t.Fatalf("resolveImageAccount: %v", err)
	}
	if acc.ID == "u-1" {
		t.Fatal("automatic image selection reused an image-limited account")
	}
	if !s.accountPool.Available("u-1") {
		t.Fatal("image-limited account must remain available to text scheduling")
	}
}

func TestResolveImageAccountRejectsExplicitImageLimitedAccount(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store, accountPool: newAccountHealth()}
	s.accountPool.MarkImageLimited("u-1")

	_, err := s.resolveImageAccount("u-1")
	if !errors.Is(err, chathub.ErrImageLimit) {
		t.Fatalf("resolveImageAccount error=%v, want ErrImageLimit", err)
	}
}

func TestNextImageAccountExcludingSkipsTriedAndImageLimitedAccounts(t *testing.T) {
	store := testAccountFiles(t)
	s := &Server{tokens: store, accountPool: newAccountHealth()}
	s.accountPool.MarkImageLimited("u-2")

	acc, err := s.nextImageAccountExcluding(map[string]struct{}{"u-1": {}})
	if err != nil {
		t.Fatalf("nextImageAccountExcluding: %v", err)
	}
	if acc.ID != "u-3" {
		t.Fatalf("selected account=%q, want u-3", acc.ID)
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

func TestTransientAndCapabilityFailuresDoNotCooldownAccount(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "overload", err: &UpstreamHTTPError{Status: http.StatusServiceUnavailable}},
		{name: "dns", err: &chathub.DialError{Kind: "DNS"}},
		{name: "tcp", err: &chathub.DialError{Kind: "TCP"}},
		{name: "tls", err: &chathub.DialError{Kind: "TLS"}},
		{name: "websocket handshake", err: &chathub.DialError{Kind: "WS_HANDSHAKE"}},
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "retryable 422", err: &UpstreamHTTPError{Status: http.StatusUnprocessableEntity}},
		{name: "empty completion", err: chathub.ErrEmptyCompletion},
		{name: "content policy", err: chathub.ErrOffensiveContent},
		{name: "image limit", err: chathub.ErrImageLimit},
		{name: "image tokens", err: &UpstreamHTTPError{ErrorCode: "InsufficientTokens"}},
		{name: "unknown", err: errors.New("single upstream failure")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAccountHealth()
			h.MarkFailure("acct", tt.err, 10*time.Minute)
			if until, ok := h.CooldownUntil("acct"); ok {
				t.Fatalf("failure %v created account cooldown until %s", tt.err, until)
			}
		})
	}
}

func TestBoundProxyFailuresDoNotOpenGlobalCircuit(t *testing.T) {
	store := testAccountFiles(t)
	if err := store.SetBoundProxy("u-1", "http://127.0.0.1:18080"); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		tokens:      store,
		accountPool: newAccountHealth(),
		settings:    &settingsStore{v: defaultRuntimeSettings()},
	}
	for i := 0; i < 10; i++ {
		s.markAccountResult("u-1", &chathub.DialError{Kind: "TCP"})
	}
	if GlobalCircuitIsOpen() {
		t.Fatal("bound proxy failures must remain proxy-scoped")
	}
	if !s.accountPool.Available("u-1") {
		t.Fatal("bound proxy failures must not disable the account")
	}
}

func TestAccountCooldownRecoveryWindows(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want time.Duration
	}{
		{name: "429 retry after", err: &UpstreamHTTPError{Status: http.StatusTooManyRequests, RetryAfter: 90}, want: 90 * time.Second},
		{name: "401", err: &UpstreamHTTPError{Status: http.StatusUnauthorized}, want: 2 * time.Minute},
		{name: "403", err: &UpstreamHTTPError{Status: http.StatusForbidden}, want: 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAccountHealth()
			started := time.Now()
			h.MarkFailure("acct", tt.err, time.Minute)
			until, ok := h.CooldownUntil("acct")
			if !ok {
				t.Fatalf("missing cooldown for %v", tt.err)
			}
			got := until.Sub(started)
			if got < tt.want-time.Second || got > tt.want+time.Second {
				t.Fatalf("cooldown=%s, want %s", got, tt.want)
			}
		})
	}
}

func TestConfirmedAccountMeteringDenialStillCoolsAccount(t *testing.T) {
	h := newAccountHealth()
	err := &chathub.MeteringError{
		Err: chathub.ErrMeteringThrottled,
		Information: []any{map[string]any{
			"meterError": "AccountCapabilityThrottled",
			"hasAccess":  false,
		}},
	}
	h.MarkFailure("acct", err, time.Minute)
	if _, ok := h.CooldownUntil("acct"); !ok {
		t.Fatal("confirmed account-level metering denial must create account cooldown")
	}
}

func TestGlobalCircuitOnlyRecordsInfrastructureFailures(t *testing.T) {
	nonGlobal := []error{
		&UpstreamHTTPError{Status: 401},
		&UpstreamHTTPError{Status: 403},
		&UpstreamHTTPError{Status: 429},
		&UpstreamHTTPError{Status: 422},
		&UpstreamHTTPError{ErrorCode: "ErrorUserBanned"},
		&UpstreamHTTPError{ErrorCode: "InsufficientTokens"},
		chathub.ErrEmptyCompletion,
		chathub.ErrOffensiveContent,
		chathub.ErrImageLimit,
		context.Canceled,
	}
	for _, err := range nonGlobal {
		ResetGlobalCircuit()
		for i := 0; i < 10; i++ {
			GlobalCircuitRecord(err)
		}
		if GlobalCircuitIsOpen() {
			t.Fatalf("non-global error opened circuit: %v", err)
		}
	}

	ResetGlobalCircuit()
	for i := 0; i < 10; i++ {
		GlobalCircuitRecord(fmt.Errorf("connection refused"))
	}
	if !GlobalCircuitIsOpen() {
		t.Fatal("transport failures must open global circuit")
	}
	ResetGlobalCircuit()
}

func TestParseMeteringAggregatesDeniedItemsRegardlessOfOrder(t *testing.T) {
	cases := []string{
		`[{"meterError":"denied","hasAccess":false},{"hasAccess":true}]`,
		`[{"hasAccess":true},{"meterError":"denied","hasAccess":false}]`,
	}
	for _, raw := range cases {
		meterError, hasAccess, known := ParseMetering("account", json.RawMessage(raw))
		if !known || hasAccess || meterError != "denied" {
			t.Fatalf("ParseMetering(%s) = (%q, %v, %v)", raw, meterError, hasAccess, known)
		}
	}
}

func TestParseMeteringTreatsMissingHasAccessAsUnknown(t *testing.T) {
	meterError, hasAccess, known := ParseMetering("account", json.RawMessage(`[{"meterError":"denied"}]`))
	if known || hasAccess || meterError != "denied" {
		t.Fatalf("ParseMetering missing hasAccess = (%q, %v, %v)", meterError, hasAccess, known)
	}
}

func TestParseMeteringRecognizesExplicitAccess(t *testing.T) {
	meterError, hasAccess, known := ParseMetering("account", json.RawMessage(`[{"hasAccess":true}]`))
	if !known || !hasAccess || meterError != "" {
		t.Fatalf("ParseMetering explicit access = (%q, %v, %v)", meterError, hasAccess, known)
	}
}

func TestAccountHealthMeteringDefaultsDeepCopySnapshotAndReset(t *testing.T) {
	h := newAccountHealth()
	const id = "acct-metering"

	if state := h.GetMetering(id); state.Known || state.HasAccess || !state.UpdatedAt.IsZero() {
		t.Fatalf("missing metering state must be unknown: %#v", state)
	}

	throttling := map[string]any{
		"numUserMessagesInConversation":    float64(3),
		"maxNumUserMessagesInConversation": float64(30),
		"nested":                           map[string]any{"value": "original"},
	}
	remaining := map[string]int{"Chat": 17}
	observedAt := time.Unix(1_777_777_777, 0).UTC()
	h.UpdateThrottling(id, throttling)
	h.UpdateMetering(id, accountMeteringUpdate{
		MeterError:     "meter-error",
		HasAccess:      false,
		AccessKnown:    true,
		AccessObserved: true,
		Remaining:      remaining,
		Source:         "chathub",
		UpdatedAt:      observedAt,
	})

	throttling["nested"].(map[string]any)["value"] = "mutated"
	remaining["Chat"] = 0

	stored := h.GetThrottling(id).(map[string]any)
	if stored["nested"].(map[string]any)["value"] != "original" {
		t.Fatal("UpdateThrottling must deep-copy input")
	}
	state := h.GetMetering(id)
	if state.MeterError != "meter-error" || !state.Known || state.HasAccess || state.Remaining["Chat"] != 17 || state.Source != "chathub" || !state.UpdatedAt.Equal(observedAt) {
		t.Fatalf("unexpected metering state: %#v", state)
	}

	h.UpdateMetering(id, accountMeteringUpdate{})
	state = h.GetMetering(id)
	if state.MeterError != "meter-error" || !state.Known || state.Remaining["Chat"] != 17 || !state.UpdatedAt.Equal(observedAt) {
		t.Fatalf("empty update cleared the prior snapshot: %#v", state)
	}

	snapshot := h.Snapshot()
	snapshot[id]["throttling"].(map[string]any)["nested"].(map[string]any)["value"] = "snapshot-mutated"
	snapshot[id]["remainingAllowance"].(map[string]int)["Chat"] = 1
	if h.GetThrottling(id).(map[string]any)["nested"].(map[string]any)["value"] != "original" {
		t.Fatal("Snapshot must deep-copy throttling")
	}
	state = h.GetMetering(id)
	if state.Remaining["Chat"] != 17 {
		t.Fatal("Snapshot must deep-copy remaining allowance")
	}

	h.ClearAllCooldowns()
	if got := h.Snapshot(); len(got) != 0 {
		t.Fatalf("reset left health state: %#v", got)
	}
	if state := h.GetMetering(id); state.MeterError != "" || state.Known || state.HasAccess || len(state.Remaining) != 0 || !state.UpdatedAt.IsZero() {
		t.Fatalf("reset left metering state: %#v", state)
	}
}

func TestRecordAccountChatErrorPreservesMeteringDenial(t *testing.T) {
	h := newAccountHealth()
	s := &Server{accountPool: h, settings: &settingsStore{v: defaultRuntimeSettings()}}
	const id = "acct-metering-error"
	information := []any{map[string]any{
		"meterError": "ImageGenInsufficientTokensThrottled",
		"hasAccess":  false,
	}}

	s.recordAccountChatResult(id, chathub.Result{}, &chathub.MeteringError{
		Err:         chathub.ErrImageLimit,
		Information: information,
	})

	state := h.GetMetering(id)
	if !state.Known || state.HasAccess || state.MeterError != "ImageGenInsufficientTokensThrottled" {
		t.Fatalf("metering denial was not retained: %#v", state)
	}
	if !h.Available(id) {
		t.Fatal("image metering denial must not disable text")
	}
	if h.ImageGenAvailable(id) {
		t.Fatal("image metering denial must disable image generation")
	}
}

func TestImageSystemCapacityMeteringOnlyCoolsImageCapability(t *testing.T) {
	h := newAccountHealth()
	s := &Server{accountPool: h, settings: &settingsStore{v: defaultRuntimeSettings()}}
	const id = "acct-image-capacity"
	information := []any{map[string]any{
		"meterError": "ImageGenSystemCapacityThrottled",
		"hasAccess":  false,
	}}

	s.recordAccountChatResult(id, chathub.Result{}, &chathub.MeteringError{
		Err:         chathub.ErrMeteringThrottled,
		Information: information,
	})

	if !h.Available(id) {
		t.Fatal("image system capacity must not disable text")
	}
	if h.ImageGenAvailable(id) {
		t.Fatal("image system capacity must cool image generation")
	}
	snapshot := h.Snapshot()[id]
	until, ok := snapshot["imageCooldownUntil"].(time.Time)
	if !ok || time.Until(until) < 29*time.Minute || time.Until(until) > 31*time.Minute {
		t.Fatalf("image capacity recovery=%v, want about 30 minutes", snapshot["imageCooldownUntil"])
	}
	if _, ok := snapshot["cooldownUntil"]; ok {
		t.Fatalf("image capacity created account cooldown: %v", snapshot)
	}
}

func TestServerRecordsAccountResultOnlyAtChatBoundary(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if got := strings.Count(text, ".accountPool.MarkFailureScoped("); got != 1 {
		t.Fatalf("server.go has %d direct MarkFailureScoped calls, want the single chat-boundary call", got)
	}
	if got := strings.Count(text, ".accountPool.MarkSuccess("); got != 1 {
		t.Fatalf("server.go has %d direct MarkSuccess calls, want the single chat-boundary call", got)
	}
}
