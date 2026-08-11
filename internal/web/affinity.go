package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"m365-copilot2api/internal/auth"
)

const previousResponseHeader = "X-M365-Previous-Response-Id"

type affinityKey struct {
	TenantHash string
	Hash       string
	BindingID  string
	Reason     string
}

type affinityBinding struct {
	ID             string    `json:"id"`
	TenantHash     string    `json:"tenant_hash"`
	AffinityHash   string    `json:"affinity_hash"`
	AccountID      string    `json:"account_id"`
	ConversationID string    `json:"conversation_id"`
	SessionID      string    `json:"session_id"`
	HistoryDigest  string    `json:"history_digest"`
	HistoryCount   int       `json:"history_count"`
	HistoryTokens  int64     `json:"history_tokens"`
	Generation     int64     `json:"generation"`
	CreatedAt      time.Time `json:"created_at"`
	LastUsedAt     time.Time `json:"last_used_at"`
}

type affinityStore interface {
	GetAccount(context.Context, string, string) (string, bool, error)
	SetAccount(context.Context, string, string, string, time.Duration) error
	GetResponse(context.Context, string, string) (string, bool, error)
	SetResponse(context.Context, string, string, string, time.Duration) error
	GetBinding(context.Context, string) (affinityBinding, bool, error)
	FindHistory(context.Context, string, []string) (affinityBinding, int, bool, error)
	PutBinding(context.Context, affinityBinding, time.Duration) error
	CompareAndSwapBinding(context.Context, string, int64, affinityBinding, time.Duration) (bool, error)
	Acquire(context.Context, string, time.Duration, time.Duration) (func(), error)
	GetAccountHealth(context.Context, string) (affinityAccountHealth, bool, error)
	SetAccountHealth(context.Context, string, affinityAccountHealth, time.Duration) error
	ClearAccountHealth(context.Context, string) error
	Healthy(context.Context) bool
	Close() error
}

type affinityAccountHealth struct {
	AuthFailed    bool      `json:"auth_failed"`
	CooldownUntil time.Time `json:"cooldown_until,omitempty"`
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func affinityExplicitHash(tenantHash, reason, value string) string {
	return hashString(tenantHash + "\x00" + reason + "\x00" + strings.TrimSpace(value))
}

func deriveAffinityKey(tenant string, body *oaiReq, r *http.Request) affinityKey {
	tenantHash := hashString(strings.TrimSpace(tenant))
	type candidate struct {
		reason string
		value  string
	}
	candidates := []candidate{
		{reason: "previous_response", value: r.Header.Get(previousResponseHeader)},
		{reason: "explicit_session", value: r.Header.Get(sessionHeaderName)},
		{reason: "explicit_session", value: body.SessionID},
		{reason: "explicit_session", value: body.SessionKey},
		{reason: "explicit_conversation", value: body.ConversationID},
		{reason: "prompt_cache_key", value: body.PromptCacheKey},
	}
	for _, c := range candidates {
		if value := strings.TrimSpace(c.value); value != "" {
			h := affinityExplicitHash(tenantHash, c.reason, value)
			bindingID := ""
			if c.reason != "prompt_cache_key" {
				bindingID = h
			}
			return affinityKey{TenantHash: tenantHash, Hash: h, BindingID: bindingID, Reason: c.reason}
		}
	}

	seed := struct {
		Model     string `json:"model"`
		System    []any  `json:"system"`
		FirstUser any    `json:"first_user"`
		Tools     any    `json:"tools"`
		Choice    any    `json:"tool_choice"`
	}{Model: body.Model, Tools: body.Tools, Choice: body.ToolChoice}
	for _, msg := range body.Messages {
		switch msg.Role {
		case "system", "developer":
			seed.System = append(seed.System, canonicalMessage(msg))
		case "user":
			if seed.FirstUser == nil {
				seed.FirstUser = canonicalMessage(msg)
			}
		}
	}
	b, _ := json.Marshal(seed)
	h := hashString(tenantHash + "\x00content\x00" + string(b))
	return affinityKey{TenantHash: tenantHash, Hash: h, Reason: "content_seed"}
}

func canonicalMessage(msg oaiMsg) map[string]any {
	out := map[string]any{
		"role":    strings.ToLower(strings.TrimSpace(msg.Role)),
		"content": canonicalContentValue(msg.Content),
	}
	if msg.Name != "" {
		out["name"] = msg.Name
	}
	if msg.ToolCallID != "" {
		out["tool_result"] = canonicalContentValue(msg.Content)
	}
	if len(msg.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(msg.ToolCalls))
		for _, call := range msg.ToolCalls {
			fn, _ := call["function"].(map[string]any)
			calls = append(calls, map[string]any{
				"type":      firstNonEmpty(fmt.Sprint(call["type"]), "function"),
				"name":      fmt.Sprint(fn["name"]),
				"arguments": fmt.Sprint(fn["arguments"]),
			})
		}
		out["tool_calls"] = calls
	}
	return out
}

