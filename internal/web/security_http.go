package web

import (
	"embed"
	"io/fs"
	"net/http"
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
	if r.URL.Path != "/" && r.URL.Path != "/login" && r.URL.Path != "/conversation" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "method not allowed")
		return
	}
	name := "index.html"
	if r.URL.Path == "/login" {
		name = "login.html"
	} else if r.URL.Path == "/conversation" {
		name = "conversation.html"
	}
	f, err := webContent.Open(name)
	if err != nil {
		http.Error(w, "web interface unavailable", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "web interface unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, name, st.ModTime(), f)
}
