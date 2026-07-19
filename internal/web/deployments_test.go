package web

import (
	"context"
	"m365-native/internal/outbound"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeployCloudflareQueriesRealSubdomain(t *testing.T) {
	var gotScript, gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if strings.Contains(r.URL.Path, "/workers/scripts/") {
			b := make([]byte, 1<<16)
			n, _ := r.Body.Read(b)
			gotScript = string(b[:n])
			w.WriteHeader(200)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/workers/subdomain") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"subdomain":"acct-sub"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	old := cloudflareAPIBase
	cloudflareAPIBase = ts.URL
	defer func() { cloudflareAPIBase = old }()
	d, err := deployCloudflare(context.Background(), "acct", "relay", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if d.DefaultURL != "https://relay.acct-sub.workers.dev" {
		t.Fatalf("url=%s", d.DefaultURL)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("auth=%q", gotAuth)
	}
	if !strings.Contains(gotScript, "/health") {
		t.Fatal("deployed worker lacks health route")
	}
}

func TestDeployCloudflareRejectsSubdomainFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "scripts") {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(500)
		_, _ = w.Write([]byte("no subdomain"))
	}))
	defer ts.Close()
	old := cloudflareAPIBase
	cloudflareAPIBase = ts.URL
	defer func() { cloudflareAPIBase = old }()
	if _, err := deployCloudflare(context.Background(), "acct", "relay", "secret"); err == nil {
		t.Fatal("expected subdomain error")
	}
}

func TestDeploymentCheckAddsHealthyURLToPool(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()
	if err := outbound.ConfigurePool(nil); err != nil {
		t.Fatal(err)
	}
	defer outbound.ConfigurePool(nil)
	st := &deploymentStore{Items: []deployment{{ID: "test", ActiveURL: ts.URL, DefaultURL: ts.URL}}}
	deployments = st
	s := &Server{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/deployment/check?id=test", nil)
	s.deploymentCheck(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	found := false
	for _, x := range outbound.ProxyPoolStatus() {
		if x["url"] == ts.URL {
			found = true
		}
	}
	if !found {
		t.Fatalf("healthy URL was not added: %#v", outbound.ProxyPoolStatus())
	}
}
