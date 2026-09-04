package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

var allowedImageProgressStages = map[string]struct{}{
	"queued":      {},
	"generating":  {},
	"downloading": {},
	"completed":   {},
}

// imageProgressStream writes a protocol-compatible SSE stream for an image
// request. It never fabricates a percentage; stages reflect real request state.
type imageProgressStream struct {
	mu       sync.Mutex
	writer   http.ResponseWriter
	flusher  http.Flusher
	ctx      context.Context
	prefix   string
	started  time.Time
	interval time.Duration
	last     string
	stop     chan struct{}
	done     chan struct{}
	stopped  bool
	stopOnce sync.Once
}

func newImageProgressStream(writer http.ResponseWriter, ctx context.Context, prefix string, interval time.Duration) *imageProgressStream {
	if ctx == nil {
		ctx = context.Background()
	}
	if prefix == "" {
		prefix = "image_generation"
	}
	return &imageProgressStream{
		writer:   writer,
		ctx:      ctx,
		prefix:   prefix,
		started:  time.Now(),
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (s *imageProgressStream) Start() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return errors.New("image progress stream stopped")
	}
	if s.writer == nil {
		s.stopped = true
		s.mu.Unlock()
		return errors.New("image progress stream writer is nil")
	}
	flusher, ok := s.writer.(http.Flusher)
	if !ok {
		s.stopped = true
		s.mu.Unlock()
		return errors.New("image progress stream requires http.Flusher")
	}
	s.flusher = flusher
	header := s.writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	s.writer.WriteHeader(http.StatusOK)
	s.flusher.Flush()
	interval := s.interval
	s.mu.Unlock()

	if interval <= 0 {
		close(s.done)
		return nil
	}
	go s.heartbeat(interval)
	return nil
}

func (s *imageProgressStream) heartbeat(interval time.Duration) {
	defer close(s.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			if s.stopped {
				s.mu.Unlock()
				return
			}
			_, err := fmt.Fprint(s.writer, ": image-generation keepalive\n\n")
			if err != nil {
				s.stopLocked()
				s.mu.Unlock()
				return
			}
			s.flusher.Flush()
			s.mu.Unlock()
		case <-s.stop:
			return
		case <-s.ctx.Done():
			s.mu.Lock()
			s.stopLocked()
			s.mu.Unlock()
			return
		}
	}
}

func (s *imageProgressStream) Stage(stage string) error {
	if _, ok := allowedImageProgressStages[stage]; !ok {
		return fmt.Errorf("invalid image progress stage %q", stage)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errors.New("image progress stream stopped")
	}
	if stage == s.last {
		return nil
	}
	payload := map[string]any{
		"type":            s.prefix + ".progress",
		"stage":           stage,
		"elapsed_seconds": int64(time.Since(s.started) / time.Second),
	}
	if err := s.emitLocked(s.prefix+".progress", payload); err != nil {
		return err
	}
	s.last = stage
	return nil
}

func (s *imageProgressStream) Emit(eventName string, payload any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emitLocked(eventName, payload)
}

func (s *imageProgressStream) Error(message string) error {
	if message == "" {
		message = "upstream request failed"
	}
	return s.Emit("error", map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "upstream_error", "message": message},
	})
}

func (s *imageProgressStream) emitLocked(eventName string, payload any) error {
	if s.stopped {
		return errors.New("image progress stream stopped")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", eventName, body); err != nil {
		s.stopLocked()
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *imageProgressStream) Stop() {
	s.mu.Lock()
	s.stopLocked()
	s.mu.Unlock()
	if s.interval > 0 {
		<-s.done
	}
}

func (s *imageProgressStream) stopLocked() {
	if s.stopped {
		return
	}
	s.stopped = true
	s.stopOnce.Do(func() { close(s.stop) })
}
