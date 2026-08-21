package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubResourceProvider struct {
	resources []Resource
	content   map[string]ResourceContent
}

func (s *stubResourceProvider) ListResources(ctx context.Context) ([]Resource, error) {
	return s.resources, nil
}

func (s *stubResourceProvider) ReadResource(ctx context.Context, uri string) (ResourceContent, error) {
	if c, ok := s.content[uri]; ok {
		return c, nil
	}
	return ResourceContent{}, errUnknownResource(uri)
}

type errUnknownResource string

func (e errUnknownResource) Error() string { return "unknown resource: " + string(e) }

func TestResourcesListReturnsRegisteredResources(t *testing.T) {
	orig := GlobalResourceProvider
	GlobalResourceProvider = &stubResourceProvider{
		resources: []Resource{
			{URI: "m365://test/foo", Name: "Test Foo", MIMEType: "text/plain"},
		},
	}
	defer func() { GlobalResourceProvider = orig }()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/message?sessionId=test-rsrc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`))
	req.Header.Set("Content-Type", "application/json")

	GlobalRegistry = &sessionRegistry{sessions: map[string]*session{}}
	sessID := GlobalRegistry.RegisterSession(nil)
	req.URL.RawQuery = "sessionId=" + sessID

	HandleMessage(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d", w.Code)
	}

	sess := GlobalRegistry.getSession(sessID)
	select {
	case msg := <-sess.msgCh:
		var resp struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(msg, &resp); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(resp.Result), "m365://test/foo") {
			t.Fatalf("missing resource URI in result: %s", resp.Result)
		}
	case <-req.Context().Done():
		t.Fatal("timeout")
	}
}

func TestResourcesReadReturnsContent(t *testing.T) {
	orig := GlobalResourceProvider
	GlobalResourceProvider = &stubResourceProvider{
		resources: []Resource{{URI: "m365://test/bar", Name: "Test Bar", MIMEType: "application/json"}},
		content: map[string]ResourceContent{
			"m365://test/bar": {URI: "m365://test/bar", MIMEType: "application/json", Text: `{"ok":true}`},
		},
	}
	defer func() { GlobalResourceProvider = orig }()

	GlobalRegistry = &sessionRegistry{sessions: map[string]*session{}}
	sessID := GlobalRegistry.RegisterSession(nil)

	w := httptest.NewRecorder()
	body := `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"m365://test/bar"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/message?sessionId="+sessID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	HandleMessage(w, req)

	sess := GlobalRegistry.getSession(sessID)
	select {
	case msg := <-sess.msgCh:
		if !strings.Contains(string(msg), "test/bar") || !strings.Contains(string(msg), "ok") {
			t.Fatalf("missing content in response: %s", msg)
		}
	case <-req.Context().Done():
		t.Fatal("timeout")
	}
}

func TestInitializeAdvertisesResourcesCapability(t *testing.T) {
	GlobalRegistry = &sessionRegistry{sessions: map[string]*session{}}
	sessID := GlobalRegistry.RegisterSession(nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/message?sessionId="+sessID, strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`))
	req.Header.Set("Content-Type", "application/json")

	HandleMessage(w, req)

	sess := GlobalRegistry.getSession(sessID)
	select {
	case msg := <-sess.msgCh:
		if !strings.Contains(string(msg), `"resources"`) {
			t.Fatalf("initialize response missing resources capability: %s", msg)
		}
	case <-req.Context().Done():
		t.Fatal("timeout")
	}
}
