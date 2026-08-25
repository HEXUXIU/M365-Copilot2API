package chathub

import "testing"

func TestIsUpstreamFallback(t *testing.T) {
	for _, text := range []string{
		"Sorry, I wasn't able to respond to that. Is there something else I can help with?",
		"Sorry, it looks like I can't respond to this. Let’s try a different topic",
	} {
		if !isUpstreamFallback(text) {
			t.Fatalf("fallback not detected: %q", text)
		}
	}
	if isUpstreamFallback("ACCOUNT_OK") {
		t.Fatal("normal completion classified as fallback")
	}
}
