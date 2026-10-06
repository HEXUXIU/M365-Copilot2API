package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"m365-copilot2api/internal/chathub"
)

type ErrorCategory string

const (
	CategoryQuota429           ErrorCategory = "QUOTA_429"
	CategoryOverload503        ErrorCategory = "OVERLOAD_503"
	CategoryAuthExpired401     ErrorCategory = "AUTH_EXPIRED_401"
	CategoryForbidden403       ErrorCategory = "FORBIDDEN_403"
	CategoryRetryable422       ErrorCategory = "RETRYABLE_422"
	CategoryUserBanned         ErrorCategory = "USER_BANNED"
	CategoryUserThrottled      ErrorCategory = "USER_THROTTLED"
	CategoryInsufficientTokens ErrorCategory = "INSUFFICIENT_TOKENS"
	CategoryDesignerDisabled   ErrorCategory = "DESIGNER_DISABLED"
	CategorySOCKS5             ErrorCategory = "SOCKS5"
	CategoryDNS                ErrorCategory = "DNS"
	CategoryTCP                ErrorCategory = "TCP"
	CategoryTLS                ErrorCategory = "TLS"
	CategoryWSHandshake        ErrorCategory = "WS_HANDSHAKE"
	CategoryWSReadTimeout      ErrorCategory = "WS_READ_TIMEOUT"
	CategoryWSWriteTimeout     ErrorCategory = "WS_WRITE_TIMEOUT"
	CategoryUpstreamStructured ErrorCategory = "UPSTREAM_STRUCTURED"
	CategoryClientCanceled     ErrorCategory = "CLIENT_CANCELED"
	CategoryGlobalUnavailable  ErrorCategory = "GLOBAL_UNAVAILABLE"
	CategoryUnknown            ErrorCategory = "UNKNOWN"
)

type UpstreamHTTPError struct {
	Status     int
	RetryAfter int
	Body       string
	ErrorCode  string
}

func (e *UpstreamHTTPError) Error() string {
	return fmt.Sprintf("upstream http %d", e.Status)
}

