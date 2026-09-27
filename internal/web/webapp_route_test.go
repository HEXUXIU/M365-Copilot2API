package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebAppHandlerDirect(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.serveWebApp(w, httptest.NewRequest(http.MethodGet, "/webapp/index.html", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("direct /webapp/index.html status=%d body=%q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "root") {
		t.Fatalf("SPA shell missing #root: %q", w.Body.String())
	}
}

func TestWebAppRouteServed(t *testing.T) {
	s := &Server{}
	handler := s.Routes()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/webapp/index.html", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/webapp/index.html status=%d body=%q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "root") {
		t.Fatalf("SPA shell missing #root: %q", w.Body.String())
	}
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/webapp/", nil))
	if w2.Code != http.StatusOK {
		t.Fatalf("/webapp/ status=%d body=%q", w2.Code, w2.Body.String())
	}
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, httptest.NewRequest(http.MethodGet, "/webapp/assets/index-CuzoNQcv.css", nil))
	if w3.Code != http.StatusOK {
		t.Fatalf("asset status=%d", w3.Code)
	}
}
