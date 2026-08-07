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
	Raw        string     `json:"raw,omitempty"`
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
		p = filepath.Join(h, ".config", "m365-copilot2api", "api-keys.json")
	}
	s := &apiKeyStore{Path: p}
	b, e := os.ReadFile(p)
	if e == nil {
		_ = json.Unmarshal(b, s)
	}
	return s
}
func (s *apiKeyStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, b, 0600)
}
func keyHash(k string) string { h := sha256.Sum256([]byte(k)); return hex.EncodeToString(h[:]) }
func (s *apiKeyStore) create(name string) (apiKeyRecord, string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return apiKeyRecord{}, "", e
	}
	raw := "m365_" + hex.EncodeToString(b)
	r := apiKeyRecord{ID: hex.EncodeToString(b[:8]), Name: name, Prefix: raw[:12], Hash: keyHash(raw), Raw: raw, CreatedAt: time.Now()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Keys = append(s.Keys, r)
	if err := s.save(); err != nil {
		s.Keys = s.Keys[:len(s.Keys)-1]
		return apiKeyRecord{}, "", err
	}
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
func (s *apiKeyStore) revoke(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Keys {
		if s.Keys[i].ID == id && !s.Keys[i].Revoked {
			s.Keys[i].Revoked = true
			if err := s.save(); err != nil {
				s.Keys[i].Revoked = false
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// delete 物理删除一条 API Key（与 revoke 不同，revoke 仅标记禁用）。
func (s *apiKeyStore) delete(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Keys {
		if s.Keys[i].ID == id {
			removed := s.Keys[i]
			s.Keys = append(s.Keys[:i], s.Keys[i+1:]...)
			if err := s.save(); err != nil {
				// 保存失败：恢复原列表（保留顺序，重新插入）
				s.Keys = append(s.Keys[:i], append([]apiKeyRecord{removed}, s.Keys[i:]...)...)
				_ = s.save()
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

func (s *apiKeyStore) update(id, name string, revoked *bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Keys {
		if s.Keys[i].ID != id {
			continue
		}
		if name != "" {
			s.Keys[i].Name = name
		}
		if revoked != nil {
			s.Keys[i].Revoked = *revoked
		}
		if err := s.save(); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}
func (s *apiKeyStore) valid(raw string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := keyHash(raw)
	for i := range s.Keys {
		if s.Keys[i].Hash == h && !s.Keys[i].Revoked {
			now := time.Now()
			s.Keys[i].LastUsedAt = &now
			_ = s.save()
			return true
		}
	}
	return false
}
