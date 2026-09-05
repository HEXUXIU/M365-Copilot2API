package web

import (
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"m365-copilot2api/internal/chathub"
)

const (
	defaultAccountConcurrency = 256
	minAccountConcurrency     = 1
	maxAccountConcurrency     = 1024
)

const (
	defaultTransientRetryAttempts = 2
	maxTransientRetryAttempts     = 5
	defaultTransientRetryDelay    = 100 * time.Millisecond
)

type accountConcurrency struct {
	mu         sync.Mutex
	limit      int
	inflight   map[string]int
	changed    chan struct{}
	adaptive   bool
	perAccount map[string]int
	successes  map[string]int
}

func newAccountConcurrency() *accountConcurrency {
	return &accountConcurrency{
		limit:      configuredAccountConcurrencyLimit(),
		inflight:   map[string]int{},
		changed:    make(chan struct{}),
		adaptive:   adaptiveAccountConcurrencyEnv(),
		perAccount: map[string]int{},
		successes:  map[string]int{},
	}
}

// configuredAccountConcurrencyLimit reads the current setting and accepts the
// legacy variable as a fallback. Invalid or out-of-range values never disable
// throttling; they fall back to the documented default.
func configuredAccountConcurrencyLimit() int {
	for _, name := range []string{"M365_ACCOUNT_CONCURRENCY_LIMIT", "M365_ACCOUNT_DEFAULT_CONCURRENCY"} {
		if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err == nil && parsed >= minAccountConcurrency && parsed <= maxAccountConcurrency {
				return parsed
			}
		}
	}
	return defaultAccountConcurrency
}

func accountConcurrencyEnv(name string, fallback int) int {
	raw, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < minAccountConcurrency || parsed > maxAccountConcurrency {
		return fallback
	}
	return parsed
}

func adaptiveAccountConcurrencyEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("M365_ADAPTIVE_ACCOUNT_CONCURRENCY"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (c *accountConcurrency) notifyLocked() {
	if c.changed == nil {
		c.changed = make(chan struct{})
		return
	}
	close(c.changed)
	c.changed = make(chan struct{})
}

func (c *accountConcurrency) effectiveLimitLocked(accountID string) int {
	limit := c.limit
	if limit < minAccountConcurrency {
		limit = minAccountConcurrency
	}
	if !c.adaptive || accountID == "" {
		return limit
	}
	if c.perAccount == nil {
		c.perAccount = map[string]int{}
	}
	if current, ok := c.perAccount[accountID]; ok {
		if current < minAccountConcurrency {
			return minAccountConcurrency
		}
		if current < limit {
			return current
		}
	}
	// Start cautiously when adaptive mode is enabled, then grow after
	// successful completions. This keeps a newly recovered account from
	// immediately reproducing the burst that caused its previous 429.
	start := limit / 4
	if start < 1 {
		start = 1
	}
	c.perAccount[accountID] = start
	return start
}

// SetAdaptive toggles per-account AIMD limits without interrupting active
// requests. Disabling it restores the configured shared limit immediately.
func (c *accountConcurrency) SetAdaptive(enabled bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.adaptive == enabled {
		c.mu.Unlock()
		return
	}
	c.adaptive = enabled
	c.perAccount = map[string]int{}
	c.successes = map[string]int{}
	c.notifyLocked()
	c.mu.Unlock()
}

func (c *accountConcurrency) Adaptive() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.adaptive
}

