package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestResourcesReadRejectsUnlistedURI verifies the SSRF guard: only URIs
// advertised by ListResources may be read, even when they use an allowed scheme.
func TestResourcesReadRejectsUnlistedURI(t *testing.T) {
	orig := resourceProvider()
	SetGlobalResourceProvider(&stubResourceProvider{
		resources: []Resource{{URI: "m365://safe/one", Name: "Safe One", MIMEType: "text/plain"}},
		content: map[string]ResourceContent{
			"m365://internal/secret": {URI: "m365://internal/secret", Text: "top secret"},
			"m365://safe/one":        {URI: "m365://safe/one", Text: "hello"},
		},
	})
	defer SetGlobalResourceProvider(orig)

	GlobalRegistry = &sessionRegistry{sessions: map[string]*session{}}
	sessID := GlobalRegistry.RegisterSession(nil)

	read := func(uri string) string {
		w := httptest.NewRecorder()
		body := `{"jsonrpc":"2.0","id":9,"method":"resources/read","params":{"uri":"` + uri + `"}}`
		req := httptest.NewRequest(http.MethodPost, "/v1/mcp/message?sessionId="+sessID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		HandleMessage(w, req)
		sess := GlobalRegistry.getSession(sessID)
		select {
		case msg := <-sess.msgCh:
			return string(msg)
		case <-req.Context().Done():
			t.Fatal("timeout")
		}
		return ""
	}

	if out := read("m365://internal/secret"); !strings.Contains(out, "-32002") {
		t.Fatalf("unlisted URI was readable: %s", out)
	}
	if out := read("file:///etc/passwd"); !strings.Contains(out, "-32602") {
		t.Fatalf("disallowed scheme accepted: %s", out)
	}
	out := read("m365://safe/one")
	var resp struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil || !strings.Contains(string(resp.Result), "hello") {
		t.Fatalf("listed URI should be readable, got: %s", out)
	}
}
