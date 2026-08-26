package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

type inflightRefresh struct {
	done chan struct{}
	acc  AccountToken
	err  error
}

func cryptoRandUint16() uint16 {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint16(b[:])
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

// TODO(security): 当前使用 AES-GCM + pepper HMAC-SHA256 派生 (M365_MASTER_KEY) 提供 AEAD 加密与 0600 落盘，
// 兼容明文迁移。未来迁移到 XChaCha20-Poly1305 (golang.org/x/crypto/chacha20poly1305.NewX, 24-byte nonce)
// 并将主密钥接入 OS DPAPI/keyring (Windows DPAPI, macOS Keychain, Linux libsecret)，见 TODO 后续。
const encPrefix = "enc:v1:"

func masterKey() []byte {
	raw := strings.TrimSpace(os.Getenv("M365_MASTER_KEY"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("M365_TOKEN_ENCRYPTION_KEY"))
	}
	if raw == "" {
		log.Printf("[security] WARNING: M365_MASTER_KEY not set; refresh tokens are encrypted with a built-in public fallback key. Set M365_MASTER_KEY to protect accounts.json at rest.")
		raw = "m365-copilot2api-fallback-pepper-v1-TODO-DPAPI-keyring"
	}
	pepper := []byte("m365-copilot2api-pepper-v1")
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(raw))
	return mac.Sum(nil)
}

func isEncrypted(s string) bool { return strings.HasPrefix(s, encPrefix) }

func encryptRefreshToken(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if isEncrypted(plain) {
		return plain, nil
	}
	key := masterKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ct), nil
}

