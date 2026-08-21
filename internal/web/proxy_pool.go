package web

import (
	"encoding/json"
	"m365-copilot2api/internal/outbound"
	"net/http"
	"strings"
)

func (s *Server) persistProxyPool() error {
	v := s.settings.get()
	items := outbound.ProxyPoolStatus()
	v.ProxyPool = make([]string, 0, len(items))
	for _, item := range items {
		if raw, ok := item["url"].(string); ok {
			v.ProxyPool = append(v.ProxyPool, raw)
		}
	}
	return s.settings.save(v)
}

func (s *Server) proxyPool(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && r.URL.Query().Get("action") == "check" {
		p := outbound.CurrentPool()
		if p == nil {
			jsonOut(w, map[string]any{"ok": true, "proxies": []map[string]any{}})
			return
		}
		jsonOut(w, map[string]any{"ok": true, "proxies": p.CheckAll(r.Context())})
		return
	}
	switch r.Method {
	case http.MethodGet:
		jsonOut(w, map[string]any{"proxies": outbound.ProxyPoolStatus()})
	case http.MethodPost:
		var body struct {
			URL  string   `json:"url"`
			URLs []string `json:"urls"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body) != nil {
			writeOpenAIError(w, 400, "invalid_request_error", "invalid_json", "bad json")
			return
		}
		urls := append(body.URLs, body.URL)
		added := 0
		for _, raw := range urls {
			for _, v := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
				if strings.TrimSpace(v) == "" {
					continue
				}
				if err := outbound.AddProxy(strings.TrimSpace(v)); err != nil {
					writeOpenAIError(w, 400, "invalid_request_error", "invalid_parameter", err.Error())
					return
				}
				if err := s.persistProxyPool(); err != nil {
					writeOpenAIError(w, 500, "storage_error", "storage_error", err.Error())
					return
				}
				added++
			}
		}
		jsonOut(w, map[string]any{"ok": true, "added": added, "proxies": outbound.ProxyPoolStatus()})
	case http.MethodDelete:
		raw := strings.TrimRight(strings.TrimSpace(r.URL.Query().Get("url")), "/")
		if raw == "" {
			if err := outbound.ConfigurePool(nil); err != nil {
				writeOpenAIError(w, 400, "invalid_request_error", "invalid_parameter", err.Error())
				return
			}
		} else if err := outbound.RemoveProxy(raw); err != nil {
			writeOpenAIError(w, 400, "invalid_request_error", "invalid_parameter", err.Error())
			return
		}
		if err := s.persistProxyPool(); err != nil {
			writeOpenAIError(w, 500, "storage_error", "storage_error", err.Error())
			return
		}
		jsonOut(w, map[string]any{"ok": true, "proxies": outbound.ProxyPoolStatus()})
	default:
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "method not allowed")
	}
}
