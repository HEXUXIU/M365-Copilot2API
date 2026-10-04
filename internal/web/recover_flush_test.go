package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The outermost wrapper must keep http.Flusher so streaming responses are
// delivered incrementally. When it lacks Flush, every inner Flush becomes a
// no-op and the whole body buffers until the handler returns.
func TestRecoverPanicsForwardsFlush(t *testing.T) {
	handler := recoverPanics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("recoverPanics wrapper does not implement http.Flusher")
			return
		}
		f.Flush()
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil))
	if !rec.Flushed {
		t.Fatal("Flush did not propagate to the underlying writer")
	}
}
