package web

import (
	"log"
	"net/http"
	"strings"
	"time"
)

type traceWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *traceWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *traceWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}
func (w *traceWriter) Flush() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func httpTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		tw := &traceWriter{ResponseWriter: w}
		log.Printf("[http-trace] id=%s stage=start method=%s path=%s", requestIDFrom(r), r.Method, r.URL.Path)
		next.ServeHTTP(tw, r)
		status := tw.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("[http-trace] id=%s stage=end status=%d bytes=%d total_ms=%d", requestIDFrom(r), status, tw.bytes, time.Since(start).Milliseconds())
	})
}
