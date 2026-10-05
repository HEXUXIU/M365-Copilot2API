package web

import (
	"strings"
	"testing"
)

// A reply containing a code fence used to lose everything after the fence: the
// streaming path emitted the text before the fence, buffered the fence, and
// dropped the tail. That is issue #102 ("truncated at the first fence").
func TestFenceStreamDoesNotDropTheTail(t *testing.T) {
	frags := []string{
		"I will create the file.\n```bash\n",
		"echo hello > note.txt\n```",
		"Then I will verify it with dir.",
	}

	pending, emitted := "", ""
	for _, f := range frags {
		out, held := fenceStream(pending, f, 3)
		emitted += out
		pending = held
	}
	emitted += pending

	for _, want := range []string{
		"I will create the file.",
		"Then I will verify it with dir.",
	} {
		if !strings.Contains(emitted, want) {
			t.Fatalf("output lost %q; got %q", want, emitted)
		}
	}
}

// A fence split across fragments must still be held back as one unit, so it is
// not mistaken for prose or emitted half-open.
func TestFenceStreamHoldsSplitFence(t *testing.T) {
	pending, emitted := "", ""
	for _, f := range []string{"before ```ba", "sh\necho hi\n``` after"} {
		out, held := fenceStream(pending, f, 3)
		emitted += out
		pending = held
	}
	emitted += pending

	if !strings.Contains(emitted, "before ") {
		t.Fatalf("text before the fence was not released: %q", emitted)
	}
	if !strings.Contains(emitted, " after") {
		t.Fatalf("text after the fence was not released: %q", emitted)
	}
	// The fence itself is released at the end, after the stream is known not to
	// contain a tool call, so it may appear anywhere in the final output.
	if !strings.Contains(emitted, "echo hi") {
		t.Fatalf("fenced body was lost entirely: %q", emitted)
	}
}

// Without the fix, everything after a closed fence was discarded.
func TestFenceStreamRegressionGuard(t *testing.T) {
	out, held := fenceStream("", "a```x\ny\n```TAIL", 3)
	if !strings.Contains(out, "TAIL") {
		t.Fatalf("tail dropped: emitted=%q held=%q", out, held)
	}
}
