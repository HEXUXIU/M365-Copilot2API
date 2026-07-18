package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type debugRecord struct {
	ID           string    `json:"id"`
	At           time.Time `json:"at"`
	Path         string    `json:"path"`
	Method       string    `json:"method"`
	Status       int       `json:"status"`
	Level        string    `json:"level"`
	DurationMS   int64     `json:"durationMs"`
	InputTokens  *int      `json:"inputTokens"`
	OutputTokens *int      `json:"outputTokens"`
	TokenSource  string    `json:"tokenSource"`
	CacheHit     *bool     `json:"cacheHit"`
	CacheSource  string    `json:"cacheSource"`
	Client       any       `json:"client"`
	Upstream     any       `json:"upstream"`
	Gateway      any       `json:"gateway"`
}
type debugStore struct {
	mu      sync.RWMutex
	records []debugRecord
	path    string
}

func openDebugStore() *debugStore {
	p := strings.TrimSpace(os.Getenv("M365_DEBUG_LOG"))
	if p == "" {
		p = "debug-logs.jsonl"
	}
	return &debugStore{path: p}
}
func redactBody(b []byte) any {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return string(b)
	}
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"api_key", "apiKey", "authorization", "access_token", "accessToken"} {
			if _, yes := m[k]; yes {
				m[k] = "[redacted]"
			}
		}
	}
	return v
}
func debugLevel(status int) string {
	if status >= 500 {
		return "error"
	}
	if status >= 400 {
		return "warn"
	}
	return "info"
}
func debugLevelRank(level string) int {
	switch level {
	case "debug":
		return 0
	case "info":
		return 1
	case "warn":
		return 2
	case "error":
		return 3
	case "silent":
		return 4
	}
	return 1
}
func (d *debugStore) add(r debugRecord) {
	configured := currentSettings().LogLevel
	if configured == "silent" || debugLevelRank(r.Level) < debugLevelRank(configured) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = append(d.records, r)
	if len(d.records) > 500 {
		d.records = d.records[len(d.records)-500:]
	}
	if f, e := os.OpenFile(d.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); e == nil {
		b, _ := json.Marshal(r)
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
}
func (d *debugStore) list() []debugRecord {
	d.mu.RLock()
	defer d.mu.RUnlock()
	o := append([]debugRecord(nil), d.records...)
	for i, j := 0, len(o)-1; i < j; i, j = i+1, j-1 {
		o[i], o[j] = o[j], o[i]
	}
	return o
}
func (d *debugStore) get(id string) (debugRecord, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, r := range d.records {
		if r.ID == id {
			return r, true
		}
	}
	return debugRecord{}, false
}

const maxDebugCaptureBytes = 256 << 10

type limitedBuffer struct {
	bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len() >= maxDebugCaptureBytes {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > maxDebugCaptureBytes-b.Len() {
		_, _ = b.Buffer.Write(p[:maxDebugCaptureBytes-b.Len()])
		b.truncated = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

type captureWriter struct {
	http.ResponseWriter
	status int
	body   limitedBuffer
}

func (c *captureWriter) WriteHeader(s int) { c.status = s; c.ResponseWriter.WriteHeader(s) }
func (c *captureWriter) Flush() {
	if c.status == 0 {
		c.status = 200
	}
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (c *captureWriter) Header() http.Header { return c.ResponseWriter.Header() }
func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}
func (s *Server) debugMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		in, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDebugCaptureBytes))
		r.Body = io.NopCloser(bytes.NewReader(in))
		cw := &captureWriter{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(cw, r)
		out := cw.body.Bytes()
		rec := debugRecord{ID: "dbg_" + uuid.NewString(), At: start, Level: debugLevel(cw.status), Path: r.URL.Path, Method: r.Method, Status: cw.status, DurationMS: time.Since(start).Milliseconds(), TokenSource: "unavailable_from_chathub", CacheSource: "not_reported_by_upstream", Client: redactBody(in), Gateway: redactBody(out), Upstream: map[string]any{"captured": false, "reason": "ChatHub transport tracing not yet attached to request context"}}
		s.debug.add(rec)
	})
}
func (s *Server) debugList(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{"records": s.debug.list()})
}
func (s *Server) debugDetail(w http.ResponseWriter, r *http.Request) {
	if x, ok := s.debug.get(r.URL.Query().Get("id")); ok {
		jsonOut(w, x)
		return
	}
	http.Error(w, "not found", 404)
}
