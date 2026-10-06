package web

import (
	"errors"
	"net/http"

	"m365-copilot2api/internal/chathub"
)

// streamErrorCode maps an upstream failure to the OpenAI-compatible error code
// that actually describes it.
//
// Streaming responses start with HTTP 200 before the first token, so a mid-stream
// failure can only be reported inside the SSE body. Clients that follow the spec
// strictly ignore unknown error codes and treat the turn as a normal, empty
// completion, which silently loses the request. The previous code hardcoded
// "rate_limit" for every failure, so a write timeout or a protocol error was
// indistinguishable from genuine throttling.
func streamErrorCode(err error) string {
	switch {
	case errors.Is(err, chathub.ErrRateLimitNotice):
		return "rate_limit_exceeded"
	case errors.Is(err, chathub.ErrOffensiveContent):
		return "content_policy_violation"
	case errors.Is(err, chathub.ErrImageLimit):
		return "image_limit_exceeded"
	case errors.Is(err, chathub.ErrEmptyCompletion):
		return "upstream_empty_completion"
	case errors.Is(err, chathub.ErrFirstTokenTimeout):
		return "upstream_timeout"
	case IsRateLimited(err):
		return "rate_limit_exceeded"
	}
	switch ClassifyError(err) {
	case CategoryQuota429:
		return "rate_limit_exceeded"
	case CategoryOverload503:
		return "upstream_overloaded"
	case CategoryWSReadTimeout, CategoryWSWriteTimeout:
		return "upstream_timeout"
	case CategoryWSHandshake:
		return "upstream_connection_failed"
	case CategoryDNS, CategoryTCP, CategoryTLS, CategorySOCKS5:
		return "upstream_connection_failed"
	case CategoryAuthExpired401:
		return "upstream_auth_expired"
	case CategoryForbidden403:
		return "upstream_forbidden"
	}
	return "upstream_error"
}

// sseUpstreamError writes the mid-stream failure frame followed by [DONE].
// It is only safe to call once the SSE body has been committed; before that the
// caller should return a real HTTP status instead.
func sseUpstreamError(r *http.Request, flusher http.Flusher, err error) string {
	msg := upstreamError(err)
	if IsRateLimited(err) {
		msg = "upstream is rate limiting; try again shortly"
	}
	if errors.Is(err, chathub.ErrOffensiveContent) {
		msg = "M365 content policy flagged this request as offensive"
	}
	msg = sanitizePublicInternalText(msg)
	return "data: " + mustJSON(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "upstream_error",
			"code":    streamErrorCode(err),
		},
	}) + "\n\n"
}
