package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:web
var webFS embed.FS

var webContent http.FileSystem

func init() {
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	webContent = http.FS(sub)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'; form-action 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; script-src 'self' 'unsafe-inline' https://unpkg.com https://cdn.jsdelivr.net")
		if r.URL.Path == "/" || r.URL.Path == "/login" || r.URL.Path == "/api/admin/login" || r.URL.Path == "/api/admin/session" || r.URL.Path == "/api/admin/change-password" {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) rootPage(w http.ResponseWriter, r *http.Request) {
	// /conversation is a standalone legacy detail page; everything else at the
	// root is the React console, so the SPA is the primary UI rather than the
	// legacy single-page HTML (which had the non-sticky sidebar and stale i18n).
	if r.URL.Path == "/conversation" {
		s.serveLegacyPage(w, r, "conversation.html")
		return
	}
	if r.URL.Path != "/" && r.URL.Path != "/login" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	s.serveWebAppShell(w, r)
}

func (s *Server) serveLegacyPage(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	f, err := webContent.Open(name)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "web interface unavailable")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "web interface unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func (s *Server) serveWebAppShell(w http.ResponseWriter, r *http.Request) {
	f, err := webContent.Open("webapp/index.html")
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "console unavailable")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "console unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", st.ModTime(), f)
}

// serveWebApp serves the built React bundle from the embedded FS. Vite emits
// index.html plus hashed assets under webapp/assets/.
func (s *Server) serveWebApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/webapp/")
	if rel == "" || strings.Contains(rel, "..") {
		rel = "index.html"
	}
	name := "webapp/" + rel
	f, err := webContent.Open(name)
	if err != nil {
		// SPA fallback: unknown paths render the app shell.
		if !strings.HasPrefix(rel, "assets/") {
			f2, err2 := webContent.Open("webapp/index.html")
			if err2 != nil {
				http.NotFound(w, r)
				return
			}
			defer f2.Close()
			st2, _ := f2.Stat()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			http.ServeContent(w, r, "index.html", st2.ModTime(), f2)
			return
		}
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(name, ".js") {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else if strings.HasSuffix(name, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	} else if strings.HasSuffix(name, ".html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	http.ServeContent(w, r, name, st.ModTime(), f)
}
