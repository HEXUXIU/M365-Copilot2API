package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestAdminUsageHandlersEnrichKeyNames(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("M365_USAGE_LOG", filepath.Join(dir, "usage.jsonl"))
	t.Setenv("M365_API_KEYS", filepath.Join(dir, "api-keys.json"))
	store := openAPIKeys()
	record, raw, err := store.create("prod key")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{usage: openUsageLog(), apiKeys: store}

	// 真实 key：走网关同样的解析路径得到 (id, prefix)。
	keyReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	keyReq.Header.Set("X-API-Key", raw)
	keyID, keyPrefix := s.resolveAPIKey(keyReq)
	if keyID != record.ID || keyPrefix != truncateAPIKey(raw) {
		t.Fatalf("resolveAPIKey=(%q,%q), want (%q,%q)", keyID, keyPrefix, record.ID, truncateAPIKey(raw))
	}

	// JWT bearer：通过认证但无 store 记录，只能得到截断前缀。
	jwtReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	jwtReq.Header.Set("Authorization", "Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig")
	jwtID, jwtPrefix := s.resolveAPIKey(jwtReq)
	if jwtID != "" || jwtPrefix != "eyJhbGci..." {
		t.Fatalf("resolveAPIKey(JWT)=(%q,%q), want (\"\", eyJhbGci...)", jwtID, jwtPrefix)
	}

	now := time.Now()
	s.usage.record(UsageRecord{Time: now, APIKeyID: keyID, APIKeyPrefix: keyPrefix, Model: "m", InputTokens: 10})
	s.usage.record(UsageRecord{Time: now, APIKeyPrefix: "m365_leg...", Model: "m", InputTokens: 1})
	s.usage.record(UsageRecord{Time: now, APIKeyID: jwtID, APIKeyPrefix: jwtPrefix, Model: "m", InputTokens: 2})

	rec := httptest.NewRecorder()
	s.adminUsage(rec, httptest.NewRequest(http.MethodGet, "/api/usage?days=7", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("usage status=%d body=%s", rec.Code, rec.Body.String())
	}
	var snapshot struct {
		Stats struct {
			Keys []map[string]any `json:"keys"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	names := map[string]any{}
	for _, row := range snapshot.Stats.Keys {
		id, _ := row["api_key_id"].(string)
		names[id] = row["api_key_name"]
	}
	if names[record.ID] != "prod key" {
		t.Fatalf("key row missing api_key_name: %v", snapshot.Stats.Keys)
	}
	if _, has := names[""].(string); has {
		t.Fatalf("legacy/JWT buckets must not resolve a name: %v", snapshot.Stats.Keys)
	}

	logRec := httptest.NewRecorder()
	s.adminUsageLogs(logRec, httptest.NewRequest(http.MethodGet, "/api/usage/logs", nil))
	if logRec.Code != http.StatusOK {
		t.Fatalf("usage logs status=%d body=%s", logRec.Code, logRec.Body.String())
	}
	var logs struct {
		Logs []struct {
			APIKeyID     string `json:"api_key_id"`
			APIKeyPrefix string `json:"api_key_prefix"`
			APIKeyName   string `json:"api_key_name"`
		} `json:"logs"`
	}
	if err := json.Unmarshal(logRec.Body.Bytes(), &logs); err != nil {
		t.Fatal(err)
	}
	if len(logs.Logs) != 3 {
		t.Fatalf("expected 3 log rows, got %d: %s", len(logs.Logs), logRec.Body.String())
	}
	byID := map[string]string{}
	for _, row := range logs.Logs {
		byID[row.APIKeyID] = row.APIKeyName
	}
	if byID[record.ID] != "prod key" {
		t.Fatalf("log row for %s missing api_key_name: %s", record.ID, logRec.Body.String())
	}
	if byID[""] != "" {
		t.Fatalf("legacy/JWT log rows must omit api_key_name: %s", logRec.Body.String())
	}
}

func TestAdminUsageKeyEndpoint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("M365_USAGE_LOG", filepath.Join(dir, "usage.jsonl"))
	t.Setenv("M365_API_KEYS", filepath.Join(dir, "api-keys.json"))
	store := openAPIKeys()
	keyRec, _, err := store.create("detail key")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{usage: openUsageLog(), apiKeys: store}
	now := time.Now()
	s.usage.record(UsageRecord{Time: now, APIKeyID: keyRec.ID, APIKeyPrefix: keyRec.Prefix[:8] + "...", Model: "m", InputTokens: 10, OutputTokens: 5})
	// 升级前的旧记录：按 8 字符前缀归入该 key。
	s.usage.record(UsageRecord{Time: now, APIKeyPrefix: keyRec.Prefix[:8] + "...", Model: "m", InputTokens: 1})
	s.usage.record(UsageRecord{Time: now, APIKeyID: "other-key", Model: "m", InputTokens: 100})

	rec := httptest.NewRecorder()
	s.adminUsageKey(rec, httptest.NewRequest(http.MethodGet, "/api/usage/key?id="+keyRec.ID+"&days=7", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("usage key status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Days int `json:"days"`
		Key  struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Prefix  string `json:"prefix"`
			Revoked bool   `json:"revoked"`
		} `json:"key"`
		Stats struct {
			Summary struct {
				Requests int64 `json:"requests"`
				Tokens   int64 `json:"tokens"`
			} `json:"summary"`
			Models []map[string]any `json:"models"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Days != 7 || payload.Key.ID != keyRec.ID || payload.Key.Name != "detail key" || payload.Key.Prefix != keyRec.Prefix || payload.Key.Revoked {
		t.Fatalf("payload key=%+v days=%d", payload.Key, payload.Days)
	}
	if payload.Stats.Summary.Requests != 2 || payload.Stats.Summary.Tokens != 16 {
		t.Fatalf("stats summary=%+v, want requests=2 tokens=16", payload.Stats.Summary)
	}
	if len(payload.Stats.Models) != 1 {
		t.Fatalf("models=%v, want 1 entry", payload.Stats.Models)
	}

	notFound := httptest.NewRecorder()
	s.adminUsageKey(notFound, httptest.NewRequest(http.MethodGet, "/api/usage/key?id=nope", nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unknown id status=%d, want 404", notFound.Code)
	}

	missing := httptest.NewRecorder()
	s.adminUsageKey(missing, httptest.NewRequest(http.MethodGet, "/api/usage/key", nil))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing id status=%d, want 400", missing.Code)
	}

	badMethod := httptest.NewRecorder()
	s.adminUsageKey(badMethod, httptest.NewRequest(http.MethodPost, "/api/usage/key?id="+keyRec.ID, nil))
	if badMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status=%d, want 405", badMethod.Code)
	}
}
