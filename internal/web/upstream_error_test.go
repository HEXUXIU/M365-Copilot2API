package web

import (
	"strings"
	"testing"

	"m365-copilot2api/internal/chathub"
)

// A caller cannot decide whether to retry if every upstream failure looks the
// same. These tests pin that the reason we know about reaches the client while
// the address that failed does not.
func TestUpstreamErrorNamesTheReasonWithoutLeakingAddresses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "websocket read timeout",
			err:  &chathub.DialError{Kind: "WS_READ_TIMEOUT"},
			want: "WS_READ_TIMEOUT",
		},
		{
			name: "websocket handshake",
			err:  &chathub.DialError{Kind: "WS_HANDSHAKE"},
			want: "WS_HANDSHAKE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := upstreamError(tc.err)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("upstreamError(%v) = %q; want it to mention %q", tc.err, got, tc.want)
			}
			for _, leak := range []string{"192.168.", "10.0.", "127.0.0.1", "https://"} {
				if strings.Contains(got, leak) {
					t.Fatalf("upstreamError leaked %q: %q", leak, got)
				}
			}
		})
	}
}

func TestUpstreamErrorPassesThroughUpstreamBody(t *testing.T) {
	// We only rewrite what we understand; anything the upstream told us is
	// reported as-is.
	got := upstreamError(&UpstreamHTTPError{Status: 503, Body: "upstream said: quota exhausted for tenant"})
	if !strings.Contains(got, "quota exhausted for tenant") {
		t.Fatalf("upstreamError dropped the upstream body: %q", got)
	}
}

func TestUpstreamErrorNil(t *testing.T) {
	if got := upstreamError(nil); got != "upstream request failed" {
		t.Fatalf("upstreamError(nil) = %q", got)
	}
}