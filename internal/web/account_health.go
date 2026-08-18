package web

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"m365-copilot2api/internal/chathub"
)

// UpstreamHTTPError carries the HTTP status of a failed upstream request so
// callers can distinguish rate limiting (429), authorization issues (401/403)
// and transient server errors (5xx) from one another.
type UpstreamHTTPError struct {
	Status     int
	RetryAfter int
	Body       string
}

func (e *UpstreamHTTPError) Error() string {
	return fmt.Sprintf("upstream http %d", e.Status)
}

// IsRateLimited reports whether err represents an upstream 429 or an
// indistinguishable throttling signal (rate limit, too many requests,
// throttled).
func IsRateLimited(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, chathub.ErrRateLimitNotice) {
		return true
	}
	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) {
		if httpErr.Status == 429 {
			return true
		}
		if httpErr.Status != 401 && httpErr.Status != 403 && httpErr.Status != 503 {
			if strings.Contains(strings.ToLower(httpErr.Body), "limited") {
				return true
			}
		}
	}
	var dialErr *chathub.DialError
	if errors.As(err, &dialErr) {
		return dialErr.Status == 429
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "limited") ||
		strings.Contains(msg, "throttl")
}

func IsInsufficientQuota(err error) bool {
	return errors.Is(err, chathub.ErrMeteringOutOfCredits)
}

func IsPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == 403
	}
	var dialErr *chathub.DialError
	if errors.As(err, &dialErr) {
		return dialErr.Status == 403
	}
	return false
}

func IsServerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == 503
	}
	var dialErr *chathub.DialError
	if errors.As(err, &dialErr) {
		return dialErr.Status == 503
	}
	return false
}

func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded")
}

func IsContentBlocked(err error) bool {
	return errors.Is(err, chathub.ErrContentPolicyBlocked)
}

func IsAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == 401
	}
	var dialErr *chathub.DialError
	if errors.As(err, &dialErr) {
		return dialErr.Status == 401
	}
	return false
}

func IsEmptyCompletion(err error) bool {
	return errors.Is(err, chathub.ErrEmptyCompletion)
}

// RetryAfterSeconds returns the upstream Retry-After hint for a rate-limited
// error, or 0 when absent. The web layer surfaces this to clients so they can
// back off instead of hammering a throttled pool.
func RetryAfterSeconds(err error) int {
	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.RetryAfter
	}
	var dialErr *chathub.DialError
	if errors.As(err, &dialErr) {
		return dialErr.RetryAfter
	}
	return 0
}

// accountHealth tracks per-account failure state: rate-limited accounts are
// cooled down and skipped by the round-robin until the window expires, and
// auth-failed accounts are pinned as unusable.
type accountHealth struct {
	mu       sync.Mutex
	cooldown map[string]time.Time
	authFail map[string]bool
	limited  map[string]bool
	calls    map[string]uint64
}

func newAccountHealth() *accountHealth {
	return &accountHealth{cooldown: map[string]time.Time{}, authFail: map[string]bool{}, limited: map[string]bool{}, calls: map[string]uint64{}}
}

func (h *accountHealth) cleanupExpiredCooldownLocked(accountID string) {
	until, ok := h.cooldown[accountID]
	if !ok || time.Now().Before(until) {
		return
	}
	rateLimited := h.limited[accountID]
	delete(h.cooldown, accountID)
	delete(h.limited, accountID)
	if rateLimited {
		delete(h.calls, accountID)
	}
}

func (h *accountHealth) MarkCall(accountID string) {
	if h == nil || accountID == "" {
		return
	}
	h.mu.Lock()
	h.cleanupExpiredCooldownLocked(accountID)
	h.calls[accountID]++
	h.mu.Unlock()
}

func (h *accountHealth) CallCount(accountID string) uint64 {
	if h == nil {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanupExpiredCooldownLocked(accountID)
	return h.calls[accountID]
}

func (h *accountHealth) RateLimited(accountID string) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanupExpiredCooldownLocked(accountID)
	return h.limited[accountID]
}

func (h *accountHealth) MarkFailure(accountID string, err error, window time.Duration) {
	if window <= 0 {
		window = 60 * time.Second
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if IsAuthFailure(err) {
		cooldown := window
		if cooldown > 2*time.Minute {
			cooldown = 2 * time.Minute
		}
		h.cooldown[accountID] = time.Now().Add(cooldown)
		delete(h.authFail, accountID)
		delete(h.limited, accountID)
		return
	}
	if IsPermissionDenied(err) {
		cooldown := window
		if cooldown > 5*time.Minute {
			cooldown = 5 * time.Minute
		}
		h.cooldown[accountID] = time.Now().Add(cooldown)
		delete(h.authFail, accountID)
		delete(h.limited, accountID)
		return
	}
	if IsRateLimited(err) {
		delete(h.authFail, accountID)
		h.limited[accountID] = true
		cd := window
		if ra := RetryAfterSeconds(err); ra > 0 {
			cd = time.Duration(ra) * time.Second
			if cd > time.Hour {
				cd = time.Hour
			}
		}
		h.cooldown[accountID] = time.Now().Add(cd)
		return
	}
	if IsInsufficientQuota(err) {
		delete(h.authFail, accountID)
		h.cooldown[accountID] = time.Now().Add(5 * time.Minute)
		return
	}
	if IsServerUnavailable(err) {
		cooldown := window
		if cooldown > 2*time.Minute {
			cooldown = 2 * time.Minute
		}
		h.cooldown[accountID] = time.Now().Add(cooldown)
		return
	}
	if IsTimeout(err) {
		h.cooldown[accountID] = time.Now().Add(window)
	}
}

// MarkSuccess clears any failure state after a healthy response.
func (h *accountHealth) MarkSuccess(accountID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.cooldown, accountID)
	delete(h.authFail, accountID)
	delete(h.limited, accountID)
}

// Available reports whether the account may be used right now.
func (h *accountHealth) Available(accountID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.authFail[accountID] {
		return false
	}
	h.cleanupExpiredCooldownLocked(accountID)
	if until, ok := h.cooldown[accountID]; ok && time.Now().Before(until) {
		return false
	}
	return true
}

func (h *accountHealth) CooldownUntil(accountID string) (time.Time, bool) {
	if h == nil {
		return time.Time{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanupExpiredCooldownLocked(accountID)
	until, ok := h.cooldown[accountID]
	if !ok {
		return time.Time{}, false
	}
	return until, true
}

// Snapshot returns a copy of the current health state for the admin UI.
func (h *accountHealth) Snapshot() map[string]map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]map[string]any, len(h.cooldown)+len(h.authFail))
	for id, until := range h.cooldown {
		out[id] = map[string]any{"available": time.Now().After(until), "cooldownUntil": until}
	}
	for id, failed := range h.authFail {
		if failed {
			if _, ok := out[id]; !ok {
				out[id] = map[string]any{}
			}
			out[id]["authFailed"] = true
		}
	}
	return out
}

func (h *accountHealth) ClearAllCooldowns() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cooldown = map[string]time.Time{}
	h.authFail = map[string]bool{}
	h.limited = map[string]bool{}
	h.calls = map[string]uint64{}
}

// EarliestRecovery returns the earliest time at which any account may become
// available again. Used to populate Retry-After when all accounts are cooling.
func (h *accountHealth) EarliestRecovery() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	earliest := time.Now().Add(5 * time.Minute)
	for _, until := range h.cooldown {
		if until.Before(earliest) {
			earliest = until
		}
	}
	return earliest
}