func ClassifyError(err error) ErrorCategory {
	if err == nil {
		return CategoryUnknown
	}
	if errors.Is(err, context.Canceled) {
		return CategoryClientCanceled
	}
	if errors.Is(err, chathub.ErrRateLimitNotice) {
		return CategoryQuota429
	}
	if errors.Is(err, chathub.ErrEmptyCompletion) || errors.Is(err, chathub.ErrOffensiveContent) || errors.Is(err, chathub.ErrImageLimit) {
		return CategoryUpstreamStructured
	}
	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) {
		if httpErr.ErrorCode != "" {
			switch httpErr.ErrorCode {
			case "ErrorUserBanned":
				return CategoryUserBanned
			case "ErrorUserThrottled":
				return CategoryUserThrottled
			case "InsufficientTokens":
				return CategoryInsufficientTokens
			case "ErrorDisallowedAADUser":
				return CategoryDesignerDisabled
			}
		}
		switch httpErr.Status {
		case 429:
			return CategoryQuota429
		case 503:
			return CategoryOverload503
		case 401:
			return CategoryAuthExpired401
		case 403:
			return CategoryForbidden403
		case 422:
			return CategoryRetryable422
		}
		if strings.Contains(strings.ToLower(httpErr.Body), "limited") {
			return CategoryQuota429
		}
	}
	var dialErr *chathub.DialError
	if errors.As(err, &dialErr) {
		if dialErr.Kind != "" {
			switch dialErr.Kind {
			case "QUOTA_429":
				return CategoryQuota429
			case "OVERLOAD_503":
				return CategoryOverload503
			case "AUTH_EXPIRED_401":
				return CategoryAuthExpired401
			case "FORBIDDEN_403":
				return CategoryForbidden403
			case "SOCKS5":
				return CategorySOCKS5
			case "DNS":
				return CategoryDNS
			case "TCP":
				return CategoryTCP
			case "TLS":
				return CategoryTLS
			case "WS_HANDSHAKE":
				return CategoryWSHandshake
			case "WS_READ_TIMEOUT":
				return CategoryWSReadTimeout
			case "WS_WRITE_TIMEOUT":
				// Same transport class and failover behaviour as a read timeout,
				// but kept as its own kind so the reported reason is accurate.
				return CategoryWSWriteTimeout
			case "CLIENT_CANCELED":
				return CategoryClientCanceled
			}
		}
		switch dialErr.Status {
		case 429:
			return CategoryQuota429
		case 503:
			return CategoryOverload503
		case 401:
			return CategoryAuthExpired401
		case 403:
			return CategoryForbidden403
		case 422:
			return CategoryRetryable422
		}
		if dialErr.Kind != "" {
			return ErrorCategory(dialErr.Kind)
		}
		if dialErr.Status == 0 {
			msg := strings.ToLower(dialErr.Error())
			if strings.Contains(msg, "socks") {
				return CategorySOCKS5
			}
			if strings.Contains(msg, "no such host") || strings.Contains(msg, "dns") {
				return CategoryDNS
			}
			if strings.Contains(msg, "tls") || strings.Contains(msg, "certificate") || strings.Contains(msg, "x509") {
				return CategoryTLS
			}
			if strings.Contains(msg, "handshake") {
				return CategoryWSHandshake
			}
			if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
				return CategoryWSReadTimeout
			}
			return CategoryTCP
		}
	}
	msg := strings.ToLower(err.Error())
	// A rejected refresh token is an auth failure, not a transient transport
	// fault; it stays until the account is re-authenticated. Match only the
	// canonical OAuth rejection signals so a service-wide error (e.g. a bad
	// client secret, AADSTS7000215) is not mistaken for a per-account issue.
	// Checked before the global circuit so an open circuit cannot mask it.
	if strings.Contains(msg, "invalid_grant") || strings.Contains(msg, "token_expired") {
		return CategoryAuthExpired401
	}
	if globalCircuit != nil && globalCircuit.IsOpen() {
		return CategoryGlobalUnavailable
	}
	switch {
	case strings.Contains(msg, "socks"):
		return CategorySOCKS5
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "name resolution") || (strings.Contains(msg, "dns") && !strings.Contains(msg, "limited")):
		return CategoryDNS
	case strings.Contains(msg, "tls") || strings.Contains(msg, "certificate") || strings.Contains(msg, "x509"):
		return CategoryTLS
	case strings.Contains(msg, "handshake"):
		return CategoryWSHandshake
	case strings.Contains(msg, "ws read") || (strings.Contains(msg, "timeout") && strings.Contains(msg, "read")) || strings.Contains(msg, "deadline exceeded"):
		return CategoryWSReadTimeout
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") || strings.Contains(msg, "network is unreachable"):
		return CategoryTCP
	case strings.Contains(msg, "client canceled") || strings.Contains(msg, "context canceled"):
		return CategoryClientCanceled
	case strings.Contains(msg, "empty completion") || strings.Contains(msg, "offensive") || strings.Contains(msg, "image limit"):
		return CategoryUpstreamStructured
	}
	return CategoryUnknown
}

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
		low := strings.ToLower(httpErr.Body)
		if strings.Contains(low, "limited") || strings.Contains(low, "图像生成功能没有成功") || strings.Contains(low, "metererror") {
			return true
		}
	}
	var dialErr *chathub.DialError
	if errors.As(err, &dialErr) {
		if dialErr.Status == 429 {
			return true
		}
		if dialErr.Kind == "QUOTA_429" {
			return true
		}
	}
	return false
}

func IsOverload(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) && httpErr.Status == 503 {
		return true
	}
	var dialErr *chathub.DialError
	if errors.As(err, &dialErr) && (dialErr.Status == 503 || dialErr.Kind == "OVERLOAD_503") {
		return true
	}
	return ClassifyError(err) == CategoryOverload503
}

func IsTransientTransport(err error) bool {
	if err == nil {
		return false
	}
	switch ClassifyError(err) {
	case CategorySOCKS5, CategoryDNS, CategoryTCP, CategoryTLS, CategoryWSHandshake, CategoryWSReadTimeout, CategoryWSWriteTimeout, CategoryOverload503:
		return true
	}
	return false
}

func IsFailoverRetriable(err error) bool {
	if IsRateLimited(err) || IsOverload(err) || IsAuthFailure(err) || IsTransientTransport(err) {
		return true
	}
	// HAR 实证上游 422 属可重试成员，纳入故障转移白名单。
	return ClassifyError(err) == CategoryRetryable422
}

func IsAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == 401 || httpErr.Status == 403
	}
	var dialErr *chathub.DialError
	if errors.As(err, &dialErr) {
		return dialErr.Status == 401 || dialErr.Status == 403 || dialErr.Kind == "AUTH_EXPIRED_401" || dialErr.Kind == "FORBIDDEN_403"
	}
	return false
}

func IsEmptyCompletion(err error) bool {
	return errors.Is(err, chathub.ErrEmptyCompletion)
}

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

func transientThrottledCooldown() time.Duration {
	n := envIntTransient()
	if n < 5 {
		n = 15
	}
	if n > 600 {
		n = 600
	}
	return time.Duration(n) * time.Second
}

func envIntTransient() int {
	v := 0
	if s := strings.TrimSpace(os.Getenv("M365_TRANSIENT_THROTTLED_COOLDOWN_SECONDS")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			v = n
		}
	}
	if v == 0 {
		v = 15
	}
	return v
}

func CooldownForCategory(cat ErrorCategory, retryAfter int, attempt int) time.Duration {
	switch cat {
	case CategoryQuota429:
		if retryAfter > 0 {
			d := time.Duration(retryAfter) * time.Second
			if d > 30*time.Minute {
				d = 30 * time.Minute
			}
			return d
		}
		if attempt < 1 {
			attempt = 1
		}
		if attempt > 7 {
			attempt = 7
		}
		base := transientThrottledCooldown()
		d := base * time.Duration(1<<(attempt-1))
		if d > 30*time.Minute || d <= 0 {
			d = 30 * time.Minute
		}
		return d
	case CategoryOverload503:
		return 15 * time.Second
	case CategoryAuthExpired401:
		return 2 * time.Minute
	case CategoryForbidden403:
		return 24 * time.Hour
	case CategoryUserBanned:
		return 365 * 24 * time.Hour
	case CategoryUserThrottled:
		return 1 * time.Hour
	case CategoryInsufficientTokens:
		return 24 * time.Hour
	case CategoryDesignerDisabled:
		return 0
	case CategoryRetryable422:
		return 5 * time.Second
	case CategorySOCKS5:
		return 30 * time.Second
	case CategoryDNS:
		return 30 * time.Second
	case CategoryTCP:
		return 15 * time.Second
	case CategoryTLS:
		return 30 * time.Second
	case CategoryWSHandshake:
		return 15 * time.Second
	case CategoryWSReadTimeout, CategoryWSWriteTimeout:
		return 30 * time.Second
	case CategoryUpstreamStructured:
		return 10 * time.Second
	case CategoryClientCanceled:
		return 0
	case CategoryGlobalUnavailable:
		return 15 * time.Second
	default:
		return 15 * time.Second
	}
}

type globalCircuitState struct {
	mu          sync.Mutex
	windowStart time.Time
	total       int
	failures    int
	openUntil   time.Time
}

var globalCircuit = &globalCircuitState{}

func (g *globalCircuitState) IsOpen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.openUntil.IsZero() {
		return false
	}
	if time.Now().Before(g.openUntil) {
		return true
	}
	g.openUntil = time.Time{}
	return false
}

func (g *globalCircuitState) OpenUntil() time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.openUntil
}

func (g *globalCircuitState) State() string {
	if g.IsOpen() {
		return "open"
	}
	return "closed"
}

func (g *globalCircuitState) Record(err error) {
	if err == nil {
		g.mu.Lock()
		now := time.Now()
		if g.windowStart.IsZero() || now.Sub(g.windowStart) > 30*time.Second {
			g.windowStart = now
			g.total = 0
			g.failures = 0
		}
		g.total++
		if g.total > 1000 {
			g.windowStart = now
			g.total = 1
			g.failures = 0
		}
		if g.total >= 10 && g.failures*2 >= g.total {
			g.openUntil = now.Add(30 * time.Second)
		}
		g.mu.Unlock()
		return
	}
	cat := ClassifyError(err)
	switch cat {
	case CategorySOCKS5, CategoryDNS, CategoryTCP, CategoryTLS, CategoryWSHandshake, CategoryWSReadTimeout, CategoryWSWriteTimeout:
	default:
		if cat == CategoryClientCanceled || cat == CategoryGlobalUnavailable {
			return
		}
		// Only shared transport / infrastructure failures contribute to the global circuit.
		// Quota, overload, auth, and request-specific errors are per-account.
		return
	}
	g.mu.Lock()
	now := time.Now()
	if g.windowStart.IsZero() || now.Sub(g.windowStart) > 30*time.Second {
		g.windowStart = now
		g.total = 0
		g.failures = 0
	}
	g.total++
	g.failures++
	if g.total >= 10 && g.failures*2 >= g.total {
		g.openUntil = now.Add(30 * time.Second)
	}
	if g.total > 1000 {
		g.windowStart = now
		g.total = 1
		g.failures = 1
	}
	g.mu.Unlock()
}