func canonicalContentValue(value any) any {
	switch typed := value.(type) {
	case nil, string, bool, float64, json.Number:
		return typed
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(typed, &decoded) == nil {
			return canonicalContentValue(decoded)
		}
		return string(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = canonicalContentValue(item)
		}
		return out
	case []map[string]any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = canonicalContentValue(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = canonicalContentValue(item)
		}
		return out
	default:
		return fmt.Sprint(typed)
	}
}

func historyDigest(messages []oaiMsg) string {
	canonical := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		canonical = append(canonical, canonicalMessage(msg))
	}
	b, _ := json.Marshal(canonical)
	return hashString(string(b))
}

func prefixHistoryDigests(messages []oaiMsg, maxMessages int) ([]string, []int) {
	if len(messages) < 2 {
		return nil, nil
	}
	upper := len(messages) - 1
	lower := 1
	if maxMessages > 0 && upper-lower+1 > maxMessages {
		lower = upper - maxMessages + 1
	}
	digests := make([]string, 0, upper-lower+1)
	counts := make([]int, 0, upper-lower+1)
	for n := upper; n >= lower; n-- {
		hasAssistant := false
		for _, msg := range messages[:n] {
			if msg.Role == "assistant" {
				hasAssistant = true
				break
			}
		}
		if !hasAssistant {
			continue
		}
		digests = append(digests, historyDigest(messages[:n]))
		counts = append(counts, n)
	}
	return digests, counts
}

func resolveHistoryBinding(ctx context.Context, store affinityStore, tenantHash string, messages []oaiMsg, maxMessages int) (affinityBinding, int, bool, error) {
	digests, counts := prefixHistoryDigests(messages, maxMessages)
	if len(digests) == 0 {
		return affinityBinding{}, 0, false, nil
	}
	binding, index, ok, err := store.FindHistory(ctx, tenantHash, digests)
	if err != nil || !ok {
		return affinityBinding{}, 0, false, err
	}
	if index < 0 || index >= len(counts) || binding.HistoryDigest != digests[index] || binding.HistoryCount != counts[index] {
		return affinityBinding{}, 0, false, nil
	}
	return binding, counts[index], true, nil
}

func selectRendezvousAccount(key string, accounts []auth.AccountToken, available func(string) bool, inflight map[string]int, limit int) (auth.AccountToken, bool) {
	if limit <= 0 {
		limit = 8
	}
	bestScore := math.Inf(-1)
	var best auth.AccountToken
	found := false
	for _, account := range accounts {
		if account.ID == "" || !available(account.ID) || inflight[account.ID] >= limit {
			continue
		}
		sum := sha256.Sum256([]byte(key + "\x00" + account.ID))
		var raw uint64
		for i := 0; i < 8; i++ {
			raw = raw<<8 | uint64(sum[i])
		}
		u := (float64(raw) + 1) / (float64(^uint64(0)) + 1)
		weight := float64(limit-inflight[account.ID]) / float64(limit)
		score := weight / -math.Log(u)
		if !found || score > bestScore {
			bestScore, best, found = score, account, true
		}
	}
	return best, found
}

type memoryAffinityStore struct {
	mu        sync.Mutex
	ttl       time.Duration
	max       int
	accounts  map[string]memoryAccountAffinity
	responses map[string]memoryAccountAffinity
	bindings  map[string]memoryBinding
	history   map[string]string
	locks     map[string]memoryLock
	health    map[string]memoryAccountHealth
}

type memoryAccountAffinity struct {
	AccountID string
	ExpiresAt time.Time
}

type memoryBinding struct {
	Value     affinityBinding
	ExpiresAt time.Time
}

type memoryLock struct {
	Owner     string
	ExpiresAt time.Time
}

type memoryAccountHealth struct {
	Value     affinityAccountHealth
	ExpiresAt time.Time
}

func newMemoryAffinityStore(ttl time.Duration, max int) *memoryAffinityStore {
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	if max <= 0 {
		max = 10000
	}
	return &memoryAffinityStore{
		ttl: ttl, max: max,
		accounts:  map[string]memoryAccountAffinity{},
		responses: map[string]memoryAccountAffinity{},
		bindings:  map[string]memoryBinding{},
		history:   map[string]string{},
		locks:     map[string]memoryLock{},
		health:    map[string]memoryAccountHealth{},
	}
}

