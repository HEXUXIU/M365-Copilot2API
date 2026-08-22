package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"m365-copilot2api/internal/auth"
)

type affinityMode string

const (
	affinityOff     affinityMode = "off"
	affinityObserve affinityMode = "observe"
	affinityEnforce affinityMode = "enforce"
)

type affinityConfig struct {
	Mode                    affinityMode
	TTL                     time.Duration
	MaxSessions             int
	LockTTL                 time.Duration
	LockWait                time.Duration
	StickyRetryAfter        time.Duration
	AnonymousScope          string
	ReuseRouterConversation bool
}

func loadAffinityConfig() affinityConfig {
	mode := affinityMode(strings.ToLower(strings.TrimSpace(os.Getenv("M365_AFFINITY_MODE"))))
	if mode != affinityObserve && mode != affinityEnforce {
		mode = affinityOff
	}
	anonymousScope := strings.ToLower(strings.TrimSpace(os.Getenv("M365_AFFINITY_ANONYMOUS_SCOPE")))
	if anonymousScope != "global" && anonymousScope != "ip" {
		anonymousScope = "ip"
	}
	return affinityConfig{
		Mode:                    mode,
		TTL:                     time.Duration(affinityEnvInt("M365_AFFINITY_TTL_MINUTES", 120)) * time.Minute,
		MaxSessions:             affinityEnvInt("M365_AFFINITY_MAX_SESSIONS", 10000),
		LockTTL:                 time.Duration(affinityEnvInt("M365_SESSION_LOCK_TTL_SECONDS", 180)) * time.Second,
		LockWait:                time.Duration(affinityEnvInt("M365_SESSION_LOCK_WAIT_SECONDS", 120)) * time.Second,
		StickyRetryAfter:        time.Duration(affinityEnvInt("M365_STICKY_RETRY_AFTER_SECONDS", 5)) * time.Second,
		AnonymousScope:          anonymousScope,
		ReuseRouterConversation: affinityEnvBool("M365_AFFINITY_REUSE_ROUTER_CONVERSATION", false),
	}
}

func affinityEnvInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err == nil && value > 0 {
		return value
	}
	return fallback
}

func affinityEnvBool(name string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if value == "true" || value == "1" || value == "yes" || value == "on" {
		return true
	}
	if value == "false" || value == "0" || value == "no" || value == "off" {
		return false
	}
	return fallback
}

type affinityManager struct {
	config      affinityConfig
	primary     affinityStore
	primaryName string
	fallback    affinityStore

	mu              sync.Mutex
	degraded        bool
	lastHealthCheck time.Time
	lastError       string
}

func openAffinityManager(config affinityConfig) *affinityManager {
	return &affinityManager{
		config: config, fallback: newMemoryAffinityStore(config.TTL, config.MaxSessions),
	}
}

func (m *affinityManager) close() {
	if m == nil {
		return
	}
	if m.primary != nil {
		_ = m.primary.Close()
	}
	_ = m.fallback.Close()
}

func (m *affinityManager) store(ctx context.Context) affinityStore {
	if m == nil {
		return nil
	}
	if m.primary == nil {
		return m.fallback
	}
	m.mu.Lock()
	degraded := m.degraded
	lastCheck := m.lastHealthCheck
	m.mu.Unlock()
	if !degraded || time.Since(lastCheck) < 10*time.Second {
		if degraded {
			return m.fallback
		}
		return m.primary
	}
	m.mu.Lock()
	m.lastHealthCheck = time.Now()
	m.mu.Unlock()
	if m.primary.Healthy(ctx) {
		m.mu.Lock()
		m.degraded = false
		m.lastError = ""
		m.mu.Unlock()
		return m.primary
	}
	return m.fallback
}

func (m *affinityManager) markStoreError(err error) affinityStore {
	if m == nil {
		return nil
	}
	// Caller cancellation is not evidence that a shared primary store failed.
	// Degrading here sends unrelated requests to an empty local store.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		if m.primary != nil {
			return m.primary
		}
		return m.fallback
	}
	if err != nil {
		m.mu.Lock()
		m.degraded = true
		m.lastHealthCheck = time.Now()
		m.lastError = err.Error()
		m.mu.Unlock()
		log.Printf("[affinity] primary store degraded: %v", err)
	}
	return m.fallback
}

