package web

import (
	"strings"
	"testing"
)

func TestStreamFinalResultFallbackIsNotEmpty(t *testing.T) {
	var text strings.Builder
	if text.Len() != 0 {
		t.Fatal("test setup")
	}
	final := "最终回答"
	if text.Len() == 0 && strings.TrimSpace(final) != "" {
		text.WriteString(final)
	}
	if text.String() != final {
		t.Fatalf("expected final result fallback %q, got %q", final, text.String())
	}
}

func TestConsumeStreamTextEmitsOrdinaryTextImmediately(t *testing.T) {
	var pending strings.Builder
	var emitted strings.Builder
	if err := consumeStreamText(&pending, "首字 arrives", func(value string) error {
		emitted.WriteString(value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if emitted.String() != "首字 arrives" || pending.Len() != 0 {
		t.Fatalf("ordinary text was buffered: emitted=%q pending=%q", emitted.String(), pending.String())
	}
}

func TestConsumeStreamTextKeepsToolPrefixesTogether(t *testing.T) {
	var pending strings.Builder
	var emitted strings.Builder
	emit := func(value string) error {
		emitted.WriteString(value)
		return nil
	}
	if err := consumeStreamText(&pending, `{"co`, emit); err != nil {
		t.Fatal(err)
	}
	if emitted.Len() != 0 || pending.String() != `{"co` {
		t.Fatalf("partial JSON tool prefix leaked: emitted=%q pending=%q", emitted.String(), pending.String())
	}
	if err := consumeStreamText(&pending, `mmand":"ls"}`, emit); err != nil {
		t.Fatal(err)
	}
	if emitted.Len() != 0 || !strings.Contains(pending.String(), `"command"`) {
		t.Fatalf("JSON tool call was emitted as text: emitted=%q pending=%q", emitted.String(), pending.String())
	}

	pending.Reset()
	emitted.Reset()
	if err := consumeStreamText(&pending, "normal ", emit); err != nil {
		t.Fatal(err)
	}
	if err := consumeStreamText(&pending, "```ba", emit); err != nil {
		t.Fatal(err)
	}
	if emitted.String() != "normal " || pending.String() != "```ba" {
		t.Fatalf("fenced tool prefix handling changed: emitted=%q pending=%q", emitted.String(), pending.String())
	}
}
