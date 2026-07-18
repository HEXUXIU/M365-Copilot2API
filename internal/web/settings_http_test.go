package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestAdminSettingsHTTP(t *testing.T) {
	st := &settingsStore{path: filepath.Join(t.TempDir(), "settings.json"), v: defaultRuntimeSettings()}
	s := &Server{settings: st}
	r := httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil)
	w := httptest.NewRecorder()
	s.adminSettings(w, r)
	if w.Code != 200 {
		t.Fatalf("GET=%d %s", w.Code, w.Body.String())
	}
	var getBody struct {
		Settings      runtimeSettings `json:"settings"`
		CodexModels   []string        `json:"codexModels"`
		UpstreamTones []string        `json:"upstreamTones"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getBody); err != nil {
		t.Fatal(err)
	}
	if len(getBody.Settings.ModelMappings) == 0 || len(getBody.CodexModels) == 0 || len(getBody.UpstreamTones) == 0 {
		t.Fatalf("missing model mapping settings: %#v", getBody)
	}
	v := st.get()
	v.MaxToolCallsPerTurn = 1
	v.MaxToolRounds = 24
	v.ChatTimeoutSeconds = 75
	v.ImageTimeoutSeconds = 180
	b, _ := json.Marshal(v)
	r = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(b))
	w = httptest.NewRecorder()
	s.adminSettings(w, r)
	if w.Code != 200 {
		t.Fatalf("PUT=%d %s", w.Code, w.Body.String())
	}
	if st.get().ChatTimeoutSeconds != 75 {
		t.Fatal("hot setting not updated")
	}
	v.MaxToolCallsPerTurn = 0
	b, _ = json.Marshal(v)
	r = httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader(b))
	w = httptest.NewRecorder()
	s.adminSettings(w, r)
	if w.Code != 400 {
		t.Fatalf("invalid PUT=%d", w.Code)
	}
}
