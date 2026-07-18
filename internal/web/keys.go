package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type apiKeyRecord struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Hash       string     `json:"hash"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	Revoked    bool       `json:"revoked"`
}
type apiKeyStore struct {
	mu   sync.Mutex
	Path string
	Keys []apiKeyRecord `json:"keys"`
}

func openAPIKeys() *apiKeyStore {
	p := strings.TrimSpace(os.Getenv("M365_API_KEYS"))
	if p == "" {
		h, _ := os.UserHomeDir()
		p = filepath.Join(h, ".config", "m365-native", "api-keys.json")
	}
	s := &apiKeyStore{Path: p}
	b, e := os.ReadFile(p)
	if e == nil {
		_ = json.Unmarshal(b, s)
	}
	return s
}
func (s *apiKeyStore) save() {
	_ = os.MkdirAll(filepath.Dir(s.Path), 0700)
	b, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(s.Path, b, 0600)
}
func keyHash(k string) string { h := sha256.Sum256([]byte(k)); return hex.EncodeToString(h[:]) }
func (s *apiKeyStore) create(name string) (apiKeyRecord, string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return apiKeyRecord{}, "", e
	}
	raw := "m365_" + hex.EncodeToString(b)
	r := apiKeyRecord{ID: hex.EncodeToString(b[:8]), Name: name, Prefix: raw[:12], Hash: keyHash(raw), CreatedAt: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Keys = append(s.Keys, r)
	s.save()
	return r, raw, nil
}
func (s *apiKeyStore) list() []apiKeyRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]apiKeyRecord, len(s.Keys))
	copy(out, s.Keys)
	for i := range out {
		out[i].Hash = ""
	}
	return out
}
func (s *apiKeyStore) revoke(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Keys {
		if s.Keys[i].ID == id && !s.Keys[i].Revoked {
			s.Keys[i].Revoked = true
			s.save()
			return true
		}
	}
	return false
}
func (s *apiKeyStore) valid(raw string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := keyHash(raw)
	for i := range s.Keys {
		if s.Keys[i].Hash == h && !s.Keys[i].Revoked {
			now := time.Now()
			s.Keys[i].LastUsedAt = &now
			s.save()
			return true
		}
	}
	return false
}
