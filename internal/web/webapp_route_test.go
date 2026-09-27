package web

import (
	"net/http"
	"net/http/httptest"
	"regexp"
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

// The React console must be the primary UI at the root, not the legacy HTML.
func TestRootServesConsole(t *testing.T) {
	s := &Server{}
	handler := s.Routes()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "root") {
		t.Fatalf("root did not serve the React console: status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestWebAppAssetsServed(t *testing.T) {
	s := &Server{}
	handler := s.Routes()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/webapp/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("/webapp/ status=%d body=%q", w.Code, w.Body.String())
	}
	re := regexp.MustCompile(`/webapp/assets/[^"']+\.(?:js|css)`)
	assets := re.FindAllString(w.Body.String(), -1)
	if len(assets) == 0 {
		t.Fatalf("SPA shell referenced no assets: %q", w.Body.String())
	}
	for _, a := range assets {
		aw := httptest.NewRecorder()
		handler.ServeHTTP(aw, httptest.NewRequest(http.MethodGet, a, nil))
		if aw.Code != http.StatusOK {
			t.Fatalf("asset %s status=%d", a, aw.Code)
		}
	}
}