func accountStoreKey(tenantHash, affinityHash string) string { return tenantHash + ":" + affinityHash }
func historyStoreKey(tenantHash, digest string) string       { return tenantHash + ":" + digest }

func (s *memoryAffinityStore) cleanupLocked(now time.Time) {
	for key, value := range s.accounts {
		if now.After(value.ExpiresAt) {
			delete(s.accounts, key)
		}
	}
	for key, value := range s.responses {
		if now.After(value.ExpiresAt) {
			delete(s.responses, key)
		}
	}
	for id, value := range s.bindings {
		if now.After(value.ExpiresAt) {
			delete(s.bindings, id)
			delete(s.history, historyStoreKey(value.Value.TenantHash, value.Value.HistoryDigest))
		}
	}
	for key, value := range s.locks {
		if now.After(value.ExpiresAt) {
			delete(s.locks, key)
		}
	}
	for key, value := range s.health {
		if now.After(value.ExpiresAt) {
			delete(s.health, key)
		}
	}
	if len(s.bindings) <= s.max {
		return
	}
	list := make([]memoryBinding, 0, len(s.bindings))
	for _, value := range s.bindings {
		list = append(list, value)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Value.LastUsedAt.Before(list[j].Value.LastUsedAt) })
	for _, value := range list[:len(list)-s.max] {
		delete(s.bindings, value.Value.ID)
		delete(s.history, historyStoreKey(value.Value.TenantHash, value.Value.HistoryDigest))
	}
}

func (s *memoryAffinityStore) GetAccount(_ context.Context, tenantHash, affinityHash string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(time.Now())
	v, ok := s.accounts[accountStoreKey(tenantHash, affinityHash)]
	return v.AccountID, ok, nil
}

func (s *memoryAffinityStore) SetAccount(_ context.Context, tenantHash, affinityHash, accountID string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = s.ttl
	}
	s.mu.Lock()
	s.accounts[accountStoreKey(tenantHash, affinityHash)] = memoryAccountAffinity{AccountID: accountID, ExpiresAt: time.Now().Add(ttl)}
	s.mu.Unlock()
	return nil
}

func (s *memoryAffinityStore) GetResponse(_ context.Context, tenantHash, responseHash string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(time.Now())
	value, ok := s.responses[accountStoreKey(tenantHash, responseHash)]
	return value.AccountID, ok, nil
}

func (s *memoryAffinityStore) SetResponse(_ context.Context, tenantHash, responseHash, bindingID string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = s.ttl
	}
	s.mu.Lock()
	s.responses[accountStoreKey(tenantHash, responseHash)] = memoryAccountAffinity{AccountID: bindingID, ExpiresAt: time.Now().Add(ttl)}
	s.mu.Unlock()
	return nil
}

func (s *memoryAffinityStore) GetBinding(_ context.Context, id string) (affinityBinding, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(time.Now())
	v, ok := s.bindings[id]
	return v.Value, ok, nil
}

func (s *memoryAffinityStore) FindHistory(_ context.Context, tenantHash string, digests []string) (affinityBinding, int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(time.Now())
	for index, digest := range digests {
		id, ok := s.history[historyStoreKey(tenantHash, digest)]
		if !ok {
			continue
		}
		value, ok := s.bindings[id]
		if ok {
			return value.Value, index, true, nil
		}
	}
	return affinityBinding{}, 0, false, nil
}

func (s *memoryAffinityStore) PutBinding(_ context.Context, binding affinityBinding, ttl time.Duration) error {
	if binding.ID == "" || binding.TenantHash == "" {
		return errors.New("affinity binding id and tenant are required")
	}
	if ttl <= 0 {
		ttl = s.ttl
	}
	now := time.Now().UTC()
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.LastUsedAt = now
	if binding.Generation == 0 {
		binding.Generation = 1
	}
	s.mu.Lock()
	if old, ok := s.bindings[binding.ID]; ok && old.Value.HistoryDigest != "" && old.Value.HistoryDigest != binding.HistoryDigest {
		delete(s.history, historyStoreKey(old.Value.TenantHash, old.Value.HistoryDigest))
	}
	s.bindings[binding.ID] = memoryBinding{Value: binding, ExpiresAt: now.Add(ttl)}
	if binding.HistoryDigest != "" {
		s.history[historyStoreKey(binding.TenantHash, binding.HistoryDigest)] = binding.ID
	}
	s.cleanupLocked(now)
	s.mu.Unlock()
	return nil
}