func GlobalCircuitIsOpen() bool         { return globalCircuit.IsOpen() }
func GlobalCircuitState() string        { return globalCircuit.State() }
func GlobalCircuitOpenUntil() time.Time { return globalCircuit.OpenUntil() }
func GlobalCircuitRecord(err error)     { globalCircuit.Record(err) }
func ResetGlobalCircuit() {
	globalCircuit.mu.Lock()
	globalCircuit.windowStart = time.Time{}
	globalCircuit.total = 0
	globalCircuit.failures = 0
	globalCircuit.openUntil = time.Time{}
	globalCircuit.mu.Unlock()
}

type accountHealth struct {
	mu                     sync.Mutex
	cooldown               map[string]time.Time
	authFail               map[string]bool
	limited                map[string]bool
	calls                  map[string]uint64
	imageLimited           map[string]bool
	imageLimitUntil        map[string]time.Time
	imageGenCooldownUntil  map[string]time.Time
	imageGenSystemCooldown map[string]time.Time
	lastThrottling         map[string]any
	authFailReason         map[string]string
	quotaAttempts          map[string]int
	lastCategory           ErrorCategory
	lastCategoryAt         time.Time
}

func newAccountHealth() *accountHealth {
	ResetGlobalCircuit()
	return &accountHealth{
		cooldown:               map[string]time.Time{},
		authFail:               map[string]bool{},
		limited:                map[string]bool{},
		calls:                  map[string]uint64{},
		imageLimited:           map[string]bool{},
		imageLimitUntil:        map[string]time.Time{},
		imageGenCooldownUntil:  map[string]time.Time{},
		imageGenSystemCooldown: map[string]time.Time{},
		lastThrottling:         map[string]any{},
		authFailReason:         map[string]string{},
		quotaAttempts:          map[string]int{},
	}
}

