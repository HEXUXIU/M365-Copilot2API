package web

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func adminTestClient(t *testing.T, h http.Handler) (*httptest.Server, *http.Client) {
	t.Helper()
	ts := httptest.NewTLSServer(h)
	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	t.Cleanup(ts.Close)
	return ts, c
}

func postJSON(t *testing.T, c *http.Client, url, body string) *http.Response {
	t.Helper()
	r, err := c.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDefaultPasswordForcesChangeAndRotatesSessions(t *testing.T) {
	t.Setenv("M365_ADMIN_PASSWORD", "")
	t.Setenv("M365_ADMIN_PASSWORD_FILE", t.TempDir()+"/admin-password")
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ts, c := adminTestClient(t, s.Routes())

	r := postJSON(t, c, ts.URL+"/api/admin/login", `{"password":"admin123"}`)
	if r.StatusCode != 200 {
		t.Fatalf("login=%d", r.StatusCode)
	}
	var login map[string]any
	_ = json.NewDecoder(r.Body).Decode(&login)
	r.Body.Close()
	if login["must_change_password"] != true {
		t.Fatalf("login=%#v", login)
	}

	r, _ = c.Get(ts.URL + "/api/accounts")
	r.Body.Close()
	if r.StatusCode != http.StatusForbidden {
		t.Fatalf("protected status=%d", r.StatusCode)
	}

	r = postJSON(t, c, ts.URL+"/api/admin/change-password", `{"current_password":"admin123","new_password":"a-new-password-123"}`)
	if r.StatusCode != 200 {
		t.Fatalf("change=%d", r.StatusCode)
	}
	r.Body.Close()

	r, _ = c.Get(ts.URL + "/api/accounts")
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old session status=%d", r.StatusCode)
	}

	r = postJSON(t, c, ts.URL+"/api/admin/login", `{"password":"a-new-password-123"}`)
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("new login=%d", r.StatusCode)
	}
	r, _ = c.Get(ts.URL + "/api/accounts")
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatalf("new session status=%d", r.StatusCode)
	}
}

func TestAdminLoginLocksAfterFiveFailures(t *testing.T) {
	t.Setenv("M365_ADMIN_PASSWORD", "correct-password")
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ts, c := adminTestClient(t, s.Routes())
	for i := 0; i < 5; i++ {
		r := postJSON(t, c, ts.URL+"/api/admin/login", `{"password":"wrong"}`)
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatalf("attempt %d=%d", i+1, r.StatusCode)
		}
	}
	r := postJSON(t, c, ts.URL+"/api/admin/login", `{"password":"correct-password"}`)
	defer r.Body.Close()
	if r.StatusCode != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("locked=%d retry=%q", r.StatusCode, r.Header.Get("Retry-After"))
	}
}

func TestPersistedPasswordOverridesBootstrapEnv(t *testing.T) {
	path := t.TempDir() + "/admin-password"
	t.Setenv("M365_ADMIN_PASSWORD_FILE", path)
	t.Setenv("M365_ADMIN_PASSWORD", "old-bootstrap-password")
	if err := saveAdminPassword("persisted-new-password"); err != nil {
		t.Fatal(err)
	}
	got, mustChange := loadAdminPassword()
	if got != "persisted-new-password" || mustChange {
		t.Fatalf("got=%q mustChange=%v", got, mustChange)
	}
}

func TestExpiredLoginWindowResets(t *testing.T) {
	s := &Server{loginAttempts: map[string]loginAttempt{"x": {Failures: 4, WindowStart: time.Now().Add(-16 * time.Minute)}}}
	if ok, _ := s.loginAllowed("x", time.Now()); !ok {
		t.Fatal("expired window remained locked")
	}
}
