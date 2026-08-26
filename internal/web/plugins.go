package web

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type pluginCacheEntry struct {
	data      json.RawMessage
	fetchedAt time.Time
}

type pluginCache struct {
	mu      sync.Mutex
	entries map[string]pluginCacheEntry
}

var globalPluginCache = &pluginCache{entries: map[string]pluginCacheEntry{}}

const pluginCacheTTL = 5 * time.Minute

func (pc *pluginCache) get(accountID string) (json.RawMessage, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	e, ok := pc.entries[accountID]
	if !ok || time.Since(e.fetchedAt) > pluginCacheTTL {
		return nil, false
	}
	return e.data, true
}

func (pc *pluginCache) set(accountID string, data json.RawMessage) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.entries[accountID] = pluginCacheEntry{data: data, fetchedAt: time.Now()}
}

func (s *Server) fetchPlugins(accessToken string) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodGet, "https://substrate.office.com/m365Copilot/EventListener/Client?EventId=ExecuteAction", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:148.0) Gecko/20100101 Firefox/148.0")
	resp, err := s.chat.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("[plugins] upstream %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
		return nil, &UpstreamHTTPError{Status: resp.StatusCode, Body: string(body)}
	}
	return json.RawMessage(body), nil
}

func (s *Server) plugins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	if !s.validAPIKey(r) {
		writeOpenAIError(w, http.StatusUnauthorized, "invalid_request_error", "invalid API key")
		return
	}
	accountID := r.URL.Query().Get("account")
	acc, err := s.resolveAccount(accountID)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	if cached, ok := globalPluginCache.get(acc.ID); ok {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Cache", "HIT")
		w.Write(cached)
		return
	}
	data, err := s.fetchPlugins(acc.AccessToken)
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	globalPluginCache.set(acc.ID, data)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", "MISS")
	w.Write(data)
}
