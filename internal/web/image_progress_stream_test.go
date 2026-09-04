package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestImageProgressStreamEmitsStageOnceAndNeverInventsPercent(t *testing.T) {
	recorder := httptest.NewRecorder()
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

	body := recorder.Body.String()
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
	recorder := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream := newImageProgressStream(recorder, ctx, "image_generation", 5*time.Millisecond)
	if err := stream.Start(); err != nil {
		t.Fatal(err)
	}
	defer stream.Stop()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && !strings.Contains(recorder.Body.String(), ": image-generation keepalive") {
		time.Sleep(time.Millisecond)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, ": image-generation keepalive") {
		t.Fatalf("heartbeat missing: %q", body)
	}
	if strings.Contains(body, `"stage":"heartbeat"`) {
		t.Fatalf("heartbeat was exposed as a stage: %q", body)
	}
}
