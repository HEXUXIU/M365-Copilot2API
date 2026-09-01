package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestGateQueuesFIFOAndReleases(t *testing.T) {
	g := newRequestGate(1, 2)
	first, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	started := make(chan struct{})
	released := make(chan struct{})
	go func() {
		close(started)
		r, acquireErr := g.Acquire(context.Background())
		if acquireErr != nil {
			t.Errorf("queued acquire: %v", acquireErr)
			return
		}
		r()
		close(released)
	}()
	<-started
	deadline := time.After(100 * time.Millisecond)
	select {
	case <-released:
		t.Fatal("queued request ran before active slot was released")
	case <-deadline:
	}
	first()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("queued request was not promoted after release")
	}
}

func TestRequestGateQueueFullAndCancellation(t *testing.T) {
	g := newRequestGate(1, 1)
	release, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquire=%v, want context canceled", err)
	}
	queuedResult := make(chan func(), 1)
	go func() {
		queued, acquireErr := g.Acquire(context.Background())
		if acquireErr != nil {
			t.Errorf("first queued acquire: %v", acquireErr)
			return
		}
		queuedResult <- queued
	}()
	deadline := time.Now().Add(time.Second)
	for g.Snapshot()["queued"].(int) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, err := g.Acquire(context.Background()); !errors.Is(err, errRequestQueueFull) {
		t.Fatalf("full queue error=%v, want errRequestQueueFull", err)
	}
	release()
	select {
	case queued := <-queuedResult:
		queued()
	case <-time.After(time.Second):
		t.Fatal("queued request was not released")
	}
}

func TestRequestGateMiddlewareOnlyGatesAPIWork(t *testing.T) {
	g := newRequestGate(1, 0)
	s := &Server{requestGate: g}
	called := make(chan struct{}, 1)
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called <- struct{}{} })
	h := s.requestGateMiddleware(next)
	// Non-API paths do not consume a slot.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("non-API request was not passed through")
	}
}

func TestRequestGateWeightedPermitsProtectLongRequests(t *testing.T) {
	g := newRequestGate(4, 4)
	first, err := g.AcquireWeighted(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := g.AcquireWeighted(ctx, 2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("weighted acquire=%v, want deadline exceeded", err)
	}
	if got := g.Snapshot()["active"].(int); got != 3 {
		t.Fatalf("active permits=%d, want 3", got)
	}
	first()
	second, err := g.AcquireWeighted(context.Background(), 4)
	if err != nil {
		t.Fatal(err)
	}
	second()
}

func TestRequestWeightUsesContentLength(t *testing.T) {
	// Keep the fixture inside the production-supported 16 KiB minimum unit.
	// A sub-minimum value must fall back to the safe default rather than make
	// every ordinary request consume an excessive number of permits.
	t.Setenv("M365_REQUEST_WEIGHT_BYTES", "16384")
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("x", 40000)))
	request.ContentLength = 40000
	if got := requestWeight(request); got != 3 {
		t.Fatalf("request weight=%d, want 3", got)
	}
}
