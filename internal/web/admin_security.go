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
	if dir := strings.TrimSpace(os.Getenv("M365_DATA_DIR")); dir != "" {
		return filepath.Join(dir, "admin-password")
	}
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
	// The writable persisted value takes precedence over bootstrap sources.
	if b, e := os.ReadFile(adminPasswordPath()); e == nil && strings.TrimSpace(string(b)) != "" {
		p := strings.TrimSpace(string(b))
		return p, p == defaultAdminPassword
	}
	if bootstrap := strings.TrimSpace(os.Getenv("M365_ADMIN_PASSWORD_BOOTSTRAP_FILE")); bootstrap != "" {
		if b, e := os.ReadFile(bootstrap); e == nil && strings.TrimSpace(string(b)) != "" {
			p := strings.TrimSpace(string(b))
			return p, p == defaultAdminPassword
		}
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
		// A trusted reverse proxy appends the client address to XFF. Use the
		// right-most valid address rather than the attacker-controlled first one.
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			if ip := net.ParseIP(strings.TrimSpace(parts[i])); ip != nil {
				return ip.String()
			}
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
	if len(p) < 6 {
		return errors.New("new password must be at least 6 characters")
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

const maxLoginAttemptEntries = 4096

func (s *Server) recordLoginFailure(ip string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.loginAttempts[ip]; !exists && len(s.loginAttempts) >= maxLoginAttemptEntries {
		for key, attempt := range s.loginAttempts {
			if now.Sub(attempt.WindowStart) > 15*time.Minute && now.After(attempt.LockedUntil) {
				delete(s.loginAttempts, key)
			}
		}
		if len(s.loginAttempts) >= maxLoginAttemptEntries {
			return
		}
	}
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
		writeOpenAIError(w, 500, "storage_error", "administrator password could not be saved; check the persistent data directory permissions")
		return
	}
	s.mu.Lock()
	s.adminPassword = b.New
	s.mustChangePassword = false
	s.adminSessions = map[string]time.Time{}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "m365_admin_session", Path: "/", HttpOnly: true, Secure: secureAdminCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	jsonOut(w, map[string]any{"status": "password_changed", "reauthenticate": true})
}
