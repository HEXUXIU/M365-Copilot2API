package web

import (
	"strings"
	"testing"

	"m365-copilot2api/internal/chathub"
)

// Streaming starts with HTTP 200, so a mid-stream failure is only visible as an
// error frame inside the body. Clients that follow the spec strictly ignore
// codes they do not recognise and treat the turn as a normal empty completion,
// so a failure mislabelled as "rate_limit" silently loses the request.
func TestStreamErrorCodeDistinguishesFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"rate limit notice", chathub.ErrRateLimitNotice, "rate_limit_exceeded"},
		{"overloaded", &UpstreamHTTPError{Status: 503}, "upstream_overloaded"},
		{"read timeout", &chathub.DialError{Kind: "WS_READ_TIMEOUT"}, "upstream_timeout"},
		{"write timeout", &chathub.DialError{Kind: "WS_WRITE_TIMEOUT"}, "upstream_timeout"},
		{"handshake", &chathub.DialError{Kind: "WS_HANDSHAKE"}, "upstream_connection_failed"},
		{"content policy", chathub.ErrOffensiveContent, "content_policy_violation"},
		{"empty completion", chathub.ErrEmptyCompletion, "upstream_empty_completion"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := streamErrorCode(tc.err); got != tc.want {
				t.Fatalf("streamErrorCode(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// An unknown failure must not be reported as throttling: that is what made a
// write timeout look like quota exhaustion to the caller.
func TestStreamErrorCodeNeverGuessesRateLimit(t *testing.T) {
	got := streamErrorCode(&chathub.DialError{Kind: "WS_WRITE_TIMEOUT"})
	if got == "rate_limit" || got == "rate_limit_exceeded" {
		t.Fatalf("write timeout reported as rate limiting: %q", got)
	}
}

func TestSSEUpstreamErrorCarriesTypeAndCode(t *testing.T) {
	frame := sseUpstreamError(nil, nil, &chathub.DialError{Kind: "WS_WRITE_TIMEOUT"})
	if !strings.HasPrefix(frame, "data: {") {
		t.Fatalf("not an SSE data frame: %q", frame)
	}
	if !strings.Contains(frame, `"code":"upstream_timeout"`) {
		t.Fatalf("frame missing upstream_timeout code: %q", frame)
	}
	if !strings.Contains(frame, `"type":"upstream_error"`) {
		t.Fatalf("frame missing type: %q", frame)
	}
}

// A write timeout must report itself, not borrow the read-timeout wording.
func TestWriteTimeoutMessageIsDistinct(t *testing.T) {
	write := upstreamError(&chathub.DialError{Kind: "WS_WRITE_TIMEOUT"})
	read := upstreamError(&chathub.DialError{Kind: "WS_READ_TIMEOUT"})
	if write == read {
		t.Fatalf("write and read timeouts report the same message: %q", write)
	}
	if !strings.Contains(write, "sending") {
		t.Fatalf("write timeout message does not say what failed: %q", write)
	}
}