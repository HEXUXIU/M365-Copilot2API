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
	mu      sync.Mutex
	Path    string         `json:"-"`
	Keys    []apiKeyRecord `json:"keys"`
	persist *persistStore
}

func newAPIKeyStore(path string) *apiKeyStore {
	s := &apiKeyStore{Path: path}
	s.persist = &persistStore{flush: s.flush}
	return s
}

func openAPIKeys() *apiKeyStore {
	p := strings.TrimSpace(os.Getenv("M365_API_KEYS"))
	if p == "" {
		h, _ := os.UserHomeDir()
		p = filepath.Join(h, ".config", "m365-copilot2api", "api-keys.json")
	}
	s := newAPIKeyStore(p)
	b, e := os.ReadFile(p)
	if e == nil && json.Unmarshal(b, s) == nil {
		// Local extension: keep stored Raw keys so the console can re-copy
		// them later. Upstream vanilla wipes Raw on load; we deliberately
		// persist it (file perms 0600, admin-only API).
		for i := range s.Keys {
			if s.Keys[i].Raw != "" && s.Keys[i].Hash == "" {
				s.Keys[i].Hash = keyHash(s.Keys[i].Raw)
			}
		}
	}
	return s
}
func (s *apiKeyStore) flush() error {
	s.mu.Lock()
	b, err := json.MarshalIndent(s, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	return writeFileAtomic(s.Path, b, 0600)
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
	s.Keys = append(s.Keys, r)
	s.mu.Unlock()
		if err := s.persist.flushNowBlocking(); err != nil {
			s.mu.Lock()
			s.Keys = s.Keys[:len(s.Keys)-1]
			s.mu.Unlock()
			return apiKeyRecord{}, "", err
		}
		r.Hash = ""
		r.Raw = ""
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
	for i := range s.Keys {
		if s.Keys[i].ID == id && !s.Keys[i].Revoked {
			s.Keys[i].Revoked = true
			s.mu.Unlock()
			if err := s.persist.flushNowBlocking(); err != nil {
				s.mu.Lock()
				s.Keys[i].Revoked = false
				s.mu.Unlock()
				return false, err
			}
			return true, nil
		}
	}
	s.mu.Unlock()
	return false, nil
}

// delete physically removes a key record, rolling back on persistence failure.
func (s *apiKeyStore) delete(id string) (bool, error) {
	s.mu.Lock()
	for i := range s.Keys {
		if s.Keys[i].ID != id {
			continue
		}
		removed := s.Keys[i]
		s.Keys = append(s.Keys[:i], s.Keys[i+1:]...)
		s.mu.Unlock()
		if err := s.persist.flushNowBlocking(); err != nil {
			s.mu.Lock()
			s.Keys = append(s.Keys[:i], append([]apiKeyRecord{removed}, s.Keys[i:]...)...)
			s.mu.Unlock()
			return false, err
		}
		return true, nil
	}
	s.mu.Unlock()
	return false, nil
}

func (s *apiKeyStore) update(id, name string, revoked *bool) (bool, error) {
	s.mu.Lock()
	found := false
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
		found = true
		break
	}
	s.mu.Unlock()
	if !found {
		return false, nil
	}
	if err := s.persist.flushNowBlocking(); err != nil {
		return false, err
	}
	return true, nil
}
func (s *apiKeyStore) valid(raw string) bool {
	s.mu.Lock()
	h := keyHash(raw)
	found := false
	for i := range s.Keys {
		if s.Keys[i].Hash == h && !s.Keys[i].Revoked {
			now := time.Now()
			s.Keys[i].LastUsedAt = &now
			found = true
			break
		}
	}
	s.mu.Unlock()
	if found {
		s.persist.markDirty()
	}
	return found
}

// lookup 按 sha256 查找 key 的副本（含已 revoked 的 key，用量归因需要）。
// 不触碰 LastUsedAt —— 该字段由 valid() 维护。
func (s *apiKeyStore) lookup(raw string) (apiKeyRecord, bool) {
	if raw == "" {
		return apiKeyRecord{}, false
	}
	h := keyHash(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Keys {
		if s.Keys[i].Hash == h {
			rec := s.Keys[i]
			rec.Hash = ""
			return rec, true
		}
	}
	return apiKeyRecord{}, false
}

// byID 返回指定 ID 的 key 副本。
func (s *apiKeyStore) byID(id string) (apiKeyRecord, bool) {
	if id == "" {
		return apiKeyRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Keys {
		if s.Keys[i].ID == id {
			rec := s.Keys[i]
			rec.Hash = ""
			return rec, true
		}
	}
	return apiKeyRecord{}, false
}

// resolveName 把 key ID（新记录）或旧版截断前缀（"m365_ab1..." = 前 8 字符 + "..."，
// 升级前 usage.jsonl 的格式）解析成 key 当前名称。改名即时生效；解析失败返回 ""。
// store 内前缀均为 m365_ 开头，JWT（eyJ...）前缀不会误匹配。
func (s *apiKeyStore) resolveName(id, legacyPrefix string) string {
	if id != "" {
		if rec, ok := s.byID(id); ok {
			return rec.Name
		}
		return ""
	}
	if len(legacyPrefix) != 11 || !strings.HasSuffix(legacyPrefix, "...") {
		return ""
	}
	base := legacyPrefix[:8]
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Keys {
		if strings.HasPrefix(s.Keys[i].Prefix, base) {
			return s.Keys[i].Name
		}
	}
	return ""
}
