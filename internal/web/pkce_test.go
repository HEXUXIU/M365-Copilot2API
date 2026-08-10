package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"m365-copilot2api/internal/auth"
)

func TestStartPKCEUsesBrowserClientDefaults(t *testing.T) {
	t.Setenv("M365_CLIENT_ID", "")
	t.Setenv("M365_AUTHORITY", "")
	t.Setenv("M365_REDIRECT_URI", "")

	s := &Server{pkce: map[string]pendingPKCE{}}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/start", nil)
	r.Host = "172.30.0.214"
	r.Header.Set("X-Forwarded-Host", "unregistered.example")
	r.Header.Set("X-Forwarded-Proto", "https")
	s.startPKCE(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var response struct {
		State       string `json:"state"`
		URL         string `json:"url"`
		RedirectURI string `json:"redirectUri"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.State == "" {
		t.Fatal("response omitted state")
	}
	if got, want := response.RedirectURI, "https://login.microsoftonline.com/common/oauth2/nativeclient"; got != want {
		t.Fatalf("redirect URI = %q, want %q", got, want)
	}
	u, err := url.Parse(response.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := u.Query().Get("client_id"), "c0ab8ce9-e9a0-42e7-b064-33d422df41f1"; got != want {
		t.Fatalf("client_id = %q, want %q", got, want)
	}
	if got := u.Query().Get("redirect_uri"); got != response.RedirectURI {
		t.Fatalf("authorization redirect URI = %q, response redirect URI = %q", got, response.RedirectURI)
	}
}

func TestStartPKCEUsesConfiguredRedirectURIExactly(t *testing.T) {
	const redirectURI = "https://app.example.test/api/auth/callback"
	t.Setenv("M365_REDIRECT_URI", redirectURI)
	t.Setenv("M365_PUBLIC_URL", "https://other.example.test")

	s := &Server{pkce: map[string]pendingPKCE{}}
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/start", nil)
	r.Host = "172.30.0.214"
	r.Header.Set("X-Forwarded-Host", "unregistered.example")
	r.Header.Set("X-Forwarded-Proto", "https")
	s.startPKCE(rr, r)

	var response struct {
		URL         string `json:"url"`
		RedirectURI string `json:"redirectUri"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if got := response.RedirectURI; got != redirectURI {
		t.Fatalf("redirect URI = %q, want %q", got, redirectURI)
	}
	u, err := url.Parse(response.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("redirect_uri"); got != redirectURI {
		t.Fatalf("authorization redirect URI = %q, want %q", got, redirectURI)
	}
}

func TestPKCEStatusReportsPendingAndExpired(t *testing.T) {
	s := &Server{pkce: map[string]pendingPKCE{
		"pending": {Created: time.Now(), Status: "pending"},
		"expired": {Created: time.Now().Add(-11 * time.Minute), Status: "pending"},
	}}

	for _, tc := range []struct {
		state string
		want  string
	}{
		{state: "pending", want: "pending"},
		{state: "expired", want: "expired"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s.pkceStatus(rr, httptest.NewRequest(http.MethodGet, "/api/auth/status?state="+tc.state, nil))
			var response map[string]any
			if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
				t.Fatal(err)
			}
			if got := response["status"]; got != tc.want {
				t.Fatalf("status = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestCallbackPKCERejectsMissingUnknownExpiredAndConsumedState(t *testing.T) {
	tests := []struct {
		name   string
		state  string
		entry  pendingPKCE
		add    bool
		status int
	}{
		{name: "missing", status: http.StatusBadRequest},
		{name: "unknown", state: "unknown", status: http.StatusBadRequest},
		{name: "expired", state: "expired", entry: pendingPKCE{Created: time.Now().Add(-11 * time.Minute), Status: "pending"}, add: true, status: http.StatusBadRequest},
		{name: "consumed", state: "consumed", entry: pendingPKCE{Created: time.Now(), Status: "authenticated"}, add: true, status: http.StatusConflict},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{pkce: map[string]pendingPKCE{}}
			if tc.add {
				s.pkce[tc.state] = tc.entry
			}
			path := "/api/auth/callback"
			if tc.state != "" {
				path += "?state=" + url.QueryEscape(tc.state) + "&code=code"
			}
			rr := httptest.NewRecorder()
			s.callbackPKCE(rr, httptest.NewRequest(http.MethodGet, path, nil))
			if rr.Code != tc.status {
				t.Fatalf("status = %d, want %d; body = %s", rr.Code, tc.status, rr.Body.String())
			}
		})
	}
}

func TestCallbackPKCEConsumesMicrosoftErrorOnce(t *testing.T) {
	s := &Server{pkce: map[string]pendingPKCE{
		"state": {Created: time.Now(), Status: "pending"},
	}}
	path := "/api/auth/callback?state=state&error=access_denied"
	first := httptest.NewRecorder()
	s.callbackPKCE(first, httptest.NewRequest(http.MethodGet, path, nil))
	if first.Code != http.StatusBadRequest {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	s.callbackPKCE(second, httptest.NewRequest(http.MethodGet, path, nil))
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d, body = %s", second.Code, second.Body.String())
	}
}

func TestCallbackPKCEAcceptsPastedURLAndReturnsSafeCompletionPage(t *testing.T) {
	const code = "sensitive-authorization-code"
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("code") != code || r.Form.Get("code_verifier") != "verifier" {
			t.Fatalf("unexpected exchange form: %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"access_token":"header.payload.signature","refresh_token":"sensitive-refresh-token","expires_in":3600}`)
	}))
	defer tokenServer.Close()
	t.Setenv("M365_TOKEN_ENDPOINT", tokenServer.URL)
	t.Setenv("M365_BROWSER_REDIRECT_URI", "http://127.0.0.1:4141/api/auth/callback")
	store, err := auth.OpenStore(t.TempDir() + "/accounts.json")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{tokens: store, pkce: map[string]pendingPKCE{
		"state": {Verifier: "verifier", Created: time.Now(), Status: "pending", RedirectURI: "http://127.0.0.1:4141/api/auth/callback"},
	}}
	callbackURL := "http://127.0.0.1:4141/api/auth/callback?code=" + code + "&state=state"
	rr := httptest.NewRecorder()
	s.callbackPKCE(rr, httptest.NewRequest(http.MethodGet, "/api/auth/callback?url="+url.QueryEscape(callbackURL), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, secret := range []string{code, "sensitive-refresh-token", callbackURL} {
		if strings.Contains(body, secret) {
			t.Fatalf("completion page exposed sensitive value %q", secret)
		}
	}
	if !strings.Contains(body, "window.close()") {
		t.Fatal("completion page does not attempt to close the popup")
	}
}

func TestCallbackPKCERecordsMockTokenFailureWithoutLeakingCode(t *testing.T) {
	const code = "sensitive-failed-code"
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_grant","error_description":"AADSTS70000: invalid grant"}`)
	}))
	defer tokenServer.Close()
	t.Setenv("M365_TOKEN_ENDPOINT", tokenServer.URL)
	s := &Server{pkce: map[string]pendingPKCE{
		"state": {Verifier: "verifier", Created: time.Now(), Status: "pending", RedirectURI: auth.DefaultRedirectURI},
	}}
	rr := httptest.NewRecorder()
	s.callbackPKCE(rr, httptest.NewRequest(http.MethodGet, "/api/auth/callback?state=state&code="+code, nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), code) || strings.Contains(s.pkce["state"].Error, code) {
		t.Fatal("failed exchange exposed authorization code")
	}
}
