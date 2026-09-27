package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestAdminSettingsPartialPutMerges(t *testing.T) {
	st := &settingsStore{path: filepath.Join(t.TempDir(), "settings.json"), v: defaultRuntimeSettings()}
	s := &Server{settings: st}

	// 用户只改监听地址，其余字段（如工具调用上限）未提交。
	// 修复前整个结构体被零值覆盖，触发了"每轮工具调用数必须为 1-64"。
	body := `{"listenAddress":"127.0.0.1:29422"}`
	r := httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	s.adminSettings(w, r)
	if w.Code != 200 {
		t.Fatalf("partial PUT=%d %s", w.Code, w.Body.String())
	}
	got := st.get()
	if got.ListenAddress != "127.0.0.1:29422" {
		t.Fatalf("listenAddress not updated: %q", got.ListenAddress)
	}
	if got.MaxToolCallsPerTurn == 0 {
		t.Fatal("partial PUT zeroed MaxToolCallsPerTurn; settings merge broken")
	}
	if got.MaxToolCallsPerTurn != defaultRuntimeSettings().MaxToolCallsPerTurn {
		t.Fatalf("MaxToolCallsPerTurn changed: %d", got.MaxToolCallsPerTurn)
	}
}

func TestAdminSettingsDuplicateKeysDoNotPanic(t *testing.T) {
	// JSON 重复键是前端不该产生但可能出现的输入（如日志里错拼的字段）。
	// 修复的 bug 是部分 PUT 零值覆盖，这里只验证这类脏输入不 panic。
	st := &settingsStore{path: filepath.Join(t.TempDir(), "settings.json"), v: defaultRuntimeSettings()}
	s := &Server{settings: st}
	body := `{"listenAddress":"a:b","listenAddress":"c:d"}`
	r := httptest.NewRequest(http.MethodPut, "/api/admin/settings", bytes.NewReader([]byte(body)))
	w := httptest.NewRecorder()
	s.adminSettings(w, r)
	if w.Code != 200 {
		t.Fatalf("duplicate-key PUT should merge&succeed, got %d %s", w.Code, w.Body.String())
	}
}

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
	// Default model mappings are intentionally empty: no fake model names are
	// shipped. Custom mappings can be added through the console.
	if len(getBody.CodexModels) == 0 || len(getBody.UpstreamTones) == 0 {
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
