package chathub

import (
	"errors"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// collectEmit returns an emit func appending every delta to out.
func collectEmit(out *[]string) func(string) error {
	return func(d string) error {
		*out = append(*out, d)
		return nil
	}
}

func TestFinalizeTextKeepsStreamedWhenFinalNotLonger(t *testing.T) {
	cases := []struct {
		name     string
		streamed string
		final    string
		want     string
	}{
		{"both empty", "", "", ""},
		{"final empty", "hello", "", "hello"},
		{"equal", "答案在这里", "答案在这里", "答案在这里"},
		{"final shorter", "a longer streamed answer", "short", "a longer streamed answer"},
		{"same length different content", "abcd", "wxyz", "abcd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var emitted []string
			got, err := finalizeText(tc.streamed, tc.final, 0, collectEmit(&emitted))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if len(emitted) != 0 {
				t.Fatalf("expected no emitted deltas, got %v", emitted)
			}
		})
	}
}

func TestFinalizeTextEmitsMissingTail(t *testing.T) {
	// Regression for issue #51: non-prefix writeAtCursor fragments are
	// skipped during the stream, leaving streamed a stale prefix of the
	// authoritative final message. The missing tail must be delivered to
	// streaming clients and the returned text must be the full answer.
	streamed := "首先，"
	final := "首先，我们需要检查配置文件。"
	var emitted []string
	got, err := finalizeText(streamed, final, 42, collectEmit(&emitted))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != final {
		t.Fatalf("got %q, want %q", got, final)
	}
	if want := []string{"我们需要检查配置文件。"}; len(emitted) != 1 || emitted[0] != want[0] {
		t.Fatalf("emitted %v, want %v", emitted, want)
	}
	// Reassembled stream must be valid UTF-8 and byte-identical to final.
	if joined := streamed + strings.Join(emitted, ""); joined != final {
		t.Fatalf("reassembled %q, want %q", joined, final)
	}
}

func TestFinalizeTextEmitsWholeFinalWhenNothingStreamed(t *testing.T) {
	final := "完整的最终回答"
	var emitted []string
	got, err := finalizeText("", final, 0, collectEmit(&emitted))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != final {
		t.Fatalf("got %q, want %q", got, final)
	}
	if len(emitted) != 1 || emitted[0] != final {
		t.Fatalf("emitted %v, want the whole final message", emitted)
	}
}

func TestFinalizeTextPrefersFinalOnDivergence(t *testing.T) {
	// A poisoned early delta means streamed is not a prefix of final.
	// Already-sent deltas cannot be retracted, so nothing more is emitted,
	// but the returned Result text must be the authoritative final message.
	streamed := "**"
	final := "好的，这是完整的答案。"
	var emitted []string
	got, err := finalizeText(streamed, final, 57, collectEmit(&emitted))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != final {
		t.Fatalf("got %q, want %q", got, final)
	}
	if len(emitted) != 0 {
		t.Fatalf("expected no emitted deltas on divergence, got %v", emitted)
	}
}

func TestFinalizeTextPropagatesEmitError(t *testing.T) {
	wantErr := errors.New("client went away")
	_, err := finalizeText("prefix ", "prefix and tail", 0, func(string) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want %v", err, wantErr)
	}
}

func TestCanFinalizeNormalClose(t *testing.T) {
	normalClose := &websocket.CloseError{Code: websocket.CloseNormalClosure, Text: "normal"}
	abnormalClose := &websocket.CloseError{Code: websocket.CloseAbnormalClosure, Text: "unexpected EOF"}
	tests := []struct {
		name                string
		err                 error
		sawSuccessfulResult bool
		final               string
		streamedLen         int
		want                bool
	}{
		{name: "successful result with final text", err: normalClose, sawSuccessfulResult: true, final: "done", want: true},
		{name: "successful result with streamed text", err: normalClose, sawSuccessfulResult: true, streamedLen: 12, want: true},
		{name: "missing result frame", err: normalClose, final: "done", want: false},
		{name: "empty result", err: normalClose, sawSuccessfulResult: true, want: false},
		{name: "abnormal close", err: abnormalClose, sawSuccessfulResult: true, final: "done", want: false},
		{name: "network error", err: errors.New("i/o timeout"), sawSuccessfulResult: true, final: "done", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := canFinalizeNormalClose(tc.err, tc.sawSuccessfulResult, tc.final, tc.streamedLen); got != tc.want {
				t.Fatalf("canFinalizeNormalClose()=%v, want %v", got, tc.want)
			}
		})
	}
}
