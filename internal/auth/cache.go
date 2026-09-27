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
	ID                string    `json:"id"`
	Email             string    `json:"email"`
	DisplayName       string    `json:"displayName,omitempty"`
	Status            string    `json:"status"`
	ScheduleDisabled  bool      `json:"scheduleDisabled,omitempty"`
	WebSearchDisabled bool      `json:"webSearchDisabled,omitempty"`
	SystemPrompt      string    `json:"systemPrompt,omitempty"`
	AccessToken       string    `json:"accessToken"`
	RefreshToken      string    `json:"refreshToken,omitempty"`
	ExpiresAt         time.Time `json:"expiresAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	OID               string    `json:"oid,omitempty"`
	TID               string    `json:"tid,omitempty"`
	ClientID          string    `json:"clientId,omitempty"`
	BoundProxy        string    `json:"boundProxy,omitempty"`
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

const fallbackMasterKeyRaw = "m365-copilot2api-fallback-pepper-v1-TODO-DPAPI-keyring"

// masterKeyRaw 返回配置的主密钥原文，未配置时为空。
func masterKeyRaw() string {
	raw := strings.TrimSpace(os.Getenv("M365_MASTER_KEY"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("M365_TOKEN_ENCRYPTION_KEY"))
	}
	return raw
}

// deriveKey 用固定 pepper 对主密钥原文做 HMAC-SHA256 派生出 32 字节 AES 密钥。
func deriveKey(raw string) []byte {
	pepper := []byte("m365-copilot2api-pepper-v1")
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(raw))
	return mac.Sum(nil)
}

func masterKey() []byte {
	raw := masterKeyRaw()
	if raw == "" {
		log.Printf("[security] WARNING: M365_MASTER_KEY not set; refresh tokens are encrypted with a built-in public fallback key. Set M365_MASTER_KEY to protect accounts.json at rest.")
		raw = fallbackMasterKeyRaw
	}
	return deriveKey(raw)
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
	if pt, err := openGCM(masterKey(), b); err == nil {
		return pt, nil
	}
	// 平滑迁移：历史数据可能由未设置 M365_MASTER_KEY 的环境用内置 fallback 密钥写入。
	// 配置主密钥后仍能读出，下次保存会自动改用新密钥，无需重新授权。
	if masterKeyRaw() != "" {
		if pt, err := openGCM(deriveKey(fallbackMasterKeyRaw), b); err == nil {
			return pt, nil
		}
	}
	return "", errors.New("failed to decrypt refresh token with current or fallback key")
}

func openGCM(key, b []byte) (string, error) {
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

// SetAccountsBatch applies schedule/websearch/system-prompt changes to every
// listed account atomically: unknown IDs abort the whole batch with no partial
// writes. Nil pointers leave the corresponding field untouched.
func (s *Store) SetAccountsBatch(ids []string, schedule, webSearch *bool, systemPrompt *string) (int, error) {
	seen := map[string]bool{}
	uniq := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return 0, errors.New("no account ids")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := make([]int, 0, len(uniq))
	for _, id := range uniq {
		found := -1
		for i := range s.data.Accounts {
			if s.data.Accounts[i].ID == id {
				found = i
				break
			}
		}
		if found < 0 {
			return 0, fmt.Errorf("account not found: %s", id)
		}
		idx = append(idx, found)
	}
	for _, i := range idx {
		if schedule != nil {
			s.data.Accounts[i].ScheduleDisabled = !*schedule
		}
		if webSearch != nil {
			s.data.Accounts[i].WebSearchDisabled = !*webSearch
		}
		if systemPrompt != nil {
			sp := strings.TrimSpace(*systemPrompt)
			if len(sp) > 8000 {
				return 0, fmt.Errorf("system prompt too long (max 8000 chars)")
			}
			s.data.Accounts[i].SystemPrompt = sp
		}
		s.data.Accounts[i].UpdatedAt = time.Now()
	}
	if err := s.saveLocked(); err != nil {
		return 0, err
	}
	return len(idx), nil
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
			acc.WebSearchDisabled = existing.WebSearchDisabled
			if acc.SystemPrompt == "" {
				acc.SystemPrompt = existing.SystemPrompt
			}
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
			acc = a
			found = true
			break
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
		f.acc, f.err = s.Upsert(tok)
	}
	close(f.done)
	s.mu.Lock()
	delete(s.inflight, acc.ID)
	s.mu.Unlock()
	return f.acc, f.err
}

func fmtExpired() error { return errors.New("token_expired: refresh token missing or expired") }

func (s *Store) RefreshAllExpired() []TokenRefreshResult {
	s.mu.Lock()
	candidates := make([]AccountToken, 0, len(s.data.Accounts))
	for _, a := range s.data.Accounts {
		remaining := a.ExpiresAt.Sub(time.Now())
		threshold := 120 * time.Second
		if total := a.ExpiresAt.Sub(a.UpdatedAt); total > 0 {
			if t := total / 10; t < threshold {
				threshold = t
			}
		}
		if remaining < threshold && a.RefreshToken != "" {
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
