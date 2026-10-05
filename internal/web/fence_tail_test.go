package web

import (
	"strings"
	"testing"
)

func collect(t *testing.T, frags []string, hold func(string) bool) string {
	t.Helper()
	pending, out := "", ""
	for _, f := range frags {
		emitted, held := fenceStream(pending, f, 3, hold)
		out += emitted
		pending = held
	}
	return out + pending
}

func neverHold(string) bool { return false }

// A reply containing a code fence used to lose everything after the fence: the
// streaming path emitted the text before the fence, buffered the fence, and
// dropped the tail. That is issue #102 ("truncated at the first fence").
func TestFenceStreamDoesNotDropTheTail(t *testing.T) {
	got := collect(t, []string{
		"I will create the file.\n```bash\n",
		"echo hello > note.txt\n```",
		"Then I will verify it with dir.",
	}, neverHold)

	for _, want := range []string{
		"I will create the file.",
		"Then I will verify it with dir.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output lost %q; got %q", want, got)
		}
	}
}

// A fence split across fragments must be recognised once it closes.
func TestFenceStreamHoldsSplitFence(t *testing.T) {
	got := collect(t, []string{"before ```ba", "sh\necho hi\n``` after"}, neverHold)
	for _, want := range []string{"before ", " after", "echo hi"} {
		if !strings.Contains(got, want) {
			t.Fatalf("lost %q; got %q", want, got)
		}
	}
}

// Two consecutive code blocks used to arrive out of order when the model
// streamed them as separate fragments: the first fence was held until the end
// of the stream while the text after it was sent, so the second fence reached
// the client first and the reply rendered as a mangled code section.
func TestFenceStreamKeepsConsecutiveFencesInOrder(t *testing.T) {
	got := collect(t, []string{
		"```text\nHello, World!\n```",
		"\n```python\nprint(1)\n```\nDone.",
	}, neverHold)

	iText := strings.Index(got, "```text")
	iPy := strings.Index(got, "```python")
	if iText < 0 || iPy < 0 {
		t.Fatalf("expected both fences, got %q", got)
	}
	if iText > iPy {
		t.Fatalf("fences out of order:\n%q", got)
	}
	if !strings.Contains(got, "Done.") {
		t.Fatalf("text after the last fence was lost: %q", got)
	}
	// The fences stay separate, each closed, so a client renders two code blocks.
	if !strings.Contains(got, "```text\nHello, World!\n```\n```python") {
		t.Fatalf("fences are not cleanly separated: %q", got)
	}
}

// A fence that is a real tool call must stay buffered so fencedToolCalls can
// consume it, instead of being streamed to the client as prose.
func TestFenceStreamHoldsToolCallFence(t *testing.T) {
	hold := func(fence string) bool {
		return len(fencedToolCalls(fence, []map[string]any{
			{"type": "function", "function": map[string]any{"name": "bash"}},
		}, "auto")) > 0
	}

	pending, out := "", ""
	for _, f := range []string{
		"Running it now.\n```bash\n",
		"{\"command\":\"echo hi\"}\n```",
	} {
		emitted, held := fenceStream(pending, f, 3, hold)
		out += emitted
		pending = held
	}

	if !strings.Contains(out, "Running it now.") {
		t.Fatalf("prose before the tool call was not released: %q", out)
	}
	if strings.Contains(out, "echo hi") {
		t.Fatalf("tool call leaked into the prose stream: %q", out)
	}
	// The fence is still available in pending for fencedToolCalls to consume.
	if !strings.Contains(pending, "echo hi") {
		t.Fatalf("tool call was not held for fencedToolCalls: %q", pending)
	}
	if !strings.Contains(pending, "```bash") {
		t.Fatalf("held text is not a complete fence: %q", pending)
	}
}

// Without the fix, everything after a closed fence was discarded.
func TestFenceStreamRegressionGuard(t *testing.T) {
	out, held := fenceStream("", "a```x\ny\n```TAIL", 3, neverHold)
	if !strings.Contains(out, "TAIL") {
		t.Fatalf("tail dropped: emitted=%q held=%q", out, held)
	}
}
