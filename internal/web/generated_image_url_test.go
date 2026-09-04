package web

import (
	"net/http/httptest"
	"testing"
)

func TestGeneratedImageURLPrefersConfiguredPublicURL(t *testing.T) {
	t.Setenv("M365_PUBLIC_URL", "https://clove.asia/")
	r := httptest.NewRequest("GET", "/v1/images/files/test", nil)
	r.Host = "172.19.0.1:4141"
	r.Header.Set("X-Forwarded-Proto", "http")

	got := generatedImageURL(r, "abc")
	if want := "https://clove.asia/v1/images/files/abc"; got != want {
		t.Fatalf("generatedImageURL = %q, want %q", got, want)
	}
}

func TestGeneratedImageURLFallsBackToForwardedHost(t *testing.T) {
	t.Setenv("M365_PUBLIC_URL", "")
	r := httptest.NewRequest("GET", "/v1/images/files/test", nil)
	r.Host = "172.19.0.1:4141"
	r.Header.Set("X-Forwarded-Host", "clove.asia")
	r.Header.Set("X-Forwarded-Proto", "https")

	got := generatedImageURL(r, "abc")
	if want := "https://clove.asia/v1/images/files/abc"; got != want {
		t.Fatalf("generatedImageURL = %q, want %q", got, want)
	}
}
