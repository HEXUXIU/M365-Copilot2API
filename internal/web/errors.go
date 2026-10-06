package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"m365-copilot2api/internal/auth"
	"m365-copilot2api/internal/chathub"
)

var ErrOffensiveContent = errors.New("upstream content policy flagged as offensive")

func logOAuthError(stage string, err error) {
	var oauthErr *auth.OAuthError
	if errors.As(err, &oauthErr) {
		log.Printf("oauth_error stage=%s error=%q aadsts=%q http_status=%d correlation_id=%q trace_id=%q", stage, oauthErr.Code, oauthErr.AADSTS, oauthErr.HTTPStatus, oauthErr.CorrelationID, oauthErr.TraceID)
		return
	}
	log.Printf("oauth_error stage=%s error=%q", stage, "request_failed")
}

// upstreamError keeps transport details, including hosts, IPs and credentials,
// out of client-visible responses, but still names the reason we actually know
// about. A caller that cannot tell a timeout from a rate limit cannot decide
// whether to retry, so the classification is reported even though the address
// that failed is not.
func upstreamError(err error) string {
	if err == nil {
		return "upstream request failed"
	}
	log.Printf("upstream request failed: %v", err)

	var dialErr *chathub.DialError
	if errors.As(err, &dialErr) && dialErr.Kind != "" {
		switch dialErr.Kind {
		case "WS_READ_TIMEOUT":
			return "upstream timed out waiting for the response stream (WS_READ_TIMEOUT)"
		case "WS_WRITE_TIMEOUT":
			return "upstream timed out while sending the request (WS_WRITE_TIMEOUT)"
		case "WS_HANDSHAKE":
			return "upstream websocket handshake failed (WS_HANDSHAKE)"
		case "CLIENT_CANCELED":
			return "request canceled before upstream responded"
		}
		return "upstream connection failed (" + dialErr.Kind + ")"
	}

	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) {
		if body := strings.TrimSpace(httpErr.Body); body != "" {
			return body
		}
	}

	switch ClassifyError(err) {
	case CategoryDNS:
		return "upstream DNS resolution failed"
	case CategoryTCP:
		return "upstream connection reset or refused (TCP)"
	case CategoryTLS:
		return "upstream TLS handshake failed"
	case CategorySOCKS5:
		return "outbound proxy (SOCKS5) connection failed"
	case CategoryWSReadTimeout:
		return "upstream timed out waiting for the response stream (WS_READ_TIMEOUT)"
	case CategoryWSWriteTimeout:
		return "upstream timed out while sending the request (WS_WRITE_TIMEOUT)"
	case CategoryWSHandshake:
		return "upstream websocket handshake failed (WS_HANDSHAKE)"
	case CategoryQuota429:
		return "upstream is rate limiting this account"
	case CategoryRetryable422:
		return "upstream rejected the request as retryable"
	case CategoryOverload503:
		return "upstream reported overload (503)"
	case CategoryGlobalUnavailable:
		return "upstream service is temporarily unavailable"
	case CategoryAuthExpired401:
		return "upstream rejected the stored credentials"
	case CategoryForbidden403:
		return "upstream refused this account for the request"
	}
	return "upstream request failed"
}

// upstreamStatus maps a failed upstream call to the client-visible HTTP status:
// rate limits stay 429 (with Retry-After when known), auth failures become 401,
// everything else is 502. Unknown upstream failures must never leak internals.
func upstreamStatus(err error) int {
	if errors.Is(err, chathub.ErrOffensiveContent) {
		return http.StatusServiceUnavailable
	}
	if errors.Is(err, chathub.ErrImageLimit) {
		return http.StatusTooManyRequests
	}
	// Local/transport failures reported as network_error must not be presented
	// as upstream rate limiting (issue #79).
	var netErr *UpstreamHTTPError
	if errors.As(err, &netErr) && netErr.ErrorCode == "network_error" {
		return http.StatusServiceUnavailable
	}
	if IsRateLimited(err) {
		return http.StatusTooManyRequests
	}
	if IsAuthFailure(err) {
		return http.StatusUnauthorized
	}
	cat := ClassifyError(err)
	switch cat {
	case CategoryUserBanned:
		return http.StatusForbidden
	case CategoryUserThrottled:
		return http.StatusTooManyRequests
	case CategoryInsufficientTokens:
		return http.StatusTooManyRequests
	case CategoryRetryable422:
		return http.StatusUnprocessableEntity
	}
	return http.StatusBadGateway
}

