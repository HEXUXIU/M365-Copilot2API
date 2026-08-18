package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"m365-copilot2api/internal/auth"
)

func logOAuthError(stage string, err error) {
	var oauthErr *auth.OAuthError
	if errors.As(err, &oauthErr) {
		log.Printf("oauth_error stage=%s error=%q aadsts=%q http_status=%d correlation_id=%q trace_id=%q", stage, oauthErr.Code, oauthErr.AADSTS, oauthErr.HTTPStatus, oauthErr.CorrelationID, oauthErr.TraceID)
		return
	}
	log.Printf("oauth_error stage=%s error=%q", stage, "request_failed")
}

// upstreamError keeps transport details, including URLs and credentials, out
// of client-visible responses while retaining a server-side diagnostic.
func upstreamError(err error) string {
	if err == nil {
		return "upstream request failed"
	}
	log.Printf("upstream request failed: %v", err)
	return "upstream request failed"
}

// upstreamStatus maps a failed upstream call to the client-visible HTTP status:
// rate limits stay 429 (with Retry-After when known), auth failures become 401,
// everything else is 502. Unknown upstream failures must never leak internals.
func upstreamStatus(err error) int {
	if IsPermissionDenied(err) {
		return http.StatusForbidden
	}
	if IsAuthFailure(err) {
		return http.StatusUnauthorized
	}
	if IsRateLimited(err) {
		return http.StatusTooManyRequests
	}
	if IsInsufficientQuota(err) {
		return http.StatusTooManyRequests
	}
	if IsServerUnavailable(err) {
		return http.StatusServiceUnavailable
	}
	if IsTimeout(err) {
		return http.StatusGatewayTimeout
	}
	return http.StatusBadGateway
}

func writeUpstreamError(w http.ResponseWriter, err error) {
	if retry := RetryAfterSeconds(err); retry > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
	}
	status := upstreamStatus(err)
	if status == http.StatusTooManyRequests {
		if w.Header().Get("Retry-After") == "" {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rateLimitCooldown.Seconds())))
		}
		writeOpenAIError(w, status, "rate_limit_error", "rate_limit_exceeded", "rate limited by upstream; retry after the indicated cool-down period")
		return
	}
	if IsInsufficientQuota(err) {
		writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_error", "insufficient_quota", "account quota exhausted; check M365 subscription and Copilot license")
		return
	}
	if IsPermissionDenied(err) {
		writeOpenAIError(w, http.StatusForbidden, "authentication_error", "insufficient_permissions", "account not authorized for Copilot; check M365 subscription and Copilot license assignment")
		return
	}
	if IsServerUnavailable(err) {
		writeOpenAIError(w, http.StatusServiceUnavailable, "server_error", "service_unavailable", "upstream service temporarily unavailable; retry later")
		return
	}
	if IsTimeout(err) {
		writeOpenAIError(w, http.StatusGatewayTimeout, "server_error", "timeout", "upstream request timed out")
		return
	}
	if IsEmptyCompletion(err) {
		writeOpenAIError(w, http.StatusBadGateway, "server_error", "empty_completion", "empty completion; model may be unavailable for this tenant")
		return
	}
	if IsContentBlocked(err) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "content_policy_violation", "content policy blocked this request")
		return
	}
	writeOpenAIError(w, status, "server_error", "bad_gateway", upstreamError(err))
}
