package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminModelsEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/models", nil)
	w := httptest.NewRecorder()
	s.adminModels(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "gpt-5.6") {
		t.Fatalf("missing model catalog: %s", w.Body.String()[:200])
	}
}
