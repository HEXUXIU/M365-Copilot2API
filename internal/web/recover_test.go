package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type flushSignalWriter struct {
	header  http.Header
	body    bytes.Buffer
	flushed chan struct{}
	once    sync.Once
}

func newFlushSignalWriter() *flushSignalWriter {
	return &flushSignalWriter{header: make(http.Header), flushed: make(chan struct{})}
}

func (w *flushSignalWriter) Header() http.Header         { return w.header }
func (w *flushSignalWriter) WriteHeader(int)             {}
func (w *flushSignalWriter) Write(p []byte) (int, error) { return w.body.Write(p) }
func (w *flushSignalWriter) Flush()                      { w.once.Do(func() { close(w.flushed) }) }

func TestRecoverPanicsPreservesStreamingFlush(t *testing.T) {
	release := make(chan struct{})
	done := make(chan struct{})
	w := newFlushSignalWriter()
	h := recoverPanics(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		flusher, ok := rw.(http.Flusher)
		if !ok {
			t.Error("panic recovery writer does not expose http.Flusher")
			return
		}
		_, _ = rw.Write([]byte("event: response.created\n\n"))
		flusher.Flush()
		<-release
	}))

	go func() {
		defer close(done)
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	}()

	select {
	case <-w.flushed:
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("SSE frame was not flushed before the handler returned")
	}
	close(release)
	<-done
}