func (s *memoryAffinityStore) CompareAndSwapBinding(_ context.Context, id string, generation int64, binding affinityBinding, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.cleanupLocked(now)
	current, ok := s.bindings[id]
	if !ok || current.Value.Generation != generation {
		return false, nil
	}
	if ttl <= 0 {
		ttl = s.ttl
	}
	delete(s.history, historyStoreKey(current.Value.TenantHash, current.Value.HistoryDigest))
	binding.LastUsedAt = now
	s.bindings[id] = memoryBinding{Value: binding, ExpiresAt: now.Add(ttl)}
	if binding.HistoryDigest != "" {
		s.history[historyStoreKey(binding.TenantHash, binding.HistoryDigest)] = id
	}
	return true, nil
}

func randomOwner() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *memoryAffinityStore) Acquire(ctx context.Context, key string, ttl, wait time.Duration) (func(), error) {
	if ttl <= 0 {
		ttl = 180 * time.Second
	}
	if wait <= 0 {
		wait = 120 * time.Second
	}
	owner := randomOwner()
	deadline := time.Now().Add(wait)
	for {
		s.mu.Lock()
		now := time.Now()
		s.cleanupLocked(now)
		if _, exists := s.locks[key]; !exists {
			s.locks[key] = memoryLock{Owner: owner, ExpiresAt: now.Add(ttl)}
			s.mu.Unlock()
			return func() {
				s.mu.Lock()
				if current, ok := s.locks[key]; ok && current.Owner == owner {
					delete(s.locks, key)
				}
				s.mu.Unlock()
			}, nil
		}
		s.mu.Unlock()
		if time.Now().After(deadline) {
			return nil, errors.New("session affinity lock timeout")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *memoryAffinityStore) Healthy(context.Context) bool { return true }
func (s *memoryAffinityStore) Close() error                 { return nil }

func (s *memoryAffinityStore) GetAccountHealth(_ context.Context, accountID string) (affinityAccountHealth, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked(time.Now())
	value, ok := s.health[accountID]
	return value.Value, ok, nil
}

func (s *memoryAffinityStore) SetAccountHealth(_ context.Context, accountID string, health affinityAccountHealth, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = s.ttl
	}
	s.mu.Lock()
	s.health[accountID] = memoryAccountHealth{Value: health, ExpiresAt: time.Now().Add(ttl)}
	s.mu.Unlock()
	return nil
}

func (s *memoryAffinityStore) ClearAccountHealth(_ context.Context, accountID string) error {
	s.mu.Lock()
	delete(s.health, accountID)
	s.mu.Unlock()
	return nil
}

type reuseUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	CachedTokens     int64
	Confirmed        bool
}

func confirmedCachedTokens(u reuseUsage) int64 {
	if !u.Confirmed || u.CachedTokens < 0 || u.CachedTokens > u.PromptTokens {
		return 0
	}
	return u.CachedTokens
}

func chatUsage(u reuseUsage) map[string]any {
	cached := confirmedCachedTokens(u)
	return map[string]any{
		"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens,
		"total_tokens":          u.PromptTokens + u.CompletionTokens,
		"prompt_tokens_details": map[string]any{"cached_tokens": cached},
	}
}

func responsesUsage(u reuseUsage) map[string]any {
	cached := confirmedCachedTokens(u)
	return map[string]any{
		"input_tokens": u.PromptTokens, "output_tokens": u.CompletionTokens,
		"total_tokens":         u.PromptTokens + u.CompletionTokens,
		"input_tokens_details": map[string]any{"cached_tokens": cached},
	}
}

func anthropicUsage(u reuseUsage) map[string]any {
	return map[string]any{
		"input_tokens": u.PromptTokens, "output_tokens": u.CompletionTokens,
		"cache_read_input_tokens":     confirmedCachedTokens(u),
		"cache_creation_input_tokens": int64(0),
	}
}

func numberInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	case json.Number:
		value, _ := n.Int64()
		return value
	default:
		return 0
	}
}

func cachedTokensFromChatResult(src map[string]any) int64 {
	usage, _ := src["usage"].(map[string]any)
	if usage == nil {
		return 0
	}
	details, _ := usage["prompt_tokens_details"].(map[string]any)
	if details == nil {
		return 0
	}
	return numberInt64(details["cached_tokens"])
}

func usageSourceFromCachedTokens(cached int64) string {
	if cached > 0 {
		return "conversation_reuse"
	}
	return "none"
}
