package chathub

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestDownloadClientRevalidatesRedirects proves the attachment download client
// re-applies the SSRF check on every redirect hop. Without this, a server could
// return a public URL to validateRemoteDownloadURL and then 302 to an internal
// or cloud-metadata address, which the client would blindly follow.
func TestDownloadClientRevalidatesRedirects(t *testing.T) {
	c := &Client{HTTPClient: &http.Client{}}
	dc := c.downloadClient()
	if dc.CheckRedirect == nil {
		t.Fatal("download client has no redirect guard")
	}

	blocked := []string{
		"http://169.254.169.254/latest/meta-data/", // cloud metadata, plaintext
		"https://169.254.169.254/",                 // cloud metadata, TLS
		"https://127.0.0.1/",                       // loopback
		"https://10.0.0.5/",                        // private
	}
	for _, target := range blocked {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if err := dc.CheckRedirect(req, nil); err == nil {
			t.Errorf("redirect to %s was allowed; SSRF guard bypassed", target)
		}
	}

	// A redirect chain that grows too long is refused regardless of target.
	via := make([]*http.Request, 5)
	req := httptest.NewRequest(http.MethodGet, "https://example.com/ok.png", nil)
	if err := dc.CheckRedirect(req, via); err == nil {
		t.Error("excessive redirect chain was not capped")
	}
}
