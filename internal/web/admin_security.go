package web

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultAdminPassword = "admin123"

type loginAttempt struct {
	Failures                 int
	WindowStart, LockedUntil time.Time
}

func adminPasswordPath() string {
	if p := strings.TrimSpace(os.Getenv("M365_ADMIN_PASSWORD_FILE")); p != "" {
		return p
	}
	if p := strings.TrimSpace(os.Getenv("M365_CONFIG")); p != "" {
		return filepath.Join(filepath.Dir(p), "admin-password")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "m365-native", "admin-password")
}
func loadAdminPassword() (string, bool) {
	// A password changed through the console must survive service restarts, so
	// the protected persisted value takes precedence over the bootstrap env var.
	if b, e := os.ReadFile(adminPasswordPath()); e == nil && strings.TrimSpace(string(b)) != "" {
		p := strings.TrimSpace(string(b))
		return p, p == defaultAdminPassword
	}
	if p := strings.TrimSpace(os.Getenv("M365_ADMIN_PASSWORD")); p != "" {
		return p, p == defaultAdminPassword
	}
	return defaultAdminPassword, true
}
func saveAdminPassword(password string) error {
	p := adminPasswordPath()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(password+"\n"), 0600)
}
func clientIP(r *http.Request) string {
	// Trust proxy headers only when the direct peer is loopback (normal local reverse-proxy deployment).
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if net.ParseIP(host).IsLoopback() {
		if x := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); x != "" {
			return x
		}
	}
	if host != "" {
		return host
	}
	return r.RemoteAddr
}
func validNewAdminPassword(p string) error {
	if p == defaultAdminPassword {
		return errors.New("new password must not be the default password")
	}
	if len(p) < 12 {
		return errors.New("new password must be at least 12 characters")
	}
	if len(p) > 256 {
		return errors.New("new password is too long")
	}
	return nil
}
func (s *Server) loginAllowed(ip string, now time.Time) (bool, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.loginAttempts[ip]
	if now.Before(a.LockedUntil) {
		return false, time.Until(a.LockedUntil)
	}
	if a.WindowStart.IsZero() || now.Sub(a.WindowStart) > 15*time.Minute {
		delete(s.loginAttempts, ip)
	}
	return true, 0
}
func (s *Server) recordLoginFailure(ip string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.loginAttempts[ip]
	if a.WindowStart.IsZero() || now.Sub(a.WindowStart) > 15*time.Minute {
		a = loginAttempt{WindowStart: now}
	}
	a.Failures++
	if a.Failures >= 5 {
		a.LockedUntil = now.Add(15 * time.Minute)
	}
	s.loginAttempts[ip] = a
}
func (s *Server) clearLoginFailures(ip string) {
	s.mu.Lock()
	delete(s.loginAttempts, ip)
	s.mu.Unlock()
}
func (s *Server) adminChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, 405, "invalid_request_error", "method not allowed")
		return
	}
	if !s.validAdminSession(r) {
		writeOpenAIError(w, 401, "auth_error", "administrator login required")
		return
	}
	var b struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b) != nil {
		writeOpenAIError(w, 400, "invalid_request_error", "bad json")
		return
	}
	s.mu.Lock()
	current := s.adminPassword
	s.mu.Unlock()
	if subtle.ConstantTimeCompare([]byte(b.Current), []byte(current)) != 1 {
		writeOpenAIError(w, 401, "auth_error", "current password is invalid")
		return
	}
	if err := validNewAdminPassword(b.New); err != nil {
		writeOpenAIError(w, 400, "invalid_request_error", err.Error())
		return
	}
	if err := saveAdminPassword(b.New); err != nil {
		writeOpenAIError(w, 500, "storage_error", "could not save administrator password")
		return
	}
	s.mu.Lock()
	s.adminPassword = b.New
	s.mustChangePassword = false
	s.adminSessions = map[string]time.Time{}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "m365_admin_session", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	jsonOut(w, map[string]any{"status": "password_changed", "reauthenticate": true})
}
