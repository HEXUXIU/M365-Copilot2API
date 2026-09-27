package web

import (
	"strings"
	"testing"
)

func TestStreamFinalResultFallbackIsNotEmpty(t *testing.T) {
	final := "最终回答"
	if got := streamFinalResultFallback(0, final); got != final {
		t.Fatalf("expected final result fallback %q, got %q", final, got)
	}
}

func TestStreamFinalResultFallbackDoesNotDuplicateStreamedContent(t *testing.T) {
	if got := streamFinalResultFallback(len("partial"), "最终回答"); got != "" {
		t.Fatalf("expected no fallback after streamed content, got %q", got)
	}
}

func TestStreamFinalResultFallbackIgnoresWhitespace(t *testing.T) {
	if got := streamFinalResultFallback(0, "  \n\t"); strings.TrimSpace(got) != "" {
		t.Fatalf("expected no whitespace-only fallback, got %q", got)
	}
}
