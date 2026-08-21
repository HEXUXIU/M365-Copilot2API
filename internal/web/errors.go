package web

import (
	"context"
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
func isClientCancel(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func upstreamStatus(err error) int {
	if isClientCancel(err) {
		return 499
	}
	if IsRateLimited(err) {
		return http.StatusTooManyRequests
	}
	if IsAuthFailure(err) {
		return http.StatusUnauthorized
	}
	var httpErr *UpstreamHTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status
	}
	return http.StatusBadGateway
}

// writeUpstreamError renders a failed upstream call as an HTTP response,
// surfacing the Retry-After hint for rate limits so clients can back off.
func writeUpstreamError(w http.ResponseWriter, err error) {
	if retry := RetryAfterSeconds(err); retry > 0 {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retry))
	}
	status := upstreamStatus(err)
	if status == http.StatusTooManyRequests {
		if w.Header().Get("Retry-After") == "" {
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rateLimitCooldown.Seconds())))
		}
		writeOpenAIError(w, status, "rate_limit_error", "", "upstream is rate limiting; try again shortly")
		return
	}
	if IsEmptyCompletion(err) {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "", "upstream returned empty completion; the requested model may be unavailable for this tenant")
		return
	}
	writeOpenAIError(w, status, "upstream_error", "", upstreamError(err))
}
