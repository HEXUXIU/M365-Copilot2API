package web

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type lockedResponseWriter struct {
	mu     sync.Mutex
	header http.Header
	buf    strings.Builder
	status int
}

func newLockedResponseWriter() *lockedResponseWriter {
	return &lockedResponseWriter{header: make(http.Header)}
}

func (w *lockedResponseWriter) Header() http.Header {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.header
}

func (w *lockedResponseWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = status
	}
}

func (w *lockedResponseWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.buf.Write(p)
}

func (w *lockedResponseWriter) Flush() {}

func (w *lockedResponseWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

var _ http.ResponseWriter = (*lockedResponseWriter)(nil)
var _ http.Flusher = (*lockedResponseWriter)(nil)

func TestImageProgressStreamEmitsStageOnceAndNeverInventsPercent(t *testing.T) {
	recorder := newLockedResponseWriter()
	ctx := context.Background()

	stream := newImageProgressStream(recorder, ctx, "image_generation", time.Hour)
	defer stream.Stop()
	if err := stream.Start(); err != nil {
		t.Fatal(err)
	}
	if err := stream.Stage("queued"); err != nil {
		t.Fatal(err)
	}
	if err := stream.Stage("queued"); err != nil {
		t.Fatal(err)
	}
	if err := stream.Stage("generating"); err != nil {
		t.Fatal(err)
	}

	body := recorder.String()
	if got := strings.Count(body, `"stage":"queued"`); got != 1 {
		t.Fatalf("queued stage count = %d, want 1; body=%q", got, body)
	}
	if got := strings.Count(body, `"stage":"generating"`); got != 1 {
		t.Fatalf("generating stage count = %d, want 1; body=%q", got, body)
	}
	if strings.Contains(body, `"percent"`) {
		t.Fatalf("progress body invented percent: %q", body)
	}
	if !strings.Contains(body, "event: image_generation.progress\n") {
		t.Fatalf("progress event missing: %q", body)
	}
}

func TestImageProgressStreamHeartbeatIsSSEComment(t *testing.T) {
	recorder := newLockedResponseWriter()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newImageProgressStream(recorder, ctx, "image_generation", 5*time.Millisecond)
	if err := stream.Start(); err != nil {
		t.Fatal(err)
	}
	defer stream.Stop()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && !strings.Contains(recorder.String(), ": image-generation keepalive") {
		time.Sleep(time.Millisecond)
	}
	body := recorder.String()
	if !strings.Contains(body, ": image-generation keepalive") {
		t.Fatalf("heartbeat missing: %q", body)
	}
	if strings.Contains(body, `"stage":"heartbeat"`) {
		t.Fatalf("heartbeat was exposed as a stage: %q", body)
	}
}

func TestImageProgressStreamDoesNotRepeatAStageAfterLifecycleAdvances(t *testing.T) {
	recorder := newLockedResponseWriter()
	stream := newImageProgressStream(recorder, context.Background(), "image_generation", time.Hour)
	defer stream.Stop()
	if err := stream.Start(); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"queued", "generating", "downloading", "generating", "completed", "downloading"} {
		if err := stream.Stage(stage); err != nil {
			t.Fatal(err)
		}
	}

	body := recorder.String()
	for _, stage := range []string{"queued", "generating", "downloading", "completed"} {
		if got := strings.Count(body, `"stage":"`+stage+`"`); got != 1 {
			t.Fatalf("stage %q count = %d, want 1; body=%q", stage, got, body)
		}
	}
}