func (m *affinityManager) status() map[string]any {
	if m == nil {
		return map[string]any{"mode": "off", "store": "disabled"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.config.Mode == affinityOff {
		return map[string]any{"mode": "off", "store": "disabled", "degraded": false}
	}
	store := "memory"
	if m.primary != nil {
		store = m.primaryName
		if store == "" {
			store = "external"
		}
		if m.degraded {
			store = "memory_fallback"
		}
	}
	return map[string]any{"mode": string(m.config.Mode), "store": store, "degraded": m.degraded, "last_error": m.lastError}
}

type affinityRequest struct {
	manager         *affinityManager
	store           affinityStore
	key             affinityKey
	binding         affinityBinding
	hasBinding      bool
	prefixCount     int
	accountID       string
	proposedAccount string
	migrationReason string
	enforced        bool
	incremental     bool
	releaseLock     func()
	closed          bool
}

func (m *affinityManager) begin(ctx context.Context, tenant string, body *oaiReq, r *http.Request, accounts []auth.AccountToken, available func(string) bool) (*affinityRequest, error) {
	state := &affinityRequest{manager: m}
	if m == nil || m.config.Mode == affinityOff {
		return state, nil
	}
	state.enforced = m.config.Mode == affinityEnforce
	state.key = deriveAffinityKey(tenant, body, r)
	state.store = m.store(ctx)

	resolve := func(store affinityStore) error {
		if state.key.BindingID != "" {
			binding, ok, err := store.GetBinding(ctx, state.key.BindingID)
			if err != nil {
				return err
			}
			if ok && binding.TenantHash == state.key.TenantHash {
				state.binding, state.hasBinding = binding, true
				state.prefixCount = contextPrefixCountForBinding(binding, body.Messages)
			}
		}
		if !state.hasBinding && state.key.Reason == "previous_response" {
			bindingID, ok, err := store.GetResponse(ctx, state.key.TenantHash, state.key.Hash)
			if err != nil {
				return err
			}
			if ok {
				binding, found, err := store.GetBinding(ctx, bindingID)
				if err != nil {
					return err
				}
				if found && binding.TenantHash == state.key.TenantHash {
					state.binding, state.hasBinding = binding, true
					state.prefixCount = contextPrefixCountForBinding(binding, body.Messages)
				}
			}
		}
		if !state.hasBinding {
			binding, prefix, ok, err := resolveHistoryBinding(ctx, store, state.key.TenantHash, body.Messages, 64)
			if err != nil {
				return err
			}
			if ok {
				state.binding, state.hasBinding, state.prefixCount = binding, true, prefix
			}
		}
		return nil
	}
	if err := resolve(state.store); err != nil {
		state.store = m.markStoreError(err)
		if err := resolve(state.store); err != nil {
			return nil, err
		}
	}

	lockID := state.key.BindingID
	if state.hasBinding {
		lockID = state.binding.ID
	}
	if state.enforced && lockID != "" {
		release, err := state.store.Acquire(ctx, state.key.TenantHash+":"+lockID, m.config.LockTTL, m.config.LockWait)
		if err != nil && state.store == m.primary {
			state.store = m.markStoreError(err)
			release, err = state.store.Acquire(ctx, state.key.TenantHash+":"+lockID, m.config.LockTTL, m.config.LockWait)
		}
		if err != nil {
			return nil, err
		}
		state.releaseLock = release
		if binding, ok, err := state.store.GetBinding(ctx, lockID); err == nil && ok && binding.TenantHash == state.key.TenantHash {
			state.binding, state.hasBinding = binding, true
			state.prefixCount = contextPrefixCountForBinding(binding, body.Messages)
		}
	}

	if body.AccountID != "" {
		state.accountID = body.AccountID
		state.proposedAccount = body.AccountID
		return state, nil
	}
	healthCache := map[string]bool{}
	sharedAvailable := func(accountID string) bool {
		if value, ok := healthCache[accountID]; ok {
			return value
		}
		if !available(accountID) {
			healthCache[accountID] = false
			return false
		}
		health, found, err := state.store.GetAccountHealth(ctx, accountID)
		if err != nil {
			if state.store == m.primary {
				state.store = m.markStoreError(err)
			}
			healthCache[accountID] = true
			return true
		}
		value := !found || (!health.AuthFailed && (health.CooldownUntil.IsZero() || time.Now().After(health.CooldownUntil)))
		healthCache[accountID] = value
		return value
	}
	if state.hasBinding && sharedAvailable(state.binding.AccountID) && (state.prefixCount > 0 || state.key.BindingID != "") {
		state.proposedAccount = state.binding.AccountID
		if state.enforced {
			state.accountID = state.binding.AccountID
			state.incremental = true
		}
	} else {
		if state.hasBinding {
			state.migrationReason = "bound_account_unavailable"
		}
		if accountID, ok, err := state.store.GetAccount(ctx, state.key.TenantHash, state.key.Hash); err == nil && ok && sharedAvailable(accountID) {
			state.proposedAccount = accountID
		} else {
			if err != nil && state.store == m.primary {
				state.store = m.markStoreError(err)
			}
			if selected, ok := selectRendezvousAccount(state.key.Hash, accounts, sharedAvailable); ok {
				state.proposedAccount = selected.ID
			}
		}
		if state.enforced {
			state.accountID = state.proposedAccount
		}
	}
	if state.enforced && state.accountID == "" {
		state.close()
		return nil, fmt.Errorf("no healthy account available for affinity route")
	}
	return state, nil
}

func (m *affinityManager) bindResponse(ctx context.Context, tenant, responseID, sessionID string) {
	if m == nil || m.config.Mode == affinityOff || responseID == "" || sessionID == "" {
		return
	}
	tenantHash := normalizeAffinityTenantHash(tenant)
	responseHash := affinityExplicitHash(tenantHash, "previous_response", responseID)
	store := m.store(ctx)
	sessionHash := affinityExplicitHash(tenantHash, "previous_response", sessionID)
	bindingID, ok, err := verifiedResponseBinding(ctx, store, tenantHash, sessionHash)
	if err != nil && store == m.primary {
		store = m.markStoreError(err)
		bindingID, ok, err = verifiedResponseBinding(ctx, store, tenantHash, sessionHash)
	}
	if err != nil || !ok {
		log.Printf("[affinity] response alias skipped response=%s session=%s verified=%t err=%v", shortPrefix(responseHash), shortPrefix(sessionHash), ok, err)
		return
	}
	if err := store.SetResponse(ctx, tenantHash, responseHash, bindingID, m.config.TTL); err != nil && store == m.primary {
		fallback := m.markStoreError(err)
		if fallbackBindingID, found, lookupErr := verifiedResponseBinding(ctx, fallback, tenantHash, sessionHash); lookupErr == nil && found {
			_ = fallback.SetResponse(ctx, tenantHash, responseHash, fallbackBindingID, m.config.TTL)
		}
	}
}

func verifiedResponseBinding(ctx context.Context, store affinityStore, tenantHash, responseHash string) (string, bool, error) {
	var lookupErr error
	if mapped, ok, err := store.GetResponse(ctx, tenantHash, responseHash); err != nil {
		lookupErr = err
	} else if ok {
		if binding, found, bindingErr := store.GetBinding(ctx, mapped); bindingErr != nil {
			lookupErr = bindingErr
		} else if found && binding.TenantHash == tenantHash {
			return mapped, true, nil
		}
	}

	// The first public response id in a chain is also its deterministic binding id.
	if binding, found, err := store.GetBinding(ctx, responseHash); err != nil {
		if lookupErr == nil {
			lookupErr = err
		}
	} else if found && binding.TenantHash == tenantHash {
		return responseHash, true, nil
	}
	return "", false, lookupErr
}

func (m *affinityManager) hasResponseBinding(ctx context.Context, tenant, responseID string) bool {
	if m == nil || m.config.Mode == affinityOff || responseID == "" {
		return false
	}
	tenantHash := normalizeAffinityTenantHash(tenant)
	responseHash := affinityExplicitHash(tenantHash, "previous_response", responseID)
	store := m.store(ctx)
	_, ok, err := verifiedResponseBinding(ctx, store, tenantHash, responseHash)
	if err != nil && store == m.primary {
		store = m.markStoreError(err)
		_, ok, err = verifiedResponseBinding(ctx, store, tenantHash, responseHash)
	}
	return err == nil && ok
}

func (m *affinityManager) markAccountFailure(accountID string, err error, window time.Duration) {
	if m == nil || m.config.Mode == affinityOff || accountID == "" || (!IsRateLimited(err) && !IsAuthFailure(err)) {
		return
	}
	health := affinityAccountHealth{}
	ttl := m.config.TTL
	if IsAuthFailure(err) {
		health.AuthFailed = true
		if ttl < 24*time.Hour {
			ttl = 24 * time.Hour
		}
	} else {
		if window <= 0 {
			window = time.Minute
		}
		health.CooldownUntil = time.Now().Add(window)
		if ttl < window {
			ttl = window
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := m.store(ctx)
	if setErr := store.SetAccountHealth(ctx, accountID, health, ttl); setErr != nil && store == m.primary {
		fallback := m.markStoreError(setErr)
		_ = fallback.SetAccountHealth(ctx, accountID, health, ttl)
	}
}

func (m *affinityManager) markAccountSuccess(accountID string) {
	if m == nil || m.config.Mode == affinityOff || accountID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store := m.store(ctx)
	if err := store.ClearAccountHealth(ctx, accountID); err != nil && store == m.primary {
		fallback := m.markStoreError(err)
		_ = fallback.ClearAccountHealth(ctx, accountID)
	}
}

func contextPrefixCountForBinding(binding affinityBinding, messages []oaiMsg) int {
	if binding.HistoryCount <= 0 || binding.HistoryCount >= len(messages) {
		return 0
	}
	if historyDigest(messages[:binding.HistoryCount]) != binding.HistoryDigest {
		return 0
	}
	for _, msg := range messages[:binding.HistoryCount] {
		if msg.Role == "assistant" {
			return binding.HistoryCount
		}
	}
	return 0
}

func (state *affinityRequest) apply(body *oaiReq) {
	if state == nil || !state.enforced {
		return
	}
	if state.accountID != "" {
		body.AccountID = state.accountID
	}
	if state.incremental && state.hasBinding {
		body.ConversationID = state.binding.ConversationID
		body.SessionID = state.binding.SessionID
	}
}

func (state *affinityRequest) markResolvedAccount(accountID string) {
	if state != nil && state.accountID == "" {
		state.accountID = accountID
	}
}

func (state *affinityRequest) markMigration(reason string) {
	if state == nil {
		return
	}
	state.migrationReason = reason
	state.incremental = false
}

func (state *affinityRequest) switchAccount(accountID string) {
	if state == nil || state.manager == nil || accountID == "" || state.accountID == accountID {
		return
	}
	state.accountID = accountID
}

func (state *affinityRequest) complete(ctx context.Context, body *oaiReq, accountID, conversationID, sessionID string, assistant oaiMsg, promptTokens, completionTokens int64) reuseUsage {
	usage := reuseUsage{PromptTokens: promptTokens, CompletionTokens: completionTokens}
	if state == nil || state.manager == nil || state.manager.config.Mode == affinityOff || conversationID == "" {
		return usage
	}
	confirmed := state.enforced && state.hasBinding && state.incremental && state.migrationReason == "" &&
		state.binding.AccountID == accountID && state.binding.ConversationID == body.ConversationID && state.prefixCount > 0
	if confirmed {
		prefixPrompt, _ := flattenPromptMessages(body.Messages[:state.prefixCount], nil)
		usage.CachedTokens = EstimateTokens(strings.TrimSpace(prefixPrompt))
		usage.Confirmed = usage.CachedTokens > 0
	}

	history := affinityBindingHistory(body.Messages, assistant)
	bindingID := state.key.BindingID
	if state.hasBinding {
		bindingID = state.binding.ID
	}
	if bindingID == "" {
		bindingID = hashString(state.key.TenantHash + "\x00binding\x00" + uuid.NewString())
	}
	now := time.Now().UTC()
	binding := affinityBinding{
		ID: bindingID, TenantHash: state.key.TenantHash, AffinityHash: state.key.Hash,
		AccountID: accountID, ConversationID: conversationID, SessionID: sessionID,
		HistoryDigest: historyDigest(history), HistoryCount: len(history),
		HistoryTokens: promptTokens + completionTokens, CreatedAt: now, LastUsedAt: now, Generation: 1,
	}
	if state.hasBinding {
		binding.CreatedAt = state.binding.CreatedAt
		binding.Generation = state.binding.Generation + 1
		ok, err := state.store.CompareAndSwapBinding(ctx, state.binding.ID, state.binding.Generation, binding, state.manager.config.TTL)
		if err != nil {
			fallback := state.manager.markStoreError(err)
			_ = fallback.PutBinding(ctx, binding, state.manager.config.TTL)
			state.store = fallback
		} else if !ok {
			log.Printf("[affinity] binding CAS lost id=%s generation=%d", state.binding.ID, state.binding.Generation)
			state.incremental = false
			usage.CachedTokens = 0
			usage.Confirmed = false
		}
	} else if err := state.store.PutBinding(ctx, binding, state.manager.config.TTL); err != nil {
		fallback := state.manager.markStoreError(err)
		_ = fallback.PutBinding(ctx, binding, state.manager.config.TTL)
		state.store = fallback
	}
	if err := state.store.SetAccount(ctx, state.key.TenantHash, state.key.Hash, accountID, state.manager.config.TTL); err != nil {
		fallback := state.manager.markStoreError(err)
		_ = fallback.SetAccount(ctx, state.key.TenantHash, state.key.Hash, accountID, state.manager.config.TTL)
		state.store = fallback
	}
	log.Printf("[affinity] mode=%s reason=%s account=%s binding=%s cache_hit=%t cached_tokens=%d migration=%s", state.manager.config.Mode, state.key.Reason, accountID, shortPrefix(bindingID), usage.Confirmed, usage.CachedTokens, state.migrationReason)
	return usage
}

func shortPrefix(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func (state *affinityRequest) close() {
	if state == nil || state.closed {
		return
	}
	state.closed = true
	if state.releaseLock != nil {
		state.releaseLock()
	}
}

func affinityBindingHistory(messages []oaiMsg, assistant oaiMsg) []oaiMsg {
	history := cloneMessages(messages)
	return append(history, assistant)
}

func affinityTenantIdentity(r *http.Request) string {
	return affinityTenantIdentityWithScope(r, "ip")
}

func affinityTenantIdentityWithScope(r *http.Request, scope string) string {
	if key := strings.TrimSpace(r.Header.Get("X-API-Key")); key != "" {
		return hashString(key)
	}
	if authHeader := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return hashString(strings.TrimSpace(authHeader[7:]))
	}
	if scope == "global" {
		return hashString("anonymous")
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	if host == "" {
		host = "unknown"
	}
	return hashString("anonymous-ip:" + host)
}

func (m *affinityManager) tenantIdentity(r *http.Request) string {
	if m == nil {
		return affinityTenantIdentity(r)
	}
	scope := m.config.AnonymousScope
	if scope == "" {
		scope = "ip"
	}
	return affinityTenantIdentityWithScope(r, scope)
}

func legacyConversationCacheAllowed(state *affinityRequest) bool {
	return state == nil || !state.enforced
}

func (s *Server) affinityTenantIdentity(r *http.Request) string {
	if s == nil || s.affinity == nil {
		return affinityTenantIdentity(r)
	}
	return s.affinity.tenantIdentity(r)
}

func (state *affinityRequest) String() string {
	if state == nil {
		return "affinity(off)"
	}
	return fmt.Sprintf("affinity(mode=%t reason=%s proposed=%s bound=%t prefix=%d)", state.enforced, state.key.Reason, state.proposedAccount, state.hasBinding, state.prefixCount)
}
