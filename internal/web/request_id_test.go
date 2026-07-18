package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDIsGeneratedByServer(t *testing.T) {
	h := requestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := w.Header().Get(requestIDHeader); got == "" {
			t.Fatal("request ID missing inside handler")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set(requestIDHeader, "client-supplied")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d", w.Code)
	}
	if got := w.Header().Get(requestIDHeader); got == "" || got == "client-supplied" {
		t.Fatalf("unexpected request ID %q", got)
	}
}
