package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

var errRequestQueueFull = errors.New("request queue is full")

type requestGateWaiter struct {
	ready   chan struct{}
	granted bool
	weight  int
}

// requestGate bounds active API work and keeps a FIFO wait queue. The gate is
// deliberately independent from per-account semaphores: it protects the
// whole service while account affinity still decides where each request runs.
type requestGate struct {
	mu       sync.Mutex
	limit    int
	maxQueue int
	active   int
	waiters  []*requestGateWaiter
}

func newRequestGate(limit, maxQueue int) *requestGate {
	if limit < 1 {
		limit = 500
	}
	if maxQueue < 0 {
		maxQueue = 1000
	}
	return &requestGate{limit: limit, maxQueue: maxQueue}
}

func (g *requestGate) promoteLocked() {
	for len(g.waiters) > 0 {
		w := g.waiters[0]
		if g.active+w.weight > g.limit {
			break
		}
		g.waiters = g.waiters[1:]
		if w.granted {
			continue
		}
		w.granted = true
		g.active += w.weight
		close(w.ready)
	}
}

func (g *requestGate) release(weight int) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			if weight < 1 {
				weight = 1
			}
			if g.active >= weight {
				g.active -= weight
			} else {
				g.active = 0
			}
			g.promoteLocked()
			g.mu.Unlock()
		})
	}
}

func (g *requestGate) Acquire(ctx context.Context) (func(), error) {
	return g.AcquireWeighted(ctx, 1)
}

// AcquireWeighted charges large request bodies more permits than small ones.
// The public limit remains a concurrency setting for ordinary requests while
// long-context and multimodal requests are naturally serialized under load.
func (g *requestGate) AcquireWeighted(ctx context.Context, weight int) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	if weight < 1 {
		weight = 1
	}
	g.mu.Lock()
	if weight > g.limit {
		weight = g.limit
	}
	w := &requestGateWaiter{ready: make(chan struct{}), weight: weight}
	if g.active+weight <= g.limit && len(g.waiters) == 0 {
		g.active += weight
		g.mu.Unlock()
		return g.release(weight), nil
	}
	if g.maxQueue > 0 && len(g.waiters) >= g.maxQueue {
		g.mu.Unlock()
		return nil, errRequestQueueFull
	}
	g.waiters = append(g.waiters, w)
	g.mu.Unlock()
	select {
	case <-w.ready:
		return g.release(weight), nil
	case <-ctx.Done():
		g.mu.Lock()
		if w.granted {
			g.mu.Unlock()
			return g.release(weight), nil
		}
		for i, queued := range g.waiters {
			if queued == w {
				g.waiters = append(g.waiters[:i], g.waiters[i+1:]...)
				break
			}
		}
		g.mu.Unlock()
		return nil, ctx.Err()
	}
}

const defaultRequestWeightBytes = 128 * 1024

func requestWeightBytes() int64 {
	if raw := strings.TrimSpace(os.Getenv("M365_REQUEST_WEIGHT_BYTES")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 16*1024 && n <= 4*1024*1024 {
			return n
		}
	}
	return defaultRequestWeightBytes
}

func requestWeight(r *http.Request) int {
	if r == nil || r.ContentLength <= 0 {
		return 1
	}
	unit := requestWeightBytes()
	weight := (r.ContentLength + unit - 1) / unit
	if weight < 1 {
		return 1
	}
	if weight > int64(^uint(0)>>1) {
		return int(^uint(0) >> 1)
	}
	return int(weight)
}

func (g *requestGate) SetLimits(limit, maxQueue int) {
	if g == nil || limit < 1 || maxQueue < 0 {
		return
	}
	g.mu.Lock()
	g.limit = limit
	g.maxQueue = maxQueue
	g.promoteLocked()
	g.mu.Unlock()
}

func (g *requestGate) Snapshot() map[string]any {
	if g == nil {
		return map[string]any{"limit": 0, "active": 0, "queued": 0}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return map[string]any{"limit": g.limit, "maxQueue": g.maxQueue, "active": g.active, "queued": len(g.waiters)}
}

func (s *Server) requestGateMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s == nil || s.requestGate == nil || !requestGatePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		release, err := s.requestGate.AcquireWeighted(r.Context(), requestWeight(r))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				writeOpenAIError(w, http.StatusRequestTimeout, "request_timeout", "request left the queue before an execution slot was available")
				return
			}
			if errors.Is(err, errRequestQueueFull) {
				w.Header().Set("Retry-After", "1")
				writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_error", "request queue is full; retry shortly")
				return
			}
			writeOpenAIError(w, http.StatusServiceUnavailable, "service_unavailable", err.Error())
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}

func requestGatePath(path string) bool {
	return strings.HasPrefix(path, "/v1/") || path == "/api/chat" || path == "/api/chat/stream"
}
