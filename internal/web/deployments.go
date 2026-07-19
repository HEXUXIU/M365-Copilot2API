package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"m365-native/internal/outbound"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type deployment struct {
	ID            string    `json:"id"`
	Provider      string    `json:"provider"`
	Name          string    `json:"name"`
	AccountID     string    `json:"accountId,omitempty"`
	DefaultURL    string    `json:"defaultUrl,omitempty"`
	CustomURL     string    `json:"customUrl,omitempty"`
	ActiveURL     string    `json:"activeUrl,omitempty"`
	Status        string    `json:"status"`
	LatencyMs     int64     `json:"latencyMs,omitempty"`
	LastCheckedAt time.Time `json:"lastCheckedAt,omitempty"`
	LastError     string    `json:"lastError,omitempty"`
}
type deploymentStore struct {
	mu    sync.Mutex
	path  string
	Items []deployment `json:"items"`
}

var deployments *deploymentStore
var cloudflareAPIBase = "https://api.cloudflare.com/client/v4"

func openDeployments() *deploymentStore {
	if deployments != nil {
		return deployments
	}
	dir := os.Getenv("M365_DATA_DIR")
	if dir == "" {
		h, _ := os.UserHomeDir()
		dir = filepath.Join(h, ".config", "m365-native")
	}
	s := &deploymentStore{path: filepath.Join(dir, "deployments.json")}
	b, e := os.ReadFile(s.path)
	if e == nil {
		_ = json.Unmarshal(b, s)
	}
	deployments = s
	return s
}
func (s *deploymentStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := os.MkdirAll(filepath.Dir(s.path), 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(s, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(s.path, b, 0600)
}
func randomState() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *Server) deployments(w http.ResponseWriter, r *http.Request) {
	st := openDeployments()
	switch r.Method {
	case http.MethodGet:
		st.mu.Lock()
		items := append([]deployment(nil), st.Items...)
		st.mu.Unlock()
		jsonOut(w, map[string]any{"items": items})
	case http.MethodPost:
		var in struct{ Provider, Name, AccountID, Token string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&in) != nil {
			writeOpenAIError(w, 400, "invalid_request_error", "bad json")
			return
		}
		if in.Provider != "cloudflare" {
			writeOpenAIError(w, 400, "invalid_request_error", "only cloudflare is implemented")
			return
		}
		d, e := deployCloudflare(r.Context(), in.AccountID, in.Name, in.Token)
		if e != nil {
			writeOpenAIError(w, 400, "deployment_error", e.Error())
			return
		}
		st.mu.Lock()
		st.Items = append(st.Items, d)
		st.mu.Unlock()
		if e = st.save(); e != nil {
			writeOpenAIError(w, 500, "storage_error", e.Error())
			return
		}
		jsonOut(w, map[string]any{"ok": true, "deployment": d})
	default:
		writeOpenAIError(w, 405, "invalid_request_error", "method not allowed")
	}
}
func (s *Server) deploymentAction(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	st := openDeployments()
	st.mu.Lock()
	var d *deployment
	for i := range st.Items {
		if st.Items[i].ID == id {
			d = &st.Items[i]
			break
		}
	}
	if d == nil {
		st.mu.Unlock()
		writeOpenAIError(w, 404, "not_found", "deployment not found")
		return
	}
	var in struct {
		CustomURL string `json:"customUrl"`
	}
	if r.Method == http.MethodPut {
		_ = json.NewDecoder(r.Body).Decode(&in)
		d.CustomURL = strings.TrimRight(strings.TrimSpace(in.CustomURL), "/")
		d.ActiveURL = d.CustomURL
		if d.ActiveURL == "" {
			d.ActiveURL = d.DefaultURL
		}
		st.mu.Unlock()
		if e := st.save(); e != nil {
			writeOpenAIError(w, 500, "storage_error", e.Error())
			return
		}
		jsonOut(w, map[string]any{"ok": true, "deployment": d})
		return
	}
	st.mu.Unlock()
	writeOpenAIError(w, 405, "invalid_request_error", "method not allowed")
}
func deployCloudflare(ctx context.Context, account, name, token string) (deployment, error) {
	if account == "" || name == "" || token == "" {
		return deployment{}, fmt.Errorf("accountId、name、token 都不能为空")
	}
	base := strings.TrimRight(cloudflareAPIBase, "/")
	u := base + "/accounts/" + url.PathEscape(account) + "/workers/scripts/" + url.PathEscape(name)
	script := "export default {async fetch(request){if(new URL(request.url).pathname==='/health')return new Response('ok');return new Response('m365-native worker relay is configured',{status:200})}}"
	req, e := http.NewRequestWithContext(ctx, http.MethodPut, u, strings.NewReader(script))
	if e != nil {
		return deployment{}, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/javascript")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		return deployment{}, e
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return deployment{}, fmt.Errorf("cloudflare deploy failed: %s", strings.TrimSpace(string(body)))
	}
	// The workers.dev hostname is account-specific; query it instead of guessing from Account ID.
	checkURL := base + "/accounts/" + url.PathEscape(account) + "/workers/subdomain"
	q, qe := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if qe != nil {
		return deployment{}, qe
	}
	q.Header.Set("Authorization", "Bearer "+token)
	qr, qe := http.DefaultClient.Do(q)
	if qe != nil {
		return deployment{}, qe
	}
	defer qr.Body.Close()
	qb, _ := io.ReadAll(io.LimitReader(qr.Body, 1<<20))
	if qr.StatusCode/100 != 2 {
		return deployment{}, fmt.Errorf("cloudflare subdomain lookup failed: %s", strings.TrimSpace(string(qb)))
	}
	var sub struct {
		Result struct {
			Subdomain string `json:"subdomain"`
		} `json:"result"`
	}
	if json.Unmarshal(qb, &sub) != nil || sub.Result.Subdomain == "" {
		return deployment{}, fmt.Errorf("cloudflare returned no workers.dev subdomain")
	}
	defaultURL := "https://" + name + "." + sub.Result.Subdomain + ".workers.dev"
	return deployment{ID: "cf-" + randomState(), Provider: "cloudflare", Name: name, AccountID: account, DefaultURL: defaultURL, ActiveURL: defaultURL, Status: "deployed"}, nil
}
func (s *Server) deploymentCheck(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	st := openDeployments()
	st.mu.Lock()
	var d *deployment
	for i := range st.Items {
		if st.Items[i].ID == id {
			d = &st.Items[i]
			break
		}
	}
	if d == nil {
		st.mu.Unlock()
		writeOpenAIError(w, 404, "not_found", "deployment not found")
		return
	}
	target := d.ActiveURL
	if target == "" {
		target = d.DefaultURL
	}
	st.mu.Unlock()
	start := time.Now()
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, strings.TrimRight(target, "/")+"/health", nil)
	resp, e := http.DefaultClient.Do(req)
	lat := time.Since(start).Milliseconds()
	st.mu.Lock()
	if e != nil {
		d.Status = "unhealthy"
		d.LastError = e.Error()
	} else if resp.StatusCode != http.StatusOK {
		d.Status = "unhealthy"
		d.LastError = fmt.Sprintf("health returned %s", resp.Status)
		resp.Body.Close()
	} else {
		d.Status = "healthy"
		d.LastError = ""
		d.LatencyMs = lat
		d.LastCheckedAt = time.Now()
		resp.Body.Close()
		// Only verified endpoints enter the active proxy pool.
		_ = outbound.AddProxy(target)
	}
	out := *d
	st.mu.Unlock()
	_ = st.save()
	jsonOut(w, map[string]any{"ok": e == nil, "deployment": out})
}