func applyM365Headers(w http.ResponseWriter, err error, accountID string) {
	cat := ClassifyError(err)
	if accountID != "" {
		w.Header().Set("X-M365-Account-Id", accountID)
	} else {
		w.Header().Set("X-M365-Account-Id", "")
	}
	w.Header().Set("X-M365-Proxy-Error", string(cat))
	if GlobalCircuitIsOpen() {
		remaining := int(time.Until(GlobalCircuitOpenUntil()).Seconds())
		if remaining < 0 {
			remaining = 0
		}
		w.Header().Set("X-M365-Global-Circuit", fmt.Sprintf("open; retry-after=%d", remaining))
	} else {
		w.Header().Set("X-M365-Global-Circuit", "closed")
	}
	if retry := RetryAfterSeconds(err); retry > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
		w.Header().Set("X-M365-Retry-After", fmt.Sprintf("%d", retry))
		w.Header().Set("X-M365-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(time.Duration(retry)*time.Second).Unix()))
	} else {
		switch cat {
		case CategoryQuota429:
			w.Header().Set("X-M365-Retry-After", "30")
			w.Header().Set("X-M365-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(30*time.Second).Unix()))
		case CategoryOverload503:
			w.Header().Set("X-M365-Retry-After", "15")
			w.Header().Set("X-M365-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(15*time.Second).Unix()))
		case CategoryAuthExpired401:
			w.Header().Set("X-M365-Retry-After", "120")
			w.Header().Set("X-M365-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(2*time.Minute).Unix()))
		case CategoryForbidden403:
			w.Header().Set("X-M365-Retry-After", "86400")
			w.Header().Set("X-M365-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(24*time.Hour).Unix()))
		}
	}
	if IsRateLimited(err) {
		w.Header().Set("X-M365-RateLimit-Remaining", "0")
	} else {
		w.Header().Set("X-M365-RateLimit-Remaining", "1")
	}
}

func writeUpstreamErrorWithAccount(w http.ResponseWriter, err error, accountID string) {
	applyM365Headers(w, err, accountID)
	if retry := RetryAfterSeconds(err); retry > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
	}
	status := upstreamStatus(err)
	if status == http.StatusTooManyRequests {
		if w.Header().Get("Retry-After") == "" {
			w.Header().Set("Retry-After", "30")
		}
		if w.Header().Get("X-M365-Retry-After") == "" {
			w.Header().Set("X-M365-Retry-After", w.Header().Get("Retry-After"))
		}
		if errors.Is(err, chathub.ErrImageLimit) {
			writeOpenAIError(w, status, "image_limit_error", "image generation daily limit reached; try again tomorrow")
			return
		}
		writeOpenAIError(w, status, "rate_limit_error", "upstream is rate limiting; try again shortly")
		return
	}
	if IsEmptyCompletion(err) {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "upstream returned empty completion; the requested model may be unavailable for this tenant")
		return
	}
	if errors.Is(err, chathub.ErrOffensiveContent) {
		writeOpenAIError(w, http.StatusServiceUnavailable, "upstream_content_blocked", "M365 content policy blocked this request; try again or switch account")
		return
	}
	// We only rewrite what we actually understand. For anything else, pass the
	// upstream status and body through unchanged so the caller sees the real
	// reason instead of a fabricated diagnosis.
	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) && httpErr.Status > 0 {
		code := "upstream_error"
		if httpErr.ErrorCode != "" {
			code = httpErr.ErrorCode
		}
		msg := strings.TrimSpace(httpErr.Body)
		if msg == "" {
			msg = httpErr.Error()
		}
		writeOpenAIError(w, httpErr.Status, code, msg)
		return
	}
	writeOpenAIError(w, status, "upstream_error", upstreamError(err))
}

func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	cat := ClassifyError(err)
	switch cat {
	case CategoryQuota429, CategoryOverload503, CategoryRetryable422,
		CategorySOCKS5, CategoryDNS, CategoryTCP, CategoryTLS,
		CategoryWSHandshake, CategoryWSReadTimeout, CategoryWSWriteTimeout, CategoryUpstreamStructured,
		CategoryGlobalUnavailable:
		return true
	case CategoryForbidden403, CategoryAuthExpired401,
		CategoryUserBanned, CategoryClientCanceled:
		return false
	default:
		return false
	}
}

func ClassifyErrorCode(code string) ErrorCategory {
	switch code {
	case "ErrorUserBanned":
		return CategoryUserBanned
	case "ErrorUserThrottled":
		return CategoryUserThrottled
	case "InsufficientTokens":
		return CategoryInsufficientTokens
	case "ErrorDisallowedAADUser":
		return CategoryDesignerDisabled
	default:
		return CategoryUnknown
	}
}

// writeUpstreamError renders a failed upstream call as an HTTP response,
// surfacing the Retry-After hint for rate limits so clients can back off.
func writeUpstreamError(w http.ResponseWriter, err error) {
	applyM365Headers(w, err, "")
	if retry := RetryAfterSeconds(err); retry > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
	}
	status := upstreamStatus(err)
	if status == http.StatusTooManyRequests {
		if w.Header().Get("Retry-After") == "" {
			w.Header().Set("Retry-After", "30")
		}
		if w.Header().Get("X-M365-Retry-After") == "" {
			w.Header().Set("X-M365-Retry-After", w.Header().Get("Retry-After"))
		}
		if errors.Is(err, chathub.ErrImageLimit) {
			writeOpenAIError(w, status, "image_limit_error", "image generation daily limit reached; try again tomorrow")
			return
		}
		writeOpenAIError(w, status, "rate_limit_error", "upstream is rate limiting; try again shortly")
		return
	}
	if IsEmptyCompletion(err) {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "upstream returned empty completion; the requested model may be unavailable for this tenant")
		return
	}
	if errors.Is(err, chathub.ErrOffensiveContent) {
		writeOpenAIError(w, http.StatusServiceUnavailable, "upstream_content_blocked", "M365 content policy blocked this request; try again or switch account")
		return
	}
	var netErr *UpstreamHTTPError
	if errors.As(err, &netErr) && netErr.ErrorCode == "network_error" {
		writeOpenAIError(w, http.StatusServiceUnavailable, "network_error", netErr.Body)
		return
	}
	writeOpenAIError(w, status, "upstream_error", upstreamError(err))
}
