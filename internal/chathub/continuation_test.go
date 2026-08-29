package chathub

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestRunWithContinuationAppendsWithoutRepeatingOverlap(t *testing.T) {
	var calls int
	var streamed strings.Builder
	result, err := runWithContinuation(context.Background(), Request{Text: "write a long answer", AutoContinue: true}, func(value string) error {
		streamed.WriteString(value)
		return nil
	}, nil, func(req Request, onDelta func(string) error, _ StreamHandler) (Result, error) {
		calls++
		switch calls {
		case 1:
			if err := onDelta("alpha beta gamma"); err != nil {
				return Result{}, err
			}
			return Result{Text: "alpha beta gamma", ConversationID: "conv", SessionID: "sess", Incomplete: true, TerminalReason: "normal_close_with_content"}, nil
		case 2:
			if req.ConversationID != "conv" || req.SessionID != "sess" || req.AutoContinue || len(req.Tools) != 0 || req.MCPServerURL != "" || len(req.Attachments) != 0 {
				t.Fatalf("invalid continuation request: %#v", req)
			}
			if err := onDelta("beta gamma and delta"); err != nil {
				return Result{}, err
			}
			return Result{Text: "beta gamma and delta", ConversationID: "conv", SessionID: "sess", TerminalReason: "completion_frame"}, nil
		default:
			t.Fatalf("unexpected call %d", calls)
			return Result{}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || result.Text != "alpha beta gamma and delta" || streamed.String() != result.Text {
		t.Fatalf("calls=%d result=%q streamed=%q", calls, result.Text, streamed.String())
	}
	if result.Incomplete || result.TerminalReason != "completion_frame" {
		t.Fatalf("unexpected terminal state: %#v", result)
	}
}

func TestRunWithContinuationStopsAfterBoundedRetries(t *testing.T) {
	calls := 0
	result, err := runWithContinuation(context.Background(), Request{Text: "long", AutoContinue: true}, nil, nil, func(req Request, _ func(string) error, _ StreamHandler) (Result, error) {
		calls++
		return Result{Text: string(rune('a' + calls - 1)), ConversationID: "conv", SessionID: "sess", Incomplete: true, TerminalReason: "normal_close_with_content"}, nil
	})
	if !errors.Is(err, ErrIncompleteCompletion) {
		t.Fatalf("err=%v, want ErrIncompleteCompletion", err)
	}
	if calls != 1+maxAutoContinuationAttempts {
		t.Fatalf("calls=%d, want %d", calls, 1+maxAutoContinuationAttempts)
	}
	if result.Text != "abc" || !result.Incomplete {
		t.Fatalf("result=%#v", result)
	}
}

func TestRunWithContinuationDoesNotReplayToolTurn(t *testing.T) {
	calls := 0
	toolFrame := json.RawMessage(`{"type":1,"target":"update","arguments":[{"toolName":"shell","arguments":{"command":"dir"}}]}`)
	result, err := runWithContinuation(context.Background(), Request{Text: "use a tool", AutoContinue: true}, nil, nil, func(Request, func(string) error, StreamHandler) (Result, error) {
		calls++
		return Result{Text: "working", Events: []json.RawMessage{toolFrame}, Incomplete: true}, nil
	})
	if err != nil || calls != 1 || !result.Incomplete {
		t.Fatalf("calls=%d result=%#v err=%v", calls, result, err)
	}
}

func TestContinuationNovelHandlesUnicodeOverlap(t *testing.T) {
	if got := continuationNovel("这是已经输出的结尾", "输出的结尾，然后继续"); got != "，然后继续" {
		t.Fatalf("novel=%q", got)
	}
	if got := continuationNovel("complete sentence.", "new paragraph"); got != "new paragraph" {
		t.Fatalf("unrelated continuation=%q", got)
	}
}