func decryptRefreshToken(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	if !isEncrypted(enc) {
		return enc, nil
	}
	raw := strings.TrimPrefix(enc, encPrefix)
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		if b2, err2 := base64.RawStdEncoding.DecodeString(raw); err2 == nil {
			b = b2
		} else {
			return "", err
		}
	}
	key := masterKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(b) < gcm.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := b[:gcm.NonceSize()], b[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func writeFileAtomic(path string, b []byte, perm os.FileMode) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cleanupStaleTmp(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()
	if err := tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	_ = fsyncDir(dir)
	return nil
}

func cleanupStaleTmp(path string) {
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	for _, pat := range []string{filepath.Join(dir, "."+base+".tmp.*"), filepath.Join(dir, base+".tmp.*")} {
		if matches, _ := filepath.Glob(pat); matches != nil {
			for _, m := range matches {
				_ = os.Remove(m)
			}
		}
	}
	_ = os.Remove(path + ".tmp")
}

func OpenStore(path string) (*Store, error) {
	if path == "" {
		path = CachePath()
	}
	cleanupStaleTmp(path)
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
	for i := range s.data.Accounts {
		a := &s.data.Accounts[i]
		if dec, err := decryptRefreshToken(a.RefreshToken); err == nil {
			a.RefreshToken = dec
		} else if isEncrypted(a.RefreshToken) {
			log.Printf("[security] WARNING: failed to decrypt refresh token for account %s (email=%s): %v. Token kept as-is; refresh will fail until M365_MASTER_KEY matches the encryption key.", a.ID, a.Email, err)
		}
		if a.OID == "" {
			a.OID = a.ID
		}
		if a.ID == "" {
			a.ID = a.OID
		}
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		if filepath.Dir(s.path) != "/" && filepath.Dir(s.path) != "." {
		}
	}
	encData := Cache{Accounts: make([]AccountToken, len(s.data.Accounts))}
	for i, a := range s.data.Accounts {
		encData.Accounts[i] = a
		if a.RefreshToken != "" {
			enc, err := encryptRefreshToken(a.RefreshToken)
			if err != nil {
				return err
			}
			encData.Accounts[i].RefreshToken = enc
		}
	}
	b, err := json.MarshalIndent(encData, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, b, 0o600)
}

func atomicWrite(path string, b []byte, perm os.FileMode) error {
	return writeFileAtomic(path, b, perm)
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
		id = fmt.Sprintf("account-%s-%04x", time.Now().Format("150405"), cryptoRandUint16())
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
	s.mu.Lock()
	var acc AccountToken
	found := false
	for _, a := range s.data.Accounts {
		if a.ID == id || a.OID == id || a.Email == id {
			if !found || a.UpdatedAt.After(acc.UpdatedAt) {
				acc = a
				found = true
			}
		}
	}
	if !found {
		s.mu.Unlock()
		return AccountToken{}, os.ErrNotExist
	}
	remaining := acc.ExpiresAt.Sub(time.Now())
	threshold := 120 * time.Second
	if total := acc.ExpiresAt.Sub(acc.UpdatedAt); total > 0 {
		if t := total / 10; t < threshold {
			threshold = t
		}
	}
	if remaining > threshold {
		s.mu.Unlock()
		return acc, nil
	}
	if acc.RefreshToken == "" {
		for i, a := range s.data.Accounts {
			if a.ID == acc.ID {
				s.data.Accounts[i].Status = "expired"
				_ = s.saveLocked()
				break
			}
		}
		s.mu.Unlock()
		acc.Status = "expired"
		return acc, fmtExpired()
	}
	s.mu.Unlock()
	return s.refreshInflight(acc)
}

// EnsureValidFor preserves the pre-v0.5 refresh API used by the background
// prefetcher while the latest cache implementation keeps one standard window.
func (s *Store) EnsureValidFor(id string, minValidity time.Duration) (AccountToken, error) {
	window := minValidity
	if window < 30*time.Second {
		window = 30 * time.Second
	}
	s.mu.Lock()
	var acc AccountToken
	for _, a := range s.data.Accounts {
		if a.ID == id || a.OID == id || a.Email == id {
			acc = a
			break
		}
	}
	s.mu.Unlock()
	if acc.ID == "" {
		return AccountToken{}, os.ErrNotExist
	}
	if time.Now().Before(acc.ExpiresAt.Add(-window)) {
		return acc, nil
	}
	return s.refreshInflight(acc)
}

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
	endpoint := TokenEndpoint()
	if acc.ClientID == DeviceClientID() {
		endpoint = DeviceTokenEndpoint()
	}
	if acc.TID != "" && acc.ClientID != DeviceClientID() && strings.Contains(endpoint, "/common/") {
		endpoint = strings.Replace(endpoint, "/common/", "/"+acc.TID+"/", 1)
	}
	tok, err := Refresh(acc.RefreshToken, acc.ClientID, endpoint, acc.OID, acc.TID)
	if err != nil {
		s.mu.Lock()
		for i, a := range s.data.Accounts {
			if a.ID == acc.ID {
				s.data.Accounts[i].Status = "expired"
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
		updated, upErr := s.Upsert(tok)
		if upErr == nil {
			s.mu.Lock()
			for i := range s.data.Accounts {
				if s.data.Accounts[i].ID == acc.ID || s.data.Accounts[i].OID == acc.OID || (acc.Email != "" && s.data.Accounts[i].Email == acc.Email) {
					s.data.Accounts[i].AccessToken = tok.AccessToken
					s.data.Accounts[i].ExpiresAt = tok.ExpiresAt
					updated = s.data.Accounts[i]
					break
				}
			}
			_ = s.saveLocked()
			s.mu.Unlock()
		}
		f.acc, f.err = updated, upErr
	}
	close(f.done)
	s.mu.Lock()
	delete(s.inflight, acc.ID)
	s.mu.Unlock()
	return f.acc, f.err
}

func fmtExpired() error { return errors.New("token_expired: refresh token missing or expired") }

func (s *Store) RefreshAllExpired() []TokenRefreshResult {
	return s.RefreshAllDue(30*time.Second, 4)
}

// RefreshAllDue refreshes accounts whose access token has less than
// minValidity remaining. Refreshes run concurrently across accounts, while
// refreshInflight still coalesces duplicate work for the same account.
func (s *Store) RefreshAllDue(minValidity time.Duration, maxConcurrent int) []TokenRefreshResult {
	if minValidity < 30*time.Second {
		minValidity = 30 * time.Second
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	s.mu.Lock()
	candidates := make([]AccountToken, 0, len(s.data.Accounts))
	for _, a := range s.data.Accounts {
		if time.Now().After(a.ExpiresAt.Add(-minValidity)) && a.RefreshToken != "" {
			candidates = append(candidates, a)
		}
	}
	s.mu.Unlock()
	if len(candidates) == 0 {
		return []TokenRefreshResult{}
	}
	results := make([]TokenRefreshResult, len(candidates))
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	for i, a := range candidates {
		wg.Add(1)
		go func(i int, a AccountToken) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			acc, err := s.EnsureValidFor(a.ID, minValidity)
			if err == nil && acc.AccessToken == "" {
				err = errors.New("empty access token after refresh")
			}
			r := TokenRefreshResult{ID: a.ID, Email: a.Email}
			if err != nil {
				r.Error = err.Error()
			} else {
				r.Success = true
				r.ExpiresAt = acc.ExpiresAt
			}
			results[i] = r
		}(i, a)
	}
	wg.Wait()
	return results
}

type TokenRefreshResult struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}