func (c *accountConcurrency) effectiveLimit(accountID string) int {
	if c == nil {
		return defaultAccountConcurrency
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.effectiveLimitLocked(accountID)
}

// Observe applies additive increase/multiplicative decrease to account
// concurrency. Account rate/auth failures and structured provider failures
// reduce the limit; ordinary transport errors stay in the proxy retry layer.
func (c *accountConcurrency) Observe(accountID string, err error) {
	if c == nil || accountID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.adaptive {
		return
	}
	if c.successes == nil {
		c.successes = map[string]int{}
	}
	current := c.effectiveLimitLocked(accountID)
	if err != nil {
		if !IsRateLimited(err) && !IsAuthFailure(err) && !IsUpstreamInternalError(err) {
			return
		}
		next := (current + 1) / 2
		if next < minAccountConcurrency {
			next = minAccountConcurrency
		}
		c.perAccount[accountID] = next
		c.successes[accountID] = 0
		c.notifyLocked()
		return
	}
	c.successes[accountID]++
	// Grow every two successful completions to avoid oscillating on single
	// responses while still recovering quickly after a cooldown.
	if c.successes[accountID]%2 != 0 || current >= c.limit {
		return
	}
	c.perAccount[accountID] = current + 1
	c.notifyLocked()
}

// SetLimit updates the shared per-account limit without interrupting active
// calls. Waiters are notified when the limit changes so increases take effect
// immediately and decreases drain naturally as in-flight calls finish.
func (c *accountConcurrency) SetLimit(limit int) {
	if c == nil || limit < minAccountConcurrency || limit > maxAccountConcurrency {
		return
	}
	c.mu.Lock()
	if c.changed == nil {
		c.changed = make(chan struct{})
	}
	if c.limit == limit {
		c.mu.Unlock()
		return
	}
	c.limit = limit
	for accountID, current := range c.perAccount {
		if current > limit {
			c.perAccount[accountID] = limit
		}
	}
	c.notifyLocked()
	c.mu.Unlock()
}

func (c *accountConcurrency) Limit() int {
	if c == nil {
		return defaultAccountConcurrency
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.limit
}

func (c *accountConcurrency) Available(accountID string) bool {
	if c == nil || accountID == "" {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight[accountID] < c.effectiveLimitLocked(accountID)
}

func (c *accountConcurrency) Acquire(ctx context.Context, accountID string) (func(), error) {
	if c == nil || accountID == "" {
		return func() {}, nil
	}
	for {
		c.mu.Lock()
		if c.inflight[accountID] < c.effectiveLimitLocked(accountID) {
			c.inflight[accountID]++
			c.mu.Unlock()
			var once sync.Once
			return func() {
				once.Do(func() {
					c.mu.Lock()
					if c.inflight[accountID] <= 1 {
						delete(c.inflight, accountID)
					} else {
						c.inflight[accountID]--
					}
					c.notifyLocked()
					c.mu.Unlock()
				})
			}, nil
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (c *accountConcurrency) Snapshot() map[string]any {
	if c == nil {
		return map[string]any{"limit": defaultAccountConcurrency, "inflight": map[string]int{}}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	inflight := make(map[string]int, len(c.inflight))
	for accountID, count := range c.inflight {
		inflight[accountID] = count
	}
	limits := make(map[string]int, len(c.perAccount))
	for accountID := range c.perAccount {
		limits[accountID] = c.effectiveLimitLocked(accountID)
	}
	return map[string]any{"limit": c.limit, "adaptive": c.adaptive, "inflight": inflight, "accountLimits": limits}
}

func (c *accountConcurrency) Inflight(accountID string) int {
	if c == nil || accountID == "" {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight[accountID]
}

func (s *Server) accountAvailable(accountID string) bool {
	if s.tokens != nil && !s.tokens.ScheduleEnabled(accountID) {
		return false
	}
	return s.accountPool.Available(accountID) && s.accountConcurrency.Available(accountID)
}

// stickyAccountAvailable keeps cache affinity from bypassing account-level
// cooldowns. Sticky routing may continue through transient transport failures
// (those do not enter accountHealth cooldown), but quota/auth failures must
// stop the bound account from receiving another upstream request.
func (s *Server) stickyAccountAvailable(accountID string, stickyLimit int) bool {
	if s == nil || accountID == "" {
		return false
	}
	if s.tokens != nil && !s.tokens.ScheduleEnabled(accountID) {
		return false
	}
	if s.accountPool != nil && !s.accountPool.Available(accountID) {
		return false
	}
	limit := s.accountConcurrency.Limit()
	if stickyLimit > 0 && stickyLimit < limit {
		limit = stickyLimit
	}
	return s.accountConcurrency.Inflight(accountID) < limit
}

func (s *Server) accountClient(accountID string) *chathub.Client {
	if acc, ok := s.tokens.Get(accountID); ok && acc.BoundProxy != "" {
		return s.clientForProxy(acc.BoundProxy)
	}
	return s.chat
}

func transientRetryAttempts() int {
	raw := strings.TrimSpace(os.Getenv("M365_TRANSIENT_RETRY_ATTEMPTS"))
	if raw == "" {
		return defaultTransientRetryAttempts
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return defaultTransientRetryAttempts
	}
	if parsed > maxTransientRetryAttempts {
		return maxTransientRetryAttempts
	}
	return parsed
}

func transientRetryDelay(attempt int) time.Duration {
	base := defaultTransientRetryDelay
	if raw := strings.TrimSpace(os.Getenv("M365_TRANSIENT_RETRY_DELAY_MS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 && parsed <= 5000 {
			base = time.Duration(parsed) * time.Millisecond
		}
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for i := 1; i < attempt && delay < 2*time.Second; i++ {
		delay *= 2
	}
	if delay > 2*time.Second {
		return 2 * time.Second
	}
	return delay
}

// callWithTransientRetry reconnects the same account and conversation only
// while no result has been exposed to the caller. Account failover remains in
// the protocol handlers, where conversation migration can be decided safely.
func callWithTransientRetry(ctx context.Context, accountID string, observed func() bool, call func() (chathub.Result, error)) (chathub.Result, error) {
	maxRetries := transientRetryAttempts()
	for attempt := 0; ; attempt++ {
		result, err := call()
		upstreamCanceled := errors.Is(err, context.Canceled) && ctx.Err() == nil
		retryLimit := maxRetries
		if IsUpstreamInternalError(err) && retryLimit > 1 {
			retryLimit = 1
		}
		if err == nil || attempt >= retryLimit || (!IsTransientUpstreamFailure(err) && !upstreamCanceled) || (observed != nil && observed()) {
			return result, err
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		retry := attempt + 1
		log.Printf("[transient-retry] account=%s retry=%d/%d category=%s", accountID, retry, retryLimit, ClassifyError(err))
		timer := time.NewTimer(transientRetryDelay(retry))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return result, ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *Server) chatWithAccount(ctx context.Context, accountID string, account chathub.Account, request chathub.Request) (chathub.Result, error) {
	release, err := s.accountConcurrency.Acquire(ctx, accountID)
	if err != nil {
		return chathub.Result{}, err
	}
	defer release()
	if s.accountPool != nil {
		s.accountPool.MarkCall(accountID)
	}
	client := s.accountClient(accountID)
	result, err := callWithTransientRetry(ctx, accountID, nil, func() (chathub.Result, error) {
		return client.Chat(ctx, account, request)
	})
	s.recordAccountChatResult(accountID, result, err)
	return result, err
}

func (s *Server) chatWithAccountEvents(ctx context.Context, accountID string, account chathub.Account, request chathub.Request, onEvent func(chathub.StreamEvent) error) (chathub.Result, error) {
	release, err := s.accountConcurrency.Acquire(ctx, accountID)
	if err != nil {
		return chathub.Result{}, err
	}
	defer release()
	if s.accountPool != nil {
		s.accountPool.MarkCall(accountID)
	}
	client := s.accountClient(accountID)
	var observed atomic.Bool
	wrappedEvent := func(event chathub.StreamEvent) error {
		observed.Store(true)
		return onEvent(event)
	}
	result, err := callWithTransientRetry(ctx, accountID, observed.Load, func() (chathub.Result, error) {
		return client.ChatWithEvents(ctx, account, request, wrappedEvent)
	})
	s.recordAccountChatResult(accountID, result, err)
	return result, err
}

func (s *Server) chatWithAccountReasoning(ctx context.Context, accountID string, account chathub.Account, request chathub.Request, onDelta, onReasoning func(string) error) (chathub.Result, error) {
	release, err := s.accountConcurrency.Acquire(ctx, accountID)
	if err != nil {
		return chathub.Result{}, err
	}
	defer release()
	if s.accountPool != nil {
		s.accountPool.MarkCall(accountID)
	}
	client := s.accountClient(accountID)
	var observed atomic.Bool
	wrappedDelta := func(value string) error {
		if value != "" {
			observed.Store(true)
		}
		return onDelta(value)
	}
	wrappedReasoning := func(value string) error {
		if value != "" {
			observed.Store(true)
		}
		return onReasoning(value)
	}
	result, err := callWithTransientRetry(ctx, accountID, observed.Load, func() (chathub.Result, error) {
		return client.ChatWithReasoning(ctx, account, request, wrappedDelta, wrappedReasoning)
	})
	s.recordAccountChatResult(accountID, result, err)
	return result, err
}
