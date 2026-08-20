package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type AccountToken struct {
	ID               string    `json:"id"`
	Email            string    `json:"email"`
	DisplayName      string    `json:"displayName,omitempty"`
	Status           string    `json:"status"`
	ScheduleDisabled bool      `json:"scheduleDisabled,omitempty"`
	AccessToken      string    `json:"accessToken"`
	RefreshToken     string    `json:"refreshToken,omitempty"`
	ExpiresAt        time.Time `json:"expiresAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	OID              string    `json:"oid,omitempty"`
	TID              string    `json:"tid,omitempty"`
	ClientID         string    `json:"clientId,omitempty"`
	BoundProxy       string    `json:"boundProxy,omitempty"`
	Priority         int       `json:"priority,omitempty"`
}

type Cache struct {
	Accounts []AccountToken `json:"accounts"`
}

type Store struct {
	mu       sync.Mutex
	path     string
	data     Cache
	nextIdx  int
	inflight map[string]*inflightRefresh
}

// inflightRefresh coalesces concurrent EnsureValid refreshes for the same
// account: AAD refresh tokens can only be redeemed once, so a stampede of
// concurrent requests must not each try Refresh().
type inflightRefresh struct {
	done chan struct{}
	acc  AccountToken
	err  error
}

func CachePath() string {
	if dir := os.Getenv("M365_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "accounts.json")
	}
	if p := os.Getenv("M365_CONFIG"); p != "" {
		return p
	}
	if p := os.Getenv("M365_TOKEN_CACHE"); p != "" {
		return p
	}
	if p := os.Getenv("M365_TOKEN_FILE"); p != "" {
		return p
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return filepath.Join(".", ".config", "m365-copilot2api", "accounts.json")
	}
	return filepath.Join(h, ".config", "m365-copilot2api", "accounts.json")
}

func OpenStore(path string) (*Store, error) {
	if path == "" {
		path = CachePath()
	}
	s := &Store{path: path, data: Cache{Accounts: []AccountToken{}}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return nil, err
	}
	// Normalize oid/tid for older cache entries.
	for i := range s.data.Accounts {
		a := &s.data.Accounts[i]
		if a.OID == "" {
			a.OID = a.ID
		}
		if a.ID == "" {
			a.ID = a.OID
		}
	}
	return s, nil
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		// /tmp has no nested dir needs usually; ignore if parent is root-ish
		if filepath.Dir(s.path) != "/" && filepath.Dir(s.path) != "." {
			// still try write below
		}
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(s.path, b, 0o600)
}

func atomicWrite(path string, b []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Store) List() []AccountToken {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AccountToken, len(s.data.Accounts))
	copy(out, s.data.Accounts)
	return out
}

func (s *Store) SetScheduleEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			s.data.Accounts[i].ScheduleDisabled = !enabled
			s.data.Accounts[i].UpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return errors.New("account not found")
}

func (s *Store) ScheduleEnabled(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, account := range s.data.Accounts {
		if account.ID == id {
			return !account.ScheduleDisabled
		}
	}
	return false
}

func (s *Store) UpdateRefreshToken(id, refreshToken string) error {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			s.data.Accounts[i].RefreshToken = refreshToken
			s.data.Accounts[i].UpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return errors.New("account not found")
}

func (s *Store) Upsert(tok TokenSet) (AccountToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := tok.HomeOID
	if id == "" {
		id = tok.Email
	}
	if id == "" {
		id = "account-" + time.Now().Format("150405")
	}
	acc := AccountToken{
		ID:           id,
		Email:        tok.Email,
		DisplayName:  tok.DisplayName,
		Status:       "online",
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.ExpiresAt,
		UpdatedAt:    time.Now(),
		OID:          firstNonEmpty(tok.HomeOID, id),
		TID:          tok.TenantID,
		ClientID:     ClientID(),
	}
	found := false
	for i, existing := range s.data.Accounts {
		if existing.ID == acc.ID || (acc.Email != "" && existing.Email == acc.Email) {
			if acc.RefreshToken == "" {
				acc.RefreshToken = existing.RefreshToken
			}
			if acc.TID == "" {
				acc.TID = existing.TID
			}
			if acc.OID == "" {
				acc.OID = existing.OID
			}
			acc.ScheduleDisabled = existing.ScheduleDisabled
			acc.Priority = existing.Priority
			if acc.BoundProxy == "" {
				acc.BoundProxy = existing.BoundProxy
			}
			s.data.Accounts[i] = acc
			found = true
			break
		}
	}
	if !found {
		s.data.Accounts = append(s.data.Accounts, acc)
	}
	return acc, s.saveLocked()
}

func (s *Store) SetPriority(id string, priority int) error {
	if priority < 0 {
		return errors.New("priority must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			s.data.Accounts[i].Priority = priority
			s.data.Accounts[i].UpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return errors.New("account not found")
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data.Accounts[:0]
	for _, a := range s.data.Accounts {
		if a.ID != id {
			next = append(next, a)
		}
	}
	s.data.Accounts = next
	return s.saveLocked()
}

func (s *Store) SetBoundProxy(id, proxyURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Accounts {
		if s.data.Accounts[i].ID == id {
			s.data.Accounts[i].BoundProxy = proxyURL
			s.data.Accounts[i].UpdatedAt = time.Now()
			return s.saveLocked()
		}
	}
	return errors.New("account not found")
}

func (s *Store) Get(id string) (AccountToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.data.Accounts {
		if a.ID == id || a.OID == id || a.Email == id {
			return a, true
		}
	}
	return AccountToken{}, false
}

func (s *Store) First() (AccountToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data.Accounts) == 0 {
		return AccountToken{}, false
	}
	return s.data.Accounts[0], true
}

// Next returns the next account in round-robin order.
func (s *Store) Next() (AccountToken, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.data.Accounts)
	if n == 0 {
		return AccountToken{}, false
	}
	acc := s.data.Accounts[s.nextIdx%n]
	s.nextIdx = (s.nextIdx + 1) % n
	return acc, true
}

func (s *Store) EnsureValid(id string) (AccountToken, error) {
	acc, ok := s.Get(id)
	if !ok {
		return AccountToken{}, os.ErrNotExist
	}
	if time.Now().Before(acc.ExpiresAt.Add(-30 * time.Second)) {
		return acc, nil
	}
	if acc.RefreshToken == "" {
		acc.Status = "expired"
		s.mu.Lock()
		for i, a := range s.data.Accounts {
			if a.ID == acc.ID {
				s.data.Accounts[i] = acc
				_ = s.saveLocked()
				break
			}
		}
		s.mu.Unlock()
		return acc, fmtExpired()
	}
	return s.refreshInflight(acc)
}

// refreshInflight runs the AAD token refresh exactly once per account; waiters
// block on the shared flight instead of redeeming the one-time refresh token
// themselves. The winner's outcome is broadcast to all waiters.
func (s *Store) refreshInflight(acc AccountToken) (AccountToken, error) {
	s.mu.Lock()
	if s.inflight == nil {
		s.inflight = map[string]*inflightRefresh{}
	}
	if f, ok := s.inflight[acc.ID]; ok {
		s.mu.Unlock()
		<-f.done
		return f.acc, f.err
	}
	f := &inflightRefresh{done: make(chan struct{})}
	s.inflight[acc.ID] = f
	s.mu.Unlock()

	tok, err := Refresh(acc.RefreshToken)
	if err != nil {
		acc.Status = "expired"
		s.mu.Lock()
		for i, a := range s.data.Accounts {
			if a.ID == acc.ID {
				s.data.Accounts[i] = acc
				_ = s.saveLocked()
				break
			}
		}
		s.mu.Unlock()
		f.acc, f.err = acc, err
	} else {
		if tok.Email == "" {
			tok.Email = acc.Email
		}
		if tok.DisplayName == "" {
			tok.DisplayName = acc.DisplayName
		}
		if tok.HomeOID == "" {
			tok.HomeOID = firstNonEmpty(acc.OID, acc.ID)
		}
		if tok.TenantID == "" {
			tok.TenantID = acc.TID
		}
		f.acc, f.err = s.Upsert(tok)
	}
	close(f.done)
	s.mu.Lock()
	delete(s.inflight, acc.ID)
	s.mu.Unlock()
	return f.acc, f.err
}

func fmtExpired() error {
	return errors.New("token_expired: refresh token missing or expired")
}

func (s *Store) RefreshAllExpired() []TokenRefreshResult {
	s.mu.Lock()
	candidates := make([]AccountToken, 0, len(s.data.Accounts))
	for _, a := range s.data.Accounts {
		if time.Now().After(a.ExpiresAt.Add(-30*time.Second)) && a.RefreshToken != "" {
			candidates = append(candidates, a)
		}
	}
	s.mu.Unlock()
	var results []TokenRefreshResult
	for _, a := range candidates {
		acc, err := s.EnsureValid(a.ID)
		r := TokenRefreshResult{ID: a.ID, Email: a.Email}
		if err != nil {
			r.Success = false
			r.Error = err.Error()
		} else {
			r.Success = true
			r.ExpiresAt = acc.ExpiresAt
		}
		results = append(results, r)
	}
	return results
}

type TokenRefreshResult struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}
