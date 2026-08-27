package chathub

import (
	"errors"
	"testing"
)

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

func TestStreamFallbackGuardDetectsSplitFallbackBeforeEmission(t *testing.T) {
	var guard streamFallbackGuard
	for _, chunk := range []string{"Sor", "ry, I was", "n't able to respond", " to that."} {
		out, err := guard.Push(chunk)
		if err != nil {
			if !errors.Is(err, ErrEmptyCompletion) {
				t.Fatalf("Push() error = %v", err)
			}
			return
		}
		if out != "" {
			t.Fatalf("fallback prefix leaked before classification: %q", out)
		}
	}
	t.Fatal("split fallback was not detected")
}

func TestStreamFallbackGuardDoesNotDelayNormalText(t *testing.T) {
	var guard streamFallbackGuard
	out, err := guard.Push("The result is 7.")
	if err != nil || out != "The result is 7." {
		t.Fatalf("normal text out=%q err=%v", out, err)
	}
	out, err = guard.Push(" More detail.")
	if err != nil || out != " More detail." {
		t.Fatalf("released text out=%q err=%v", out, err)
	}
}

func TestStreamFallbackGuardReleasesDivergingSorryPrefix(t *testing.T) {
	var guard streamFallbackGuard
	if out, err := guard.Push("Sorry, "); err != nil || out != "" {
		t.Fatalf("prefix out=%q err=%v", out, err)
	}
	out, err := guard.Push("the answer is still available.")
	if err != nil || out != "Sorry, the answer is still available." {
		t.Fatalf("diverging prefix out=%q err=%v", out, err)
	}
}