// LastCategory reports the most recent failure category recorded by
// MarkFailure. resolveAccount uses it to distinguish local network failures
// from upstream rate limiting (issue #79).
func (h *accountHealth) LastCategory() (ErrorCategory, time.Time) {
	if h == nil {
		return CategoryUnknown, time.Time{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastCategory, h.lastCategoryAt
}

// IsTransportCategory reports whether the category is a local/transport
// failure rather than an upstream quota or auth rejection. Upstream overload
// (503) is deliberately excluded: it is a server-side capacity signal, not a
// local connectivity fault, so it must not be reported as network_error.
func IsTransportCategory(cat ErrorCategory) bool {
	switch cat {
	case CategorySOCKS5, CategoryDNS, CategoryTCP, CategoryTLS, CategoryWSHandshake, CategoryWSReadTimeout, CategoryWSWriteTimeout:
		return true
	}
	return false
}

func (h *accountHealth) cleanupExpiredCooldownLocked(accountID string) {
	until, ok := h.cooldown[accountID]
	if !ok || time.Now().Before(until) {
		return
	}
	wasRateLimited := h.limited[accountID]
	delete(h.cooldown, accountID)
	delete(h.limited, accountID)
	delete(h.authFail, accountID)
	delete(h.authFailReason, accountID)
	delete(h.imageLimited, accountID)
	if wasRateLimited {
		delete(h.calls, accountID)
	}
	delete(h.quotaAttempts, accountID)
	if t, ok := h.imageGenCooldownUntil[accountID]; ok && time.Now().After(t) {
		delete(h.imageGenCooldownUntil, accountID)
	}
	if t, ok := h.imageGenSystemCooldown[accountID]; ok && time.Now().After(t) {
		delete(h.imageGenSystemCooldown, accountID)
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

func (h *accountHealth) MarkImageLimited(accountID string) {
	if h == nil || accountID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.imageLimited[accountID] = true
	h.imageLimitUntil[accountID] = time.Now().Add(24 * time.Hour)
	h.cooldown[accountID] = time.Now().Add(24 * time.Hour)
}

func (h *accountHealth) ImageLimited(accountID string) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.imageLimited[accountID] {
		if until, ok := h.imageLimitUntil[accountID]; ok && time.Now().After(until) {
			delete(h.imageLimited, accountID)
			delete(h.imageLimitUntil, accountID)
			delete(h.imageLimitUntil, accountID)
		}
	}
	h.cleanupExpiredCooldownLocked(accountID)
	return h.imageLimited[accountID]
}

func (h *accountHealth) MarkImageGenTokensThrottled(accountID string) {
	if h == nil || accountID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UTC()
	tomorrow := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	h.imageGenCooldownUntil[accountID] = tomorrow
}

func (h *accountHealth) MarkImageGenSystemThrottled(accountID string) {
	if h == nil || accountID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.imageGenSystemCooldown[accountID] = time.Now().Add(30 * time.Minute)
}

func (h *accountHealth) ImageGenAvailable(accountID string) bool {
	if h == nil {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if t, ok := h.imageGenCooldownUntil[accountID]; ok && time.Now().Before(t) {
		return false
	}
	if t, ok := h.imageGenSystemCooldown[accountID]; ok && time.Now().Before(t) {
		return false
	}
	return true
}

func (h *accountHealth) UpdateThrottling(accountID string, data any) {
	if h == nil || accountID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastThrottling[accountID] = data
}

func (h *accountHealth) GetThrottling(accountID string) any {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	v := h.lastThrottling[accountID]
	h.mu.Unlock()
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var copy any
	if json.Unmarshal(b, &copy) != nil {
		return v
	}
	return copy
}

func (h *accountHealth) AuthFailReason(accountID string) string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.authFailReason[accountID]
}

func (h *accountHealth) MarkFailure(accountID string, err error, window time.Duration) {
	if window <= 0 {
		window = 60 * time.Second
	}
	cat := ClassifyError(err)
	GlobalCircuitRecord(err)
	if cat == CategoryClientCanceled {
		return
	}
	h.mu.Lock()
	h.lastCategory = cat
	h.lastCategoryAt = time.Now()
	h.mu.Unlock()
	if cat == CategoryGlobalUnavailable {
		h.mu.Lock()
		h.cooldown[accountID] = time.Now().Add(CooldownForCategory(cat, 0, 1))
		h.mu.Unlock()
		return
	}
	// 429 keeps the upstream-advised window: RetryAfter wins when present,
	// otherwise the backoff grows with consecutive attempts. MarkSuccess clears
	// quotaAttempts, so a recovered account returns to the short base window.
	if cat == CategoryQuota429 {
		h.mu.Lock()
		h.quotaAttempts[accountID] = h.quotaAttempts[accountID] + 1
		h.limited[accountID] = true
		delete(h.authFail, accountID)
		delete(h.authFailReason, accountID)
		h.cooldown[accountID] = time.Now().Add(CooldownForCategory(cat, RetryAfterSeconds(err), h.quotaAttempts[accountID]))
		h.mu.Unlock()
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	switch cat {
	case CategoryAuthExpired401:
		cooldown := CooldownForCategory(cat, 0, 1)
		h.cooldown[accountID] = time.Now().Add(cooldown)
		h.authFail[accountID] = true
		delete(h.limited, accountID)
		var httpErr *UpstreamHTTPError
		if errors.As(err, &httpErr) {
			h.authFailReason[accountID] = fmt.Sprintf("%d", httpErr.Status)
		} else {
			var dialErr *chathub.DialError
			if errors.As(err, &dialErr) {
				if dialErr.Status != 0 {
					h.authFailReason[accountID] = fmt.Sprintf("%d", dialErr.Status)
				} else {
					h.authFailReason[accountID] = "401"
				}
			} else {
				h.authFailReason[accountID] = "401"
			}
		}
		return
	case CategoryForbidden403:
		var httpErr403 *UpstreamHTTPError
		if errors.As(err, &httpErr403) && httpErr403.ErrorCode == "ErrorDisallowedAADUser" {
			return
		}
		var dialErr403 *chathub.DialError
		if errors.As(err, &dialErr403) && dialErr403.Kind == "DESIGNER_DISABLED" {
			return
		}
		h.cooldown[accountID] = time.Now().Add(CooldownForCategory(cat, 0, 1))
		h.authFail[accountID] = true
		delete(h.limited, accountID)
		h.authFailReason[accountID] = "403"
		if httpErr403 != nil {
			h.authFailReason[accountID] = fmt.Sprintf("%d", httpErr403.Status)
		} else {
			if dialErr403 != nil && dialErr403.Status != 0 {
				h.authFailReason[accountID] = fmt.Sprintf("%d", dialErr403.Status)
			}
		}
		return
	case CategoryOverload503:
		delete(h.authFail, accountID)
		delete(h.authFailReason, accountID)
		delete(h.limited, accountID)
		h.cooldown[accountID] = time.Now().Add(CooldownForCategory(cat, RetryAfterSeconds(err), 1))
		return
	case CategorySOCKS5, CategoryDNS, CategoryTCP, CategoryTLS, CategoryWSHandshake, CategoryWSReadTimeout, CategoryWSWriteTimeout:
		delete(h.authFail, accountID)
		delete(h.authFailReason, accountID)
		delete(h.limited, accountID)
		cd := CooldownForCategory(cat, 0, 1)
		h.cooldown[accountID] = time.Now().Add(cd)
		return
	case CategoryUpstreamStructured:
		cd := CooldownForCategory(cat, 0, 1)
		h.cooldown[accountID] = time.Now().Add(cd)
		return
	case CategoryUserBanned:
		h.authFail[accountID] = true
		h.authFailReason[accountID] = "banned"
		h.cooldown[accountID] = time.Now().Add(CooldownForCategory(cat, 0, 1))
		return
	case CategoryUserThrottled:
		h.authFail[accountID] = true
		h.authFailReason[accountID] = "throttled"
		h.cooldown[accountID] = time.Now().Add(CooldownForCategory(cat, 0, 1))
		return
	case CategoryInsufficientTokens:
		delete(h.authFail, accountID)
		delete(h.authFailReason, accountID)
		h.limited[accountID] = true
		h.cooldown[accountID] = time.Now().Add(CooldownForCategory(cat, 0, 1))
		return
	case CategoryRetryable422:
		delete(h.authFail, accountID)
		delete(h.authFailReason, accountID)
		h.cooldown[accountID] = time.Now().Add(CooldownForCategory(cat, 0, 1))
		return
	default:
		delete(h.authFail, accountID)
		delete(h.authFailReason, accountID)
		cd := window
		if cd > 30*time.Second {
			cd = 30 * time.Second
		}
		h.cooldown[accountID] = time.Now().Add(cd)
	}
}

func (h *accountHealth) MarkSuccess(accountID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	imageLimited := h.imageLimited[accountID]
	imageLimitUntil := h.imageLimitUntil[accountID]
	imageGenCooldown := h.imageGenCooldownUntil[accountID]
	imageGenSysCooldown := h.imageGenSystemCooldown[accountID]
	delete(h.cooldown, accountID)
	delete(h.authFail, accountID)
	delete(h.limited, accountID)
	delete(h.authFailReason, accountID)
	delete(h.quotaAttempts, accountID)
	GlobalCircuitRecord(nil)
	if imageLimited && time.Now().Before(imageLimitUntil) {
		h.imageLimited[accountID] = true
		h.imageLimitUntil[accountID] = imageLimitUntil
		h.cooldown[accountID] = imageLimitUntil
	} else {
		delete(h.imageLimited, accountID)
		delete(h.imageLimitUntil, accountID)
	}
	if !imageGenCooldown.IsZero() && time.Now().Before(imageGenCooldown) {
		h.imageGenCooldownUntil[accountID] = imageGenCooldown
	} else {
		delete(h.imageGenCooldownUntil, accountID)
	}
	if !imageGenSysCooldown.IsZero() && time.Now().Before(imageGenSysCooldown) {
		h.imageGenSystemCooldown[accountID] = imageGenSysCooldown
	} else {
		delete(h.imageGenSystemCooldown, accountID)
	}
}

func (h *accountHealth) Available(accountID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanupExpiredCooldownLocked(accountID)
	if GlobalCircuitIsOpen() {
		return false
	}
	if h.authFail[accountID] {
		return false
	}
	if until, ok := h.cooldown[accountID]; ok && time.Now().Before(until) {
		return false
	}
	return true
}

// QuotaDetail reports how many consecutive 429s the account has accumulated
// and until when it is cooling down. The pool keeper reads it to stop warming
// freshly-throttled accounts sooner than the generic Available() check, which
// only hides them after their cooldown is registered.
func (h *accountHealth) QuotaDetail(accountID string) (attempts int, limited bool, until time.Time) {
	if h == nil || accountID == "" {
		return 0, false, time.Time{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cleanupExpiredCooldownLocked(accountID)
	return h.quotaAttempts[accountID], h.limited[accountID], h.cooldown[accountID]
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

func (h *accountHealth) Snapshot() map[string]map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]map[string]any)
	ids := make(map[string]bool)
	for id := range h.cooldown {
		ids[id] = true
	}
	for id := range h.authFail {
		ids[id] = true
	}
	for id := range h.limited {
		ids[id] = true
	}
	for id := range h.imageLimited {
		ids[id] = true
	}
	for id := range h.lastThrottling {
		ids[id] = true
	}
	for id := range h.calls {
		ids[id] = true
	}
	for id := range h.quotaAttempts {
		ids[id] = true
	}
	for id := range h.imageGenCooldownUntil {
		ids[id] = true
	}
	for id := range h.imageGenSystemCooldown {
		ids[id] = true
	}
	for id := range ids {
		h.cleanupExpiredCooldownLocked(id)
		m := map[string]any{}
		if until, ok := h.cooldown[id]; ok {
			m["available"] = time.Now().After(until)
			m["cooldownUntil"] = until
			if catAttempts, ok := h.quotaAttempts[id]; ok && catAttempts > 0 {
				m["quotaAttempts"] = catAttempts
			}
		} else {
			m["available"] = true
		}
		if h.authFail[id] {
			m["authFailed"] = true
		}
		if h.limited[id] {
			m["limited"] = true
		}
		if h.imageLimited[id] {
			m["imageLimited"] = true
		}
		if t, ok := h.imageGenCooldownUntil[id]; ok && !t.IsZero() {
			if time.Now().Before(t) {
				m["imageGenCooldownUntil"] = t
			}
		}
		if t, ok := h.imageGenSystemCooldown[id]; ok && !t.IsZero() {
			if time.Now().Before(t) {
				m["imageGenSystemCooldown"] = t
			}
		}
		if t := h.lastThrottling[id]; t != nil {
			m["throttling"] = t
		}
		if r := h.authFailReason[id]; r != "" {
			m["authFailReason"] = r
		}
		if c := h.calls[id]; c > 0 {
			m["calls"] = c
		}
		if GlobalCircuitIsOpen() {
			m["globalCircuit"] = "open"
		}
		out[id] = m
	}
	if GlobalCircuitIsOpen() {
		out["_global"] = map[string]any{"globalCircuit": "open", "openUntil": GlobalCircuitOpenUntil()}
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
	h.imageLimited = map[string]bool{}
	h.imageLimitUntil = map[string]time.Time{}
	h.imageGenCooldownUntil = map[string]time.Time{}
	h.imageGenSystemCooldown = map[string]time.Time{}
	h.lastThrottling = map[string]any{}
	h.authFailReason = map[string]string{}
	h.quotaAttempts = map[string]int{}
	ResetGlobalCircuit()
}

func (h *accountHealth) EarliestRecovery() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.cooldown) == 0 {
		return time.Time{}
	}
	var earliest time.Time
	first := true
	for _, until := range h.cooldown {
		if first || until.Before(earliest) {
			earliest = until
			first = false
		}
	}
	if GlobalCircuitIsOpen() {
		if gu := GlobalCircuitOpenUntil(); !gu.IsZero() && (earliest.IsZero() || gu.Before(earliest)) {
			earliest = gu
		}
	}
	return earliest
}

// AnyRateLimited reports whether any cooling account was sidelined by an
// upstream quota signal. It separates a quota exhaustion from a transport
// outage when every account is cooling, so the error we return names the
// reason we actually observed instead of guessing.
func (h *accountHealth) AnyRateLimited() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id := range h.cooldown {
		if h.limited[id] {
			return true
		}
	}
	return false
}
