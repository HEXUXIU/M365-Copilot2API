package web

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log"
	"m365-copilot2api/internal/auth"
	"m365-copilot2api/internal/outbound"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const substrateBase = "https://substrate.office.com"

var flagsCache = &struct {
	sync.Mutex
	m map[string]flagsCacheEntry
}{m: map[string]flagsCacheEntry{}}

type flagsCacheEntry struct {
	body      []byte
	fetchedAt time.Time
}

func (s *Server) memoryAccount(r *http.Request) (auth.AccountToken, bool) {
	acc, err := s.resolveAccount("")
	if err != nil {
		return auth.AccountToken{}, false
	}
	if acc.OID == "" || acc.TID == "" {
		if o, t := extractOIDTID(acc.AccessToken); o != "" {
			acc.OID, acc.TID = o, t
		}
	}
	if acc.OID == "" || acc.TID == "" {
		return auth.AccountToken{}, false
	}
	return acc, true
}

func substrateHeaders(acc auth.AccountToken) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+acc.AccessToken)
	h.Set("x-anchormailbox", "Oid:"+acc.OID+"@"+acc.TID)
	h.Set("x-routingparameter-sessionkey", acc.OID)
	h.Set("x-scenario", "OfficeWebIncludedCopilot")
	b := make([]byte, 16)
	rand.Read(b)
	h.Set("x-clientrequestid", hex.EncodeToString(b))
	h.Set("Content-Type", "application/json")
	return h
}

func proxySubstrate(w http.ResponseWriter, targetURL string, method string, acc auth.AccountToken, body io.Reader) {
	req, err := http.NewRequest(method, targetURL, body)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "failed to create substrate request")
		return
	}
	req.Header = substrateHeaders(acc)
	resp, err := outbound.HTTPClient().Do(req)
	if err != nil {
		log.Printf("[memory-proxy] substrate error url=%s err=%v", targetURL, err)
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "substrate request failed")
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "substrate read failed")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-M365-Account", acc.ID)
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

func (s *Server) memoryGetFlags(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.memoryAccount(r)
	if !ok {
		writeOpenAIError(w, http.StatusBadGateway, "account_error", "no account available")
		return
	}
	flagsCache.Lock()
	entry, cached := flagsCache.m[acc.ID]
	flagsCache.Unlock()
	if cached && time.Since(entry.fetchedAt) < 60*time.Second {
		w.Header().Set("Content-Type", "application/json")
		w.Write(entry.body)
		return
	}
	target := substrateBase + "/m365Copilot/PersonalizationUserFlags?variants=feature.EnablePersonalization"
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "failed to create substrate request")
		return
	}
	req.Header = substrateHeaders(acc)
	resp, err := outbound.HTTPClient().Do(req)
	if err != nil {
		log.Printf("[memory-flags] substrate error err=%v", err)
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "substrate request failed")
		return
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "substrate read failed")
		return
	}
	if resp.StatusCode == 200 {
		flagsCache.Lock()
		flagsCache.m[acc.ID] = flagsCacheEntry{body: respBody, fetchedAt: time.Now()}
		flagsCache.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

func (s *Server) memoryPatchFlags(w http.ResponseWriter, r *http.Request) {
	if !s.validAdminSession(r) {
		writeOpenAIError(w, http.StatusForbidden, "insufficient_permission", "administrator login required for this operation; sign in via /api/admin/login and retry with the admin session cookie")
		return
	}
	acc, ok := s.memoryAccount(r)
	if !ok {
		writeOpenAIError(w, http.StatusBadGateway, "account_error", "no account available")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	target := substrateBase + "/m365Copilot/PersonalizationUserFlags?variants=feature.EnablePersonalization"
	proxySubstrate(w, target, http.MethodPost, acc, r.Body)
	flagsCache.Lock()
	delete(flagsCache.m, acc.ID)
	flagsCache.Unlock()
}

func (s *Server) memoryGetInstructions(w http.ResponseWriter, r *http.Request) {
	acc, ok := s.memoryAccount(r)
	if !ok {
		writeOpenAIError(w, http.StatusBadGateway, "account_error", "no account available")
		return
	}
	target := substrateBase + "/m365Copilot/CustomInstructions?variants=feature.EnablePersonalization"
	proxySubstrate(w, target, http.MethodGet, acc, nil)
}

func (s *Server) memoryPutInstructions(w http.ResponseWriter, r *http.Request) {
	if !s.validAdminSession(r) {
		writeOpenAIError(w, http.StatusForbidden, "insufficient_permission", "administrator login required for this operation; sign in via /api/admin/login and retry with the admin session cookie")
		return
	}
	acc, ok := s.memoryAccount(r)
	if !ok {
		writeOpenAIError(w, http.StatusBadGateway, "account_error", "no account available")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	target := substrateBase + "/m365Copilot/CustomInstructions?variants=feature.EnablePersonalization"
	proxySubstrate(w, target, http.MethodPost, acc, r.Body)
}

func (s *Server) memoryDeleteInstruction(w http.ResponseWriter, r *http.Request) {
	if !s.validAdminSession(r) {
		writeOpenAIError(w, http.StatusForbidden, "insufficient_permission", "administrator login required for this operation; sign in via /api/admin/login and retry with the admin session cookie")
		return
	}
	acc, ok := s.memoryAccount(r)
	if !ok {
		writeOpenAIError(w, http.StatusBadGateway, "account_error", "no account available")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/memory/instructions/")
	if id == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "instruction id required")
		return
	}
	encodedID := url.PathEscape(id)
	target := substrateBase + "/m365Copilot/CustomInstructions/" + encodedID + "?variants=feature.EnablePersonalization"
	proxySubstrate(w, target, http.MethodDelete, acc, nil)
}

func (s *Server) memoryPatchSettings(w http.ResponseWriter, r *http.Request) {
	if !s.validAdminSession(r) {
		writeOpenAIError(w, http.StatusForbidden, "insufficient_permission", "administrator login required for this operation; sign in via /api/admin/login and retry with the admin session cookie")
		return
	}
	acc, ok := s.memoryAccount(r)
	if !ok {
		writeOpenAIError(w, http.StatusBadGateway, "account_error", "no account available")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	target := substrateBase + "/puds/v1/me/settings/copilot"
	proxySubstrate(w, target, http.MethodPatch, acc, r.Body)
}

func (s *Server) handleMemoryFlags(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.memoryGetFlags(w, r)
	case http.MethodPatch:
		s.memoryPatchFlags(w, r)
	default:
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

func (s *Server) handleMemoryInstructions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.memoryGetInstructions(w, r)
	case http.MethodPut:
		s.memoryPutInstructions(w, r)
	default:
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}

func (s *Server) handleMemoryInstructionsID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	s.memoryDeleteInstruction(w, r)
}

func (s *Server) handleMemorySettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	s.memoryPatchSettings(w, r)
}

func initMemoryFlagsCacheGC() {
	go func() {
		for {
			time.Sleep(60 * time.Second)
			flagsCache.Lock()
			now := time.Now()
			for k, v := range flagsCache.m {
				if now.Sub(v.fetchedAt) > 120*time.Second {
					delete(flagsCache.m, k)
				}
			}
			flagsCache.Unlock()
		}
	}()
}

func init() {
	initMemoryFlagsCacheGC()
}
