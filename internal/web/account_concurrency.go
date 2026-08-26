package web

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"m365-copilot2api/internal/chathub"
)

const defaultAccountConcurrency = 8

const (
	defaultTransientRetryAttempts = 2
	maxTransientRetryAttempts     = 5
	defaultTransientRetryDelay    = 100 * time.Millisecond
)

type accountConcurrency struct {
	mu       sync.Mutex
	limit    int
	inflight map[string]int
	changed  chan struct{}
}

func newAccountConcurrency() *accountConcurrency {
	limit := defaultAccountConcurrency
	if raw := strings.TrimSpace(os.Getenv("M365_ACCOUNT_DEFAULT_CONCURRENCY")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	return &accountConcurrency{limit: limit, inflight: map[string]int{}, changed: make(chan struct{})}
}

func (c *accountConcurrency) Available(accountID string) bool {
	if c == nil || accountID == "" {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inflight[accountID] < c.limit
}

func (c *accountConcurrency) Acquire(ctx context.Context, accountID string) (func(), error) {
	if c == nil || accountID == "" {
		return func() {}, nil
	}
	for {
		c.mu.Lock()
		if c.inflight[accountID] < c.limit {
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
					close(c.changed)
					c.changed = make(chan struct{})
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
	return map[string]any{"limit": c.limit, "inflight": inflight}
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
		if err == nil || attempt >= maxRetries || !IsTransientUpstreamFailure(err) || (observed != nil && observed()) {
			return result, err
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		retry := attempt + 1
		log.Printf("[transient-retry] account=%s retry=%d/%d category=%s", accountID, retry, maxRetries, ClassifyError(err))
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
	s.markAccountResult(accountID, err)
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
	s.markAccountResult(accountID, err)
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
	s.markAccountResult(accountID, err)
	return result, err
}
