package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"m365-copilot2api/internal/auth"
	"m365-copilot2api/internal/chathub"
	"m365-copilot2api/internal/mcp"
	"m365-copilot2api/internal/outbound"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type pendingPKCE struct {
	Verifier    string
	Created     time.Time
	Status      string
	Account     any
	Error       string
	RedirectURI string
}

func (s *Server) getRateLimitCooldown() time.Duration {
	secs := s.settings.get().RateLimitCooldownSeconds
	if secs < 5 {
		secs = 30
	}
	return time.Duration(secs) * time.Second
}

func (s *Server) featureFlags() chathub.FeatureFlags {
	cfg := s.settings.get()
	return chathub.FeatureFlags{
		MemoryV2:             cfg.EnableMemoryV2,
		DeepWork:             cfg.EnableDeepWork,
		ComputerUse:          cfg.EnableComputerUse,
		RealtimeVoice:        cfg.EnableRealtimeVoice,
		SystemPromptOverride: cfg.EnableSystemPromptOverride,
		DesignerImageGen4o:   cfg.EnableDesignerImageGen4o,
		CodeCanvas:           cfg.EnableCodeCanvas,
		SydneyReconnect:      cfg.EnableSydneyReconnect,
	}
}

const maxAccountProbe = 16

const rateLimitProbePrompt = "Reply with exactly: OK"

func (s *Server) logThrottlingWarning(accountID string, throttling any) {
	maxMsgs := s.settings.get().MaxConversationMessages
	if maxMsgs <= 0 {
		return
	}
	t, ok := throttling.(map[string]any)
	if !ok {
		return
	}
	num, ok := t["numUserMessagesInConversation"]
	if !ok {
		return
	}
	n, ok := num.(float64)
	if !ok {
		return
	}
	if n > float64(maxMsgs)*0.8 {
		log.Printf("[throttling-warning] account=%s messages=%.0f/%d approaching limit", accountID, n, maxMsgs)
	}
}

func (s *Server) markAccountResult(accountID string, err error) {
	if s == nil || s.accountPool == nil || accountID == "" {
		return
	}
	if err != nil {
		s.accountPool.MarkFailure(accountID, err, s.getRateLimitCooldown())
		return
	}
	s.accountPool.MarkSuccess(accountID)
}

// confirmRateLimitNotice verifies a text-channel rate-limit notice with a
// separate, fresh ChatHub conversation. A single notice is not enough to cool
// down an account because the upstream can occasionally emit a false positive.
func (s *Server) confirmRateLimitNotice(ctx context.Context, acc auth.AccountToken, noticeErr error) (bool, error) {
	if !errors.Is(noticeErr, chathub.ErrRateLimitNotice) {
		return IsRateLimited(noticeErr), noticeErr
	}

	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	probeSettings := s.settings.get()
	_, probeErr := s.chatWithAccount(probeCtx, acc.ID, chathub.Account{
		AccessToken: acc.AccessToken,
		OID:         acc.OID,
		TID:         acc.TID,
	}, chathub.Request{
		Text:         rateLimitProbePrompt,
		Tone:         "magic",
		Started:      true,
		LicenseType:  probeSettings.LicenseType,
		Scenario:     probeSettings.Scenario,
		FeatureFlags: s.featureFlags(),
	})
	if probeErr == nil {
		return false, nil
	}
	if errors.Is(probeErr, chathub.ErrRateLimitNotice) || IsRateLimited(probeErr) {
		return true, &UpstreamHTTPError{
			Status:     http.StatusTooManyRequests,
			RetryAfter: int(s.getRateLimitCooldown().Seconds()),
		}
	}
	return false, probeErr
}

type Server struct {
	mu                   sync.Mutex
	tokens               *auth.Store
	accountPool          *accountHealth
	accountConcurrency   *accountConcurrency
	pkce                 map[string]pendingPKCE
	chat                 *chathub.Client
	proxyClients         sync.Map
	sessions             *sessionStore
	userSessions         *userSessionStore
	sessionResolver      *sessionResolver
	conversationManager  *conversationManager
	adminPassword        string
	adminPasswordHistory []string
	adminSessions        map[string]time.Time
	mustChangePassword   bool
	loginAttempts        map[string]loginAttempt
	apiKeys              *apiKeyStore
	debug                *debugStore
	settings             *settingsStore
	responseMu           sync.Mutex
	responseMessages     map[string]map[string]*RespNode
	usage                *usageLog
	generatedImages      map[string]generatedImage
	convCache            *conversationCache
	lastHealthyAccount   string
	contextAffinity      *contextAffinity
}

const maxResponsesPerTenant = 256

func (s *Server) clientForProxy(proxyURL string) *chathub.Client {
	if proxyURL == "" {
		return s.chat
	}
	if v, ok := s.proxyClients.Load(proxyURL); ok {
		return v.(*chathub.Client)
	}
	clients, err := outbound.New(proxyURL)
	if err != nil {
		log.Printf("[bound-proxy] invalid proxy %q: %v", proxyURL, err)
		return s.chat
	}
	c := &chathub.Client{
		HTTPHeader: make(http.Header),
		HTTPClient: clients.HTTP,
		Dialer:     clients.WebSocket,
		Pool:       chathub.NewConnPool(clients.WebSocket, make(http.Header)),
		Trace:      s.chat.Trace,
	}
	c.HTTPHeader.Set("Origin", "https://m365.cloud.microsoft")
	c.HTTPHeader.Set("User-Agent", chromeUA)
	actual, _ := s.proxyClients.LoadOrStore(proxyURL, c)
	return actual.(*chathub.Client)
}

type ToolCallRecord struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Type      string `json:"type,omitempty"`
}

type RespNode struct {
	At        time.Time                  `json:"at"`
	Messages  []oaiMsg                   `json:"messages"`
	ToolCalls map[string]*ToolCallRecord `json:"tool_calls,omitempty"`
	Version   int64                      `json:"version"`
	Consumed  bool                       `json:"consumed"`
	ParentID  string                     `json:"parent_id,omitempty"`
	Tenant    string                     `json:"tenant,omitempty"`
	SessionID string                     `json:"session_id,omitempty"`
}

// respHistory is kept as an alias so older code or tests referencing the old
// name continue to compile; the new canonical type is RespNode.
type respHistory = RespNode

func New() (*Server, error) {
	store, err := auth.OpenStore("")
	if err != nil {
		return nil, err
	}
	password, mustChange, err := loadAdminPassword()
	if err != nil {
		return nil, err
	}
	var history []string
	if data, ok := readPersistedAdminData(); ok {
		history = data.History
	}
	sessionTTL := 30 * time.Minute
	if v := os.Getenv("M365_USER_SESSION_TTL_MINUTES"); v != "" {
		if d, err := time.ParseDuration(v + "m"); err == nil {
			sessionTTL = d
		}
	}
	return &Server{
		tokens:             store,
		accountPool:        newAccountHealth(),
		accountConcurrency: newAccountConcurrency(),
		pkce:               map[string]pendingPKCE{},
		chat: func() *chathub.Client {
			c := chathub.NewClient()
			c.Trace = func(meta map[string]any) { fmt.Printf("[multimodal-trace] %s\\n", mustJSON(meta)) }
			return c
		}(),
		sessions:             openSessionStore(),
		userSessions:         openUserSessionStore(sessionTTL),
		sessionResolver:      openSessionResolver(),
		conversationManager:  openConversationManager(),
		adminPassword:        password,
		adminPasswordHistory: history,
		adminSessions:        map[string]time.Time{},
		mustChangePassword:   mustChange,
		loginAttempts:        map[string]loginAttempt{},
		apiKeys:              openAPIKeys(),
		debug:                openDebugStore(),
		settings:             openSettingsStore(),
		responseMessages:     map[string]map[string]*RespNode{},
		usage:                openUsageLog(),
		generatedImages:      map[string]generatedImage{},
		convCache:            newConversationCache(),
		contextAffinity:      newContextAffinity(),
	}, nil
}

func (s *Server) StartConvCacheGC() {
	go func() {
		for {
			time.Sleep(2 * time.Minute)
			s.convCache.GC()
		}
	}()
}

func (s *Server) PreheatPool() {
	if s.chat == nil || s.chat.Pool == nil {
		return
	}
	accounts := s.tokens.List()
	cfg := s.settings.get()
	for _, acc := range accounts {
		if acc.OID == "" || acc.TID == "" {
			oid, tid := extractOIDTID(acc.AccessToken)
			acc.OID, acc.TID = oid, tid
		}
		if acc.OID == "" {
			continue
		}
		// Warm through the account's own client so a proxy-bound account warms
		// its proxy pool instead of leaking a direct connection past the proxy.
		client := s.accountClient(acc.ID)
		if client == nil || client.Pool == nil {
			continue
		}
		for i := 0; i < 2; i++ {
			go func(a auth.AccountToken, cl *chathub.Client) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				reqID := uuid.NewString()
				sid := uuid.NewString()
				cid := uuid.NewString()
				wsURL, err := chathub.BuildWSURL(chathub.Account{AccessToken: a.AccessToken, OID: a.OID, TID: a.TID}, sid, cid, reqID, cfg.LicenseType, cfg.Scenario)
				if err != nil {
					return
				}
				cl.Pool.Warm(ctx, chathub.Account{AccessToken: a.AccessToken, OID: a.OID, TID: a.TID}, wsURL)
			}(acc, client)
		}
	}
}

func (s *Server) InitM365CloudClient() {
	accounts := s.tokens.List()
	if len(accounts) == 0 {
		return
	}
	acc := accounts[0]
	clientID := os.Getenv("M365_CLIENT_ID")
	if clientID == "" {
		clientID = acc.ClientID
	}
	if clientID == "" {
		clientID = auth.DefaultClientID
	}
	InitM365CloudClient(clientID, acc.TID, acc.RefreshToken)
	log.Printf("[m365-cloud] client initialized for account %s", acc.Email)
}

// keepPoolWarm re-warms the active account plus one standby so a new request
// reuses a pooled socket instead of paying the ~0.7s dial. Pooled sockets have
// a 2-minute TTL, so this runs well inside that window.
func (s *Server) keepPoolWarm() {
	if s.chat == nil || s.chat.Pool == nil {
		return
	}
	s.mu.Lock()
	active := s.lastHealthyAccount
	s.mu.Unlock()
	accounts := s.tokens.List()
	if len(accounts) == 0 {
		return
	}
	targets := make([]auth.AccountToken, 0, 2)
	seen := map[string]bool{}
	add := func(a auth.AccountToken) {
		if a.ID == "" || seen[a.ID] {
			return
		}
		if a.OID == "" || a.TID == "" {
			oid, tid := extractOIDTID(a.AccessToken)
			a.OID, a.TID = oid, tid
		}
		if a.OID == "" || a.TID == "" {
			return
		}
		seen[a.ID] = true
		targets = append(targets, a)
	}
	if acc, ok := s.tokens.Get(active); ok && s.accountAvailable(acc.ID) {
		add(acc)
	}
	for _, a := range accounts {
		if len(targets) >= 2 {
			break
		}
		if a.ID != active && s.accountAvailable(a.ID) {
			add(a)
		}
	}
	if len(targets) == 0 {
		for _, a := range accounts {
			if s.accountAvailable(a.ID) {
				add(a)
			}
			if len(targets) >= 2 {
				break
			}
		}
	}
	cfg := s.settings.get()
	now := time.Now()
	for _, acc := range targets {
		// Warm through the account's own client so a proxy-bound account warms
		// the correct pool instead of leaking a direct connection past its proxy.
		client := s.accountClient(acc.ID)
		if client == nil || client.Pool == nil {
			continue
		}
		// Skip accounts whose token is already expired; dialing would just fail.
		if !acc.ExpiresAt.IsZero() && acc.ExpiresAt.Before(now) {
			continue
		}
		for i := 0; i < 2; i++ {
			go func(a auth.AccountToken, cl *chathub.Client) {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				wsURL, err := chathub.BuildWSURL(chathub.Account{AccessToken: a.AccessToken, OID: a.OID, TID: a.TID}, uuid.NewString(), uuid.NewString(), uuid.NewString(), cfg.LicenseType, cfg.Scenario)
				if err != nil {
					return
				}
				cl.Pool.Warm(ctx, chathub.Account{AccessToken: a.AccessToken, OID: a.OID, TID: a.TID}, wsURL)
			}(acc, client)
		}
	}
}

// StartPoolKeeper keeps a small set of pooled WebSocket connections warm so the
// first request of a session does not pay a fresh dial. It stops when ctx is
// cancelled during shutdown.
func (s *Server) StartPoolKeeper(ctx context.Context) {
	go func() {
		t := time.NewTicker(90 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.keepPoolWarm()
			}
		}
	}()
}

func (s *Server) RefreshExpiredTokens() {
	results := s.tokens.RefreshAllExpired()
	for _, r := range results {
		if r.Success {
			log.Printf("[token-refresh] account=%s refreshed, expires=%s", r.Email, r.ExpiresAt.Format(time.RFC3339))
		} else {
			log.Printf("[token-refresh] account=%s failed: %s", r.Email, r.Error)
		}
	}
}

func (s *Server) Routes() http.Handler {
	mcp.APIKeyValidator = s.validAPIKey
	m := http.NewServeMux()
	m.HandleFunc("/api/admin/login", s.adminLogin)
	m.HandleFunc("/api/admin/logout", s.adminLogout)
	m.HandleFunc("/api/admin/session", s.adminSession)
	m.HandleFunc("/api/admin/change-password", s.adminChangePassword)
	m.HandleFunc("/api/admin/keys", s.adminKeys)
	m.HandleFunc("/api/admin/models", s.adminModels)
	m.HandleFunc("/api/admin/models/test", s.adminModelTest)
	m.HandleFunc("/api/admin/models/sync", s.adminModelSync)
	m.HandleFunc("/api/admin/settings", s.adminSettings)
	m.HandleFunc("/api/admin/proxy-pool", s.proxyPool)
	m.HandleFunc("/api/admin/deployments", s.deployments)
	m.HandleFunc("/api/admin/deployment", s.deploymentAction)
	m.HandleFunc("/api/admin/deployment/check", s.deploymentCheck)
	m.HandleFunc("/api/admin/debug/logs", s.debugList)
	m.HandleFunc("/api/admin/debug/detail", s.debugDetail)
	m.HandleFunc("/api/health", s.health)
	m.HandleFunc("/api/version", s.version)
	m.HandleFunc("/api/update", s.update)
	m.HandleFunc("/api/accounts", s.accounts)
	m.HandleFunc("/api/accounts/refresh", s.refreshAccount)
	m.HandleFunc("/api/accounts/schedule", s.scheduleAccount)
	m.HandleFunc("/api/accounts/batch", s.batchAccounts)
	m.HandleFunc("/api/accounts/token-health", s.tokenHealth)
	m.HandleFunc("/api/accounts/clear-cooldown", s.clearCooldown)
	m.HandleFunc("/api/accounts/delete", s.deleteAccount)
	m.HandleFunc("/api/accounts/provision", s.provisionAccount)
	m.HandleFunc("/api/accounts/bind-proxy", s.bindProxy)
	m.HandleFunc("/api/auth/start", s.startPKCE)
	m.HandleFunc("/api/auth/status", s.pkceStatus)
	m.HandleFunc("/api/auth/callback", s.callbackPKCE)
	m.HandleFunc("/api/chat", s.chatOnce)
	m.HandleFunc("/api/chat/stream", s.chatStream)
	m.HandleFunc("/api/conversations", s.conversations)
	m.HandleFunc("/api/conversations/delete", s.deleteConversation)
	m.HandleFunc("/api/conversations/cleanup", s.conversationCleanup)
	m.HandleFunc("/api/conversations/whitelist", s.conversationWhitelist)
	m.HandleFunc("/v1/sessions", s.handleSessions)
	m.HandleFunc("/v1/sessions/", s.handleSessionDelete)
	m.HandleFunc("/api/m365/conversations", s.handleM365Conversations)
	m.HandleFunc("/api/m365/conversations/detail", s.handleM365ConversationDetail)
	m.HandleFunc("/api/m365/conversations/delete", s.handleM365Delete)
	m.HandleFunc("/api/m365/conversations/cleanup", s.handleM365Cleanup)
	m.HandleFunc("/api/stats", s.handleCacheStats)
	m.HandleFunc("/api/stats/reset", s.handleCacheStatsReset)
	m.HandleFunc("/api/usage", s.adminUsage)
	m.HandleFunc("/api/usage/logs", s.adminUsageLogs)
	m.HandleFunc("/api/plugins", s.plugins)
	m.HandleFunc("/v1/models", s.openaiModels)
	m.HandleFunc("/v1/chat/completions", s.openaiChat)
	m.HandleFunc("/v1/responses", s.responses)
	m.HandleFunc("/v1/mcp/sse", mcp.HandleSSE)
	m.HandleFunc("/v1/mcp/message", mcp.HandleMessage)
	m.HandleFunc("/v1/mcp/tools", mcp.HandleToolsList)
	m.HandleFunc("/v1/messages", s.anthropicMessages)
	m.HandleFunc("/v1/images/generations", s.imageGenerations)
	m.HandleFunc("/v1/images/edits", s.imageEdits)
	m.HandleFunc("/v1/images/files/", s.generatedImageFile)
	m.HandleFunc("/v1/memory/flags", s.handleMemoryFlags)
	m.HandleFunc("/v1/memory/instructions", s.handleMemoryInstructions)
	m.HandleFunc("/v1/memory/instructions/", s.handleMemoryInstructionsID)
	m.HandleFunc("/v1/memory/settings", s.handleMemorySettings)
	// React SPA bundle (Vite output lives under web/webapp inside the embed).
	m.HandleFunc("/webapp/", s.serveWebApp)
	m.HandleFunc("/", s.rootPage)
	return recoverPanics(requestID(httpTrace(securityHeaders(s.adminMiddleware(s.debugMiddleware(m))))))
}

func (s *Server) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/images/files/") {
			next.ServeHTTP(w, r)
			return
		}
		// The SPA shell and its hashed assets are public: authentication is
		// enforced by the app itself against the admin session APIs.
		if strings.HasPrefix(r.URL.Path, "/webapp/") {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/admin/login" || r.URL.Path == "/api/admin/session" || r.URL.Path == "/api/admin/change-password" || r.URL.Path == "/api/admin/logout" || r.URL.Path == "/api/auth/start" || r.URL.Path == "/api/auth/status" || r.URL.Path == "/api/auth/callback" || r.URL.Path == "/" || r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			if !s.validAPIKey(r) {
				writeOpenAIError(w, http.StatusUnauthorized, "auth_error", "valid API key required")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if s.adminPassword == "" {
			writeOpenAIError(w, http.StatusServiceUnavailable, "configuration_error", "administrator password is not configured")
			return
		}
		if !s.validAdminSession(r) {
			writeOpenAIError(w, http.StatusUnauthorized, "auth_error", "administrator login required")
			return
		}
		s.mu.Lock()
		mustChange := s.mustChangePassword
		s.mu.Unlock()
		if mustChange && r.URL.Path != "/api/admin/change-password" && r.URL.Path != "/api/admin/logout" {
			writeOpenAIError(w, http.StatusForbidden, "password_change_required", "administrator password must be changed before using the console")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func secureAdminCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	// Only trust X-Forwarded-Proto from a loopback reverse proxy.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) validAdminSession(r *http.Request) bool {
	c, err := r.Cookie("m365_admin_session")
	if err != nil || c.Value == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.adminSessions[c.Value]
	if !ok || time.Now().After(expires) {
		delete(s.adminSessions, c.Value)
		return false
	}
	return true
}

const maxAdminSessions = 4096

// pruneAdminSessions drops expired entries; callers must hold s.mu.
func pruneAdminSessions(m map[string]time.Time, now time.Time) {
	for k, exp := range m {
		if now.After(exp) {
			delete(m, k)
		}
	}
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	ip, now := clientIP(r), time.Now()
	if ok, wait := s.loginAllowed(ip, now); !ok {
		seconds := int(wait.Seconds()) + 1
		w.Header().Set("Retry-After", fmt.Sprint(seconds))
		auditLog(r, "admin_login_locked", fmt.Sprintf("locked wait=%ds", seconds))
		writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_error", "too many failed login attempts; try again later")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	s.mu.Lock()
	passwordHash := s.adminPassword
	mustChange := s.mustChangePassword
	s.mu.Unlock()
	if decodeErr != nil || body.Password == "" || !checkPassword(passwordHash, body.Password) {
		s.recordLoginFailure(ip, now)
		auditLog(r, "admin_login_failed", "invalid password")
		writeOpenAIError(w, http.StatusUnauthorized, "auth_error", "invalid administrator password")
		return
	}
	s.clearLoginFailures(ip)
	auditLog(r, "admin_login_success", "")
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		writeOpenAIError(w, 500, "internal_error", "session failure")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	pruneAdminSessions(s.adminSessions, now)
	if len(s.adminSessions) >= maxAdminSessions {
		// Evict the oldest entry to keep the map bounded.
		var oldest string
		var oldestExp time.Time
		for k, exp := range s.adminSessions {
			if oldest == "" || exp.Before(oldestExp) {
				oldest, oldestExp = k, exp
			}
		}
		delete(s.adminSessions, oldest)
	}
	s.adminSessions[token] = now.Add(24 * time.Hour)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "m365_admin_session", Value: token, Path: "/", HttpOnly: true, Secure: secureAdminCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: 86400})
	jsonOut(w, map[string]any{"status": "authenticated", "must_change_password": mustChange})
}
func (s *Server) adminLogout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("m365_admin_session"); e == nil {
		s.mu.Lock()
		delete(s.adminSessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "m365_admin_session", Path: "/", HttpOnly: true, Secure: secureAdminCookie(r), SameSite: http.SameSiteLaxMode, MaxAge: -1})
	jsonOut(w, map[string]string{"status": "logged_out"})
}
func (s *Server) adminSession(w http.ResponseWriter, r *http.Request) {
	authenticated := s.validAdminSession(r)
	s.mu.Lock()
	mustChange := s.mustChangePassword
	s.mu.Unlock()
	jsonOut(w, map[string]bool{"authenticated": authenticated, "must_change_password": authenticated && mustChange})
}

func (s *Server) adminKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		jsonOut(w, map[string]any{"keys": s.apiKeys.list()})
	case http.MethodPost:
		var b struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil {
			writeOpenAIError(w, 400, "invalid_request_error", "bad json")
			return
		}
		if strings.TrimSpace(b.Name) == "" {
			b.Name = "API key"
		}
		rec, raw, e := s.apiKeys.create(b.Name)
		if e != nil {
			writeOpenAIError(w, 500, "internal_error", e.Error())
			return
		}
		jsonOut(w, map[string]any{"key": raw, "record": rec})
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		deleted, e := s.apiKeys.delete(id)
		if e != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "internal_error", e.Error())
			return
		}
		if !deleted {
			writeOpenAIError(w, 404, "not_found", "key not found")
			return
		}
		jsonOut(w, map[string]string{"status": "deleted"})
	case http.MethodPut:
		var b struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Revoked *bool  `json:"revoked"`
		}
		if json.NewDecoder(r.Body).Decode(&b) != nil || b.ID == "" {
			writeOpenAIError(w, 400, "invalid_request_error", "bad json")
			return
		}
		updated, e := s.apiKeys.update(b.ID, b.Name, b.Revoked)
		if e != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "internal_error", e.Error())
			return
		}
		if !updated {
			writeOpenAIError(w, 404, "not_found", "key not found")
			return
		}
		jsonOut(w, map[string]string{"status": "updated"})
	default:
		writeOpenAIError(w, 405, "invalid_request_error", "method not allowed")
	}
}

// rawAPIKey returns the full API key presented by the caller (X-API-Key or
// Authorization: Bearer), or "" when none is present. Unlike extractAPIKey it
// does not truncate: callers that use the key as a tenant/isolation identity
// need the complete secret so distinct keys never collide on a shared prefix.
func rawAPIKey(r *http.Request) string {
	raw := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if raw == "" {
		v := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(v), "bearer ") {
			raw = strings.TrimSpace(v[7:])
		}
	}
	return raw
}

func (s *Server) validAPIKey(r *http.Request) bool {
	raw := rawAPIKey(r)
	return raw != "" && s.apiKeys.valid(raw)
}

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[jsonOut] encode error: %v", err)
	}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	list := s.tokens.List()
	throttlingSummary := map[string]any{}
	if s.accountPool != nil {
		for _, a := range list {
			if t := s.accountPool.GetThrottling(a.ID); t != nil {
				throttlingSummary[a.ID] = t
			}
		}
	}
	jsonOut(w, map[string]any{
		"status":             "ok",
		"auth":               []string{"pkce"},
		"chat":               "chathub",
		"clientId":           auth.ClientID(),
		"scope":              auth.Scope(),
		"tokenCache":         s.tokens.Path(),
		"accountCount":       len(list),
		"accountConcurrency": s.accountConcurrency.Snapshot(),
		"throttling":         throttlingSummary,
	})
}

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	list := s.tokens.List()
	type view struct {
		ID               string     `json:"id"`
		Email            string     `json:"email"`
		DisplayName      string     `json:"displayName,omitempty"`
		Status           string     `json:"status"`
		ScheduleEnabled  bool       `json:"scheduleEnabled"`
		WebSearchEnabled bool       `json:"webSearchEnabled"`
		SystemPrompt     string     `json:"systemPrompt,omitempty"`
		CallCount        uint64     `json:"callCount"`
		RateLimited      bool       `json:"rateLimited"`
		ImageLimited     bool       `json:"imageLimited"`
		AuthFailed       bool       `json:"authFailed"`
		AuthFailReason   string     `json:"authFailReason,omitempty"`
		CooldownUntil    *time.Time `json:"cooldownUntil,omitempty"`
		Throttling       any        `json:"throttling,omitempty"`
		Concurrency      int        `json:"concurrency"`
		OID              string     `json:"oid,omitempty"`
		TID              string     `json:"tid,omitempty"`
		ExpiresAt        time.Time  `json:"expiresAt,omitempty"`
		UpdatedAt        time.Time  `json:"updatedAt,omitempty"`
		BoundProxy       string     `json:"boundProxy,omitempty"`
	}
	out := make([]view, 0, len(list))
	for _, a := range list {
		status := a.Status
		var cooldownUntil *time.Time
		var callCount uint64
		var rateLimited bool
		var throttling any
		var authFailReason string
		var imageLimited bool
		if s.accountPool != nil {
			if until, ok := s.accountPool.CooldownUntil(a.ID); ok {
				status = "cooldown"
				cooldownUntil = &until
			}
			callCount = s.accountPool.CallCount(a.ID)
			rateLimited = s.accountPool.RateLimited(a.ID)
			throttling = s.accountPool.GetThrottling(a.ID)
			authFailReason = s.accountPool.AuthFailReason(a.ID)
			imageLimited = s.accountPool.ImageLimited(a.ID)
		}
		concurrency := s.accountConcurrency.Inflight(a.ID)
		out = append(out, view{
			ID: a.ID, Email: a.Email, DisplayName: a.DisplayName,
			Status: status, ScheduleEnabled: !a.ScheduleDisabled, WebSearchEnabled: !a.WebSearchDisabled, SystemPrompt: a.SystemPrompt,
			CallCount: callCount, RateLimited: rateLimited,
			ImageLimited:   imageLimited,
			AuthFailed:     s.accountPool != nil && !s.accountPool.Available(a.ID) && authFailReason != "",
			AuthFailReason: authFailReason,
			CooldownUntil:  cooldownUntil, Throttling: throttling, Concurrency: concurrency,
			OID: a.OID, TID: a.TID,
			ExpiresAt: a.ExpiresAt, UpdatedAt: a.UpdatedAt, BoundProxy: a.BoundProxy,
		})
	}
	jsonOut(w, map[string]any{"accounts": out, "health": s.accountPool.Snapshot()})
}

func (s *Server) refreshAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad json")
		return
	}
	acc, err := s.tokens.EnsureValid(strings.TrimSpace(body.ID))
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "token_refresh_error", err.Error())
		return
	}
	jsonOut(w, map[string]any{"status": "refreshed", "account": map[string]any{
		"id": acc.ID, "email": acc.Email, "displayName": acc.DisplayName,
		"status": acc.Status, "expiresAt": acc.ExpiresAt, "updatedAt": acc.UpdatedAt,
	}})
}

func (s *Server) scheduleAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	var body struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad json")
		return
	}
	if err := s.tokens.SetScheduleEnabled(strings.TrimSpace(body.ID), body.Enabled); err != nil {
		writeOpenAIError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	jsonOut(w, map[string]any{"status": "updated", "scheduleEnabled": body.Enabled})
}

func (s *Server) batchAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	var body struct {
		IDs          []string `json:"ids"`
		Scheduling   *bool    `json:"scheduling"`
		WebSearch    *bool    `json:"webSearch"`
		SystemPrompt *string  `json:"systemPrompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad json")
		return
	}
	if body.Scheduling == nil && body.WebSearch == nil && body.SystemPrompt == nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "nothing to update")
		return
	}
	n, err := s.tokens.SetAccountsBatch(body.IDs, body.Scheduling, body.WebSearch, body.SystemPrompt)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	jsonOut(w, map[string]any{"status": "updated", "updated": n})
}

func (s *Server) tokenHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		results := s.tokens.RefreshAllExpired()
		refreshed, failed := 0, 0
		for _, r := range results {
			if r.Success {
				refreshed++
			} else {
				failed++
			}
		}
		jsonOut(w, map[string]any{"refreshed": refreshed, "failed": failed, "results": results})
		return
	}
	list := s.tokens.List()
	now := time.Now()
	type entry struct {
		ID        string    `json:"id"`
		Email     string    `json:"email"`
		Status    string    `json:"status"`
		ExpiresAt time.Time `json:"expires_at"`
		Expired   bool      `json:"expired"`
		ExpiresIn string    `json:"expires_in"`
	}
	out := make([]entry, 0, len(list))
	for _, a := range list {
		e := entry{ID: a.ID, Email: a.Email, Status: a.Status, ExpiresAt: a.ExpiresAt}
		if now.After(a.ExpiresAt) {
			e.Expired = true
			e.ExpiresIn = "expired"
		} else {
			e.ExpiresIn = a.ExpiresAt.Sub(now).Truncate(time.Second).String()
		}
		out = append(out, e)
	}
	jsonOut(w, map[string]any{"accounts": out, "now": now.Format(time.RFC3339)})
}

func (s *Server) clearCooldown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	s.accountPool.ClearAllCooldowns()
	jsonOut(w, map[string]any{"status": "ok"})
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad json")
		return
	}
	if err := s.tokens.Delete(body.ID); err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	jsonOut(w, map[string]string{"status": "deleted"})
}

func (s *Server) provisionAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	if !s.validAdminSession(r) {
		writeOpenAIError(w, http.StatusUnauthorized, "auth_error", "administrator login required")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" || body.Password == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "email and password required")
		return
	}
	set, err := auth.ROPC(body.Email, body.Password)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "ropc_error", err.Error())
		return
	}
	acc, err := s.tokens.Upsert(set)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "upsert_error", err.Error())
		return
	}
	jsonOut(w, map[string]any{"status": "provisioned", "account": map[string]any{
		"id": acc.ID, "email": acc.Email, "displayName": acc.DisplayName,
		"status": acc.Status, "expiresAt": acc.ExpiresAt,
	}})
}

func (s *Server) bindProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	if !s.validAdminSession(r) {
		writeOpenAIError(w, http.StatusUnauthorized, "auth_error", "administrator login required")
		return
	}
	var body struct {
		ID       string `json:"id"`
		ProxyURL string `json:"proxyUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "id required")
		return
	}
	if body.ProxyURL != "" {
		if err := outbound.ValidateProxyURL(body.ProxyURL); err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_proxy", err.Error())
			return
		}
	}
	if err := s.tokens.SetBoundProxy(body.ID, body.ProxyURL); err != nil {
		writeOpenAIError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	acc, _ := s.tokens.Get(body.ID)
	if acc.BoundProxy == "" {
		s.proxyClients.Range(func(key, _ any) bool {
			if keyStr, ok := key.(string); ok && keyStr != "" {
				s.proxyClients.Delete(keyStr)
			}
			return true
		})
	}
	jsonOut(w, map[string]any{"ok": true, "id": body.ID, "boundProxy": acc.BoundProxy})
}

func (s *Server) startPKCE(w http.ResponseWriter, _ *http.Request) {
	v, err := auth.Verifier()
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "pkce failure")
		return
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", "state failure")
		return
	}
	state := hex.EncodeToString(b)
	redirectURI := auth.RedirectURI()
	s.mu.Lock()
	s.pkce[state] = pendingPKCE{Verifier: v, Created: time.Now(), Status: "pending", RedirectURI: redirectURI}
	s.mu.Unlock()
	jsonOut(w, map[string]string{
		"status": "pkce_ready",
		"state":  state,
		"url": auth.AuthorizationURL(
			auth.AuthorizeEndpoint(),
			auth.ClientID(),
			redirectURI,
			state,
			auth.Challenge(v),
			auth.Scope(),
		),
		"redirectUri": redirectURI,
		"note":        "If redirect is nativeclient, paste the final URL/code into /api/auth/callback after login.",
	})
}

func (s *Server) pkceStatus(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if state == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "missing state")
		return
	}
	s.mu.Lock()
	p, ok := s.pkce[state]
	if ok && time.Since(p.Created) > 10*time.Minute {
		delete(s.pkce, state)
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		jsonOut(w, map[string]any{"status": "expired"})
		return
	}
	out := map[string]any{"status": p.Status}
	if p.Account != nil {
		out["account"] = p.Account
	}
	if p.Error != "" {
		out["error"] = p.Error
	}
	jsonOut(w, out)
}

func (s *Server) callbackPKCE(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	oauthError := r.URL.Query().Get("error")
	// also accept pasted full callback URL
	if code == "" && oauthError == "" {
		if u := r.URL.Query().Get("url"); u != "" {
			if parsed, err := http.NewRequest(http.MethodGet, u, nil); err == nil {
				code = parsed.URL.Query().Get("code")
				oauthError = parsed.URL.Query().Get("error")
				if state == "" {
					state = parsed.URL.Query().Get("state")
				}
			}
		}
	}
	if state == "" || (code == "" && oauthError == "") {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "missing state or authorization result")
		return
	}
	s.mu.Lock()
	p, ok := s.pkce[state]
	if !ok || time.Since(p.Created) > 10*time.Minute {
		if ok {
			delete(s.pkce, state)
		}
		s.mu.Unlock()
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "invalid or expired state")
		return
	}
	if p.Status != "pending" {
		s.mu.Unlock()
		writeOpenAIError(w, http.StatusConflict, "invalid_request_error", "authorization result already consumed")
		return
	}
	p.Status = "processing"
	s.pkce[state] = p
	s.mu.Unlock()
	if oauthError != "" {
		log.Printf("oauth_error stage=callback error=%q", oauthError)
		s.mu.Lock()
		p.Status = "error"
		p.Error = oauthError
		s.pkce[state] = p
		s.mu.Unlock()
		writeOpenAIError(w, http.StatusBadRequest, "auth_error", "Microsoft authorization failed: "+oauthError)
		return
	}
	redirectURI := p.RedirectURI
	if redirectURI == "" {
		redirectURI = auth.RedirectURI()
	}
	tok, err := auth.ExchangeCode(code, p.Verifier, redirectURI)
	if err != nil {
		logOAuthError("code_exchange", err)
		s.mu.Lock()
		p.Status = "error"
		p.Error = err.Error()
		s.pkce[state] = p
		s.mu.Unlock()
		writeOpenAIError(w, http.StatusBadRequest, "auth_error", err.Error())
		return
	}
	acc, err := s.tokens.Upsert(tok)
	if err != nil {
		s.mu.Lock()
		p.Status = "error"
		p.Error = err.Error()
		s.pkce[state] = p
		s.mu.Unlock()
		writeOpenAIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	s.mu.Lock()
	p.Status = "authenticated"
	p.Account = map[string]any{"id": acc.ID, "email": acc.Email, "displayName": acc.DisplayName, "status": acc.Status, "oid": acc.OID, "tid": acc.TID}
	s.pkce[state] = p
	s.mu.Unlock()
	// Hot-reload the cloud client so conversation management works immediately
	// after adding the first account at runtime (issue #84); it is otherwise
	// only initialized at startup.
	s.InitM365CloudClient()
	// Browser loopback callbacks should finish in a friendly page instead of
	// displaying a raw JSON response. Keep JSON for the manual/API flow.
	if strings.HasPrefix(redirectURI, "http://127.0.0.1:") || strings.HasPrefix(redirectURI, "http://localhost:") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>M365 Copilot2API 授权完成</title><style>body{font:16px system-ui;text-align:center;padding:15vh 20px;color:#242424}main{max-width:520px;margin:auto}h1{font-size:26px}</style><main><h1>授权完成</h1><p>账号已经自动加入账号池，可以关闭此页面。</p><script>if(window.opener){window.opener.postMessage({type:"m365-auth-complete"},window.location.origin);setTimeout(()=>window.close(),300)}</script></main>`)
		return
	}
	jsonOut(w, map[string]any{
		"status":  "authenticated",
		"account": map[string]any{"id": acc.ID, "email": acc.Email, "displayName": acc.DisplayName, "status": acc.Status, "oid": acc.OID, "tid": acc.TID},
	})
}

func (s *Server) resolveAccount(accountID string) (auth.AccountToken, error) {
	if accountID == "" {
		// Failover mode: prefer the last healthy account, only rotate on failure
		s.mu.Lock()
		preferred := s.lastHealthyAccount
		s.mu.Unlock()
		if preferred != "" && s.accountAvailable(preferred) && s.accountPool.Available(preferred) && s.accountConcurrency.Available(preferred) {
			if acc, err := s.tokens.EnsureValid(preferred); err == nil {
				accountID = preferred
				return acc, nil
			}
		}
		// No preferred account or it's unavailable; fall back to round-robin
		acc, ok := s.tokens.Next()
		if !ok {
			return auth.AccountToken{}, fmt.Errorf("no accounts; login first")
		}
		accountID = acc.ID
		for i := 0; !s.accountAvailable(accountID) && i < maxAccountProbe; i++ {
			acc, ok = s.tokens.Next()
			if !ok {
				break
			}
			accountID = acc.ID
		}
		if !s.tokens.ScheduleEnabled(accountID) {
			return auth.AccountToken{}, fmt.Errorf("no accounts enabled for scheduling")
		}
		if !s.accountPool.Available(accountID) {
			until := s.accountPool.EarliestRecovery()
			retry := int(time.Until(until).Seconds())
			if retry < 5 {
				retry = 5
			}
			// A local network failure is not a quota problem: report 503 with a
			// distinct error code instead of masquerading as 429 rate limiting
			// (issue #79).
			if cat, at := s.accountPool.LastCategory(); time.Since(at) < 5*time.Minute && IsTransportCategory(cat) {
				return auth.AccountToken{}, &UpstreamHTTPError{Status: 503, ErrorCode: "network_error", RetryAfter: retry, Body: "all accounts cooling down after local network errors (last: " + string(cat) + "); check DNS/IPv6/proxy connectivity"}
			}
			return auth.AccountToken{}, &UpstreamHTTPError{Status: 429, RetryAfter: retry, Body: "all accounts are cooling down; try again later"}
		}
		if !s.accountConcurrency.Available(accountID) {
			return auth.AccountToken{}, &UpstreamHTTPError{Status: 429, RetryAfter: 1, Body: "all accounts are at their concurrency limit; try again shortly"}
		}
	}
	result, err := s.tokens.EnsureValid(accountID)
	if err == nil {
		s.mu.Lock()
		s.lastHealthyAccount = accountID
		s.mu.Unlock()
	}
	return result, err
}

// nextHealthyAccount returns the next round-robin account that is still
// healthy, skipping the given id first, and validates its token. Used by the
// failover path after a rate-limited or auth-failed attempt.
func (s *Server) nextHealthyAccount(avoidID string) (auth.AccountToken, error) {
	for i := 0; i < maxAccountProbe; i++ {
		acc, ok := s.tokens.Next()
		if !ok {
			return auth.AccountToken{}, fmt.Errorf("no accounts; login first")
		}
		if avoidID != "" && acc.ID == avoidID {
			continue
		}
		if !s.accountAvailable(acc.ID) {
			continue
		}
		return s.tokens.EnsureValid(acc.ID)
	}
	return auth.AccountToken{}, fmt.Errorf("no healthy account available for failover")
}

func (s *Server) failoverStats(attempted []string) (total, throttled, healthy, attemptedCount int) {
	total = len(s.tokens.List())
	throttled = 0
	healthy = 0
	if s.accountPool != nil {
		for _, a := range s.tokens.List() {
			if s.accountPool.RateLimited(a.ID) {
				throttled++
			} else if s.accountAvailable(a.ID) {
				healthy++
			}
		}
	} else {
		healthy = total
	}
	attemptedCount = len(attempted)
	return
}

func (s *Server) writeFailoverExhausted(w http.ResponseWriter, attempted []string) {
	total, throttled, healthy, attemptedCount := s.failoverStats(attempted)
	retry := 30
	if s.accountPool != nil {
		if until := s.accountPool.EarliestRecovery(); !until.IsZero() {
			if r := int(time.Until(until).Seconds()); r > 0 {
				retry = r
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", fmt.Sprint(retry))
	w.Header().Set("X-M365-Retry-After", fmt.Sprint(retry))
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":              map[string]any{"message": "所有账号均触发限流，请稍后重试", "type": "rate_limit_error", "code": "rate_limit_exhausted"},
		"total_accounts":     total,
		"throttled_accounts": throttled,
		"healthy_accounts":   healthy,
		"attempted_accounts": attemptedCount,
		"failover_status":    "exhausted",
	})
}

func (s *Server) nextHealthyExcluding(tried map[string]bool) (auth.AccountToken, bool) {
	for _, acc := range s.tokens.List() {
		if tried[acc.ID] {
			continue
		}
		if !s.accountAvailable(acc.ID) {
			continue
		}
		if tok, err := s.tokens.EnsureValid(acc.ID); err == nil {
			return tok, true
		}
	}
	return auth.AccountToken{}, false
}

func isThrottledForFailover(err error) bool {
	if IsRateLimited(err) {
		return true
	}
	// HAR 报告 06/07：WS_READ_TIMEOUT 是账号级读超时，应触发跨账号转移；
	// 报告 07 实证上游 422 属可重试成员。共享层传输故障（DNS/TCP/TLS/SOCKS5）
	// 与 503 不在此列，交给全局熔断快速失败，避免逐账号轮转造成集体冷却。
	switch ClassifyError(err) {
	case CategoryWSReadTimeout, CategoryRetryable422:
		return true
	}
	return false
}

type chatBody struct {
	AccountID             string                   `json:"accountId"`
	Message               string                   `json:"message"`
	Prompt                string                   `json:"prompt"`
	Tone                  string                   `json:"tone"`
	ConversationID        string                   `json:"conversationId"`
	SessionID             string                   `json:"sessionId"`
	SessionKey            string                   `json:"sessionKey"`
	ConversationSignature string                   `json:"conversationSignature"`
	Attachments           []chathub.Attachment     `json:"attachments,omitempty"`
	PreviousMessages      []chathub.ContextMessage `json:"previousMessages,omitempty"`
	ConnectedFederatedIDs []string                 `json:"connectedFederatedIds,omitempty"`
	Tools                 []chathub.Tool           `json:"tools,omitempty"`
	// Legacy OpenAI-compatible clients still send functions/function_call.
	Functions       []json.RawMessage `json:"functions,omitempty"`
	ToolChoice      any               `json:"tool_choice,omitempty"`
	FunctionCall    any               `json:"function_call,omitempty"`
	Reasoning       *reasoningConfig  `json:"reasoning,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
	ResponseFormat  *responseFormat   `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type       string         `json:"type"`
	JSONSchema map[string]any `json:"json_schema,omitempty"`
}

func modelTone(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "gpt-5.2":
		return "Gpt_5_2_Chat"
	case "gpt-5.2-reasoning":
		return "Gpt_5_2_Reasoning"
	case "gpt-5.3":
		return "Gpt_5_3_Chat"
	case "gpt-5.4":
		return "Gpt_5_4_Chat"
	case "gpt-5.4-reasoning":
		return "Gpt_5_4_Reasoning"
	case "gpt-5.5":
		return "Gpt_5_5_Chat"
	case "gpt-5.5-reasoning":
		return "Gpt_5_5_Reasoning"
	case "gpt-5.6-reasoning":
		return "Gpt_5_6_Reasoning"
	case "claude", "claude-sonnet":
		return "Claude_Sonnet"
	case "claude-sonnet-reasoning":
		return "Claude_Sonnet_Reasoning"
	case "gpt-5.4-quick":
		return "Gpt_5_4_Chat"
	case "gpt-5.3-think-deeper":
		return "Gpt_5_3_Chat"
	default:
		return "magic"
	}
}

func sseRaw(ctx context.Context, w http.ResponseWriter, f http.Flusher, payload string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := fmt.Fprint(w, payload); err != nil {
		return err
	}
	if f != nil {
		f.Flush()
	}
	return nil
}

func (s *Server) chatOnce(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	var body chatBody
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad json")
		return
	}
	text := strings.TrimSpace(firstNonEmpty(body.Message, body.Prompt))
	if text == "" && len(body.Attachments) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "message or attachment required")
		return
	}
	if body.SessionKey != "" {
		if v, ok := s.sessions.get(body.SessionKey); ok {
			body.AccountID = firstNonEmpty(body.AccountID, v.AccountID)
			body.ConversationID = firstNonEmpty(body.ConversationID, v.ConversationID)
			body.SessionID = firstNonEmpty(body.SessionID, v.SessionID)
		}
	}
	acc, err := s.resolveAccount(body.AccountID)
	if err != nil {
		if isThrottledForFailover(err) {
			s.writeFailoverExhausted(w, nil)
			return
		}
		writeUpstreamError(w, err)
		return
	}
	if acc.OID == "" || acc.TID == "" {
		if claimsOID, claimsTID := extractOIDTID(acc.AccessToken); claimsOID != "" {
			acc.OID = claimsOID
			acc.TID = claimsTID
		}
	}
	if acc.OID == "" || acc.TID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "account_error", "account missing oid/tid")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
	defer cancel()
	chatSettings := s.settings.get()
	res, err := s.chatWithAccount(ctx, acc.ID, chathub.Account{
		AccessToken: acc.AccessToken,
		OID:         acc.OID,
		TID:         acc.TID,
	}, chathub.Request{
		Text:                  text,
		Tone:                  body.Tone,
		ConversationID:        body.ConversationID,
		SessionID:             body.SessionID,
		Attachments:           body.Attachments,
		LicenseType:           chatSettings.LicenseType,
		Scenario:              chatSettings.Scenario,
		ConversationSignature: body.ConversationSignature,
		PreviousMessages:      body.PreviousMessages,
		ConnectedFederatedIDs: body.ConnectedFederatedIDs,
		FeatureFlags:          s.featureFlags(),
	})
	if err != nil {
		originalErr := err
		if body.AccountID == "" && body.ConversationID == "" && isThrottledForFailover(err) {
			tried := map[string]bool{acc.ID: true}
			attempted := []string{acc.ID}
			s.accountPool.MarkFailure(acc.ID, originalErr, s.getRateLimitCooldown())
			succeeded := false
			var lastErr error = originalErr
			for {
				next, ok := s.nextHealthyExcluding(tried)
				if !ok {
					s.writeFailoverExhausted(w, attempted)
					return
				}
				tried[next.ID] = true
				attempted = append(attempted, next.ID)
				ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
				res2, err2 := s.chatWithAccount(ctx2, next.ID, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, chathub.Request{
					Text:                  text,
					Tone:                  body.Tone,
					ConversationID:        body.ConversationID,
					SessionID:             body.SessionID,
					Attachments:           body.Attachments,
					LicenseType:           chatSettings.LicenseType,
					Scenario:              chatSettings.Scenario,
					ConversationSignature: body.ConversationSignature,
					PreviousMessages:      body.PreviousMessages,
					ConnectedFederatedIDs: body.ConnectedFederatedIDs,
					FeatureFlags:          s.featureFlags(),
				})
				cancel2()
				if err2 == nil {
					s.accountPool.MarkSuccess(next.ID)
					acc = next
					res = res2
					err = nil
					succeeded = true
					break
				}
				if isThrottledForFailover(err2) {
					s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
					lastErr = err2
					continue
				}
				s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
				if errors.Is(err2, chathub.ErrImageLimit) && s.accountPool != nil {
					s.accountPool.MarkImageLimited(next.ID)
				}
				err = err2
				acc = next
				break
			}
			if succeeded {
				// fall through to success path
			} else if len(attempted) > 1 && isThrottledForFailover(lastErr) && err == nil {
				// loop exhausted via continue path already returned, but guard
				s.writeFailoverExhausted(w, attempted)
				return
			} else if err != nil {
				if isThrottledForFailover(err) {
					s.writeFailoverExhausted(w, attempted)
					return
				}
				s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
				if errors.Is(err, chathub.ErrImageLimit) && s.accountPool != nil {
					s.accountPool.MarkImageLimited(acc.ID)
				}
				writeUpstreamError(w, err)
				return
			}
		} else if IsAuthFailure(err) && body.AccountID == "" && body.ConversationID == "" {
			next, nerr := s.nextHealthyAccount(acc.ID)
			if nerr == nil {
				ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
				defer cancel2()
				res2, err2 := s.chatWithAccount(ctx2, next.ID, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, chathub.Request{
					Text:                  text,
					Tone:                  body.Tone,
					ConversationID:        body.ConversationID,
					SessionID:             body.SessionID,
					Attachments:           body.Attachments,
					LicenseType:           chatSettings.LicenseType,
					Scenario:              chatSettings.Scenario,
					ConversationSignature: body.ConversationSignature,
					PreviousMessages:      body.PreviousMessages,
					ConnectedFederatedIDs: body.ConnectedFederatedIDs,
					FeatureFlags:          s.featureFlags(),
				})
				if err2 == nil {
					s.accountPool.MarkFailure(acc.ID, originalErr, s.getRateLimitCooldown())
					if errors.Is(originalErr, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(acc.ID)
					}
					s.accountPool.MarkSuccess(next.ID)
					acc = next
					res = res2
					err = nil
				} else {
					s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
					if errors.Is(err2, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(next.ID)
					}
					err = err2
				}
			}
			if err != nil {
				s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
				if errors.Is(err, chathub.ErrImageLimit) && s.accountPool != nil {
					s.accountPool.MarkImageLimited(acc.ID)
				}
				writeUpstreamError(w, err)
				return
			}
		} else {
			s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
			if errors.Is(err, chathub.ErrImageLimit) && s.accountPool != nil {
				s.accountPool.MarkImageLimited(acc.ID)
			}
			if isThrottledForFailover(err) {
				s.writeFailoverExhausted(w, []string{acc.ID})
				return
			}
			writeUpstreamError(w, err)
			return
		}
	}
	s.accountPool.MarkSuccess(acc.ID)
	s.applyResultMetering(acc.ID, res)
	// Strip upstream citation control markup from public text (issue #79).
	res.Text, _ = chathub.StripCitationMarkers(res.Text, res.References)
	res.Text = sanitizePublicAssistantText(res.Text)
	res.Reasoning = sanitizePublicReasoningText(res.Reasoning)
	if body.SessionKey != "" {
		s.sessions.upsert(conversation{ID: body.SessionKey, AccountID: acc.ID, ConversationID: res.ConversationID, SessionID: res.SessionID, Title: text})
	}
	if res.Throttling != nil {
		if b, err := json.Marshal(res.Throttling); err == nil {
			w.Header().Set("X-M365-Throttling", string(b))
		}
	}
	if len(res.Scores) > 0 {
		if b, err := json.Marshal(res.Scores); err == nil {
			w.Header().Set("X-M365-Scores", string(b))
		}
	}
	jsonOut(w, map[string]any{
		"status":                    "ok",
		"text":                      res.Text,
		"conversationId":            res.ConversationID,
		"sessionId":                 res.SessionID,
		"requestId":                 res.RequestID,
		"throttling":                res.Throttling,
		"suggestedResponses":        res.SuggestedResponses,
		"result":                    res.RawResult,
		"events":                    res.Events,
		"images":                    res.Images,
		"account":                   map[string]any{"id": acc.ID, "email": acc.Email},
		"offense":                   res.Offense,
		"scores":                    res.Scores,
		"conversationTransferToken": res.ConversationTransferToken,
		"meteringInformation":       res.MeteringInformation,
		"spokenText":                res.SpokenText,
		"storageMessageId":          res.StorageMessageID,
		"timestamps":                res.Timestamps,
	})
}

// dropTransientConversation 异步删除 router/repair 轮创建的一次性云端对话，
// 避免每请求都往 M365 对话列表塞一条记录。删除失败不阻塞请求，留给 auto_cleanup 兜底。
func (s *Server) dropTransientConversation(conversationID string) {
	if conversationID == "" || m365CloudClient == nil {
		return
	}
	go func(id string) {
		if err := m365CloudClient.DeleteConversation(id); err != nil {
			log.Printf("[transient-conv] delete failed id=%s err=%v", id, err)
		}
	}(conversationID)
}

func (s *Server) adminModelSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	syncUpstreamTones()
	tones := liveUpstreamTones()
	jsonOut(w, map[string]any{"synced": true, "upstream_tones": tones, "count": len(tones)})
}

func (s *Server) adminModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	jsonOut(w, map[string]any{"object": "list", "data": modelCatalog()})
}

// adminModelTest 由控制台模型测试调用，通过管理员会话鉴权，不依赖明文 API Key
// （密钥加固后 list 不再返回 raw，前端无法再自行携带 key 调用 /v1 端点）。
func (s *Server) adminModelTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	var b struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&b) != nil || strings.TrimSpace(b.Model) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad json: model required")
		return
	}
	acc, err := s.resolveAccount("")
	if err != nil {
		writeUpstreamError(w, err)
		return
	}
	if acc.OID == "" || acc.TID == "" {
		if o, t := extractOIDTID(acc.AccessToken); o != "" {
			acc.OID, acc.TID = o, t
		}
	}
	if acc.OID == "" || acc.TID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "account_error", "account missing oid/tid")
		return
	}
	tone, _ := reasoningTone(b.Model, "")
	prompt := strings.TrimSpace(b.Prompt)
	if prompt == "" {
		prompt = `Say "OK" in one word.`
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
	defer cancel()
	testCfg := s.settings.get()
	res, err := s.chatWithAccount(ctx, acc.ID, chathub.Account{AccessToken: acc.AccessToken, OID: acc.OID, TID: acc.TID}, chathub.Request{
		Text:         prompt,
		Tone:         tone,
		LicenseType:  testCfg.LicenseType,
		Scenario:     testCfg.Scenario,
		FeatureFlags: s.featureFlags(),
	})
	ms := time.Since(start).Milliseconds()
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "m365_error", upstreamError(err))
		return
	}
	// The probe has no conversation binding, so M365 created a throwaway
	// conversation; delete it so model tests do not clutter the account.
	s.dropTransientConversation(res.ConversationID)
	jsonOut(w, map[string]any{"ok": true, "model": b.Model, "reply": sanitizePublicAssistantTextForModel(res.Text, b.Model), "latency_ms": ms})
}

func (s *Server) openaiModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	data := modelCatalog()
	created := time.Now().Unix()
	for _, model := range data {
		model["created"] = created
	}
	// Codex v0.144.5 requires `models`, while OpenAI-compatible clients use
	// `data`. Keep both aliases backed by the same catalog.
	jsonOut(w, map[string]any{"object": "list", "data": data, "models": data})
}

type oaiMsg struct {
	Role             string           `json:"role"`
	Content          any              `json:"content"`
	Name             string           `json:"name,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	ToolCalls        []map[string]any `json:"tool_calls,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
}

type oaiReq struct {
	Model          string          `json:"model"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Messages       []oaiMsg        `json:"messages"`
	Stream         bool            `json:"stream"`
	StreamOptions  *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	MaxTokens           *int                 `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                 `json:"max_completion_tokens,omitempty"`
	Temperature         *float64             `json:"temperature,omitempty"`
	TopP                *float64             `json:"top_p,omitempty"`
	FrequencyPenalty    *float64             `json:"frequency_penalty,omitempty"`
	PresencePenalty     *float64             `json:"presence_penalty,omitempty"`
	Stop                any                  `json:"stop,omitempty"`
	N                   *int                 `json:"n,omitempty"`
	Seed                *int64               `json:"seed,omitempty"`
	Logprobs            *bool                `json:"logprobs,omitempty"`
	TopLogprobs         *int                 `json:"top_logprobs,omitempty"`
	User                string               `json:"user"`
	AccountID           string               `json:"accountId"`
	ConversationID      string               `json:"conversation_id"`
	SessionID           string               `json:"session_id"`
	SessionKey          string               `json:"session_key"`
	ConversationIDC     string               `json:"conversationId,omitempty"`
	SessionIDC          string               `json:"sessionId,omitempty"`
	Attachments         []chathub.Attachment `json:"attachments,omitempty"`
	Tools               []chathub.Tool       `json:"tools,omitempty"`
	Functions           []json.RawMessage    `json:"functions,omitempty"`
	ToolChoice          any                  `json:"tool_choice,omitempty"`
	FunctionCall        any                  `json:"function_call,omitempty"`
	ParallelToolCalls   *bool                `json:"parallel_tool_calls,omitempty"`
	Reasoning           *reasoningConfig     `json:"reasoning,omitempty"`
	ReasoningEffort     string               `json:"reasoning_effort,omitempty"`
	Metadata            *oaiMetadata         `json:"metadata,omitempty"`
}

type oaiMetadata struct {
	CopilotTempSession bool `json:"copilot_temp_session"`
}

func (r *oaiReq) shouldSendStreamUsage() bool {
	if r.StreamOptions == nil {
		return true
	}
	return r.StreamOptions.IncludeUsage
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func contentToString(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, part := range v {
			m, ok := part.(map[string]any)
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			switch t {
			case "text", "input_text", "output_text":
				if s, _ := m["text"].(string); s != "" {
					b.WriteString(s)
				}
			case "image_url":
				url := extractMediaURL(m, "image_url")
				b.WriteString("[image:" + shortHash(url) + "]")
			case "input_image", "image":
				url := extractMediaURL(m, "image_url", "url", "source")
				if raw, ok2 := m["image_url"].(map[string]any); ok2 {
					if u := stringValue(raw, "url", "data", "image_url"); u != "" {
						url = u
					}
				}
				if raw, ok2 := m["source"].(map[string]any); ok2 && url == "" {
					url = stringValue(raw, "url", "data", "source")
				}
				b.WriteString("[image:" + shortHash(url) + "]")
			case "input_file", "file":
				url := stringValue(m, "file_data", "file_url", "url", "source", "file_id")
				b.WriteString("[file:" + shortHash(url) + "]")
			case "input_audio", "audio":
				url := stringValue(m, "data", "audio_url", "url", "source")
				b.WriteString("[audio:" + shortHash(url) + "]")
			}
		}
		return b.String()
	default:
		return fmt.Sprint(v)
	}
}

func extractMediaURL(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case map[string]any:
			if u, ok := v["url"].(string); ok && u != "" {
				return u
			}
		}
	}
	return ""
}

func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func normalizeLegacyTools(body *oaiReq) {
	if len(body.Tools) == 0 && len(body.Functions) > 0 {
		body.Tools = make([]chathub.Tool, 0, len(body.Functions))
		for _, f := range body.Functions {
			body.Tools = append(body.Tools, chathub.Tool{Type: "function", Function: f})
		}
	}
	if body.ToolChoice == nil && body.FunctionCall != nil {
		body.ToolChoice = body.FunctionCall
	}
}

func buildAnswerRequest(answerPrompt, tone string, body oaiReq, ledger agentLedger, planningMode string, mcpServerURL string, cfg runtimeSettings, flags chathub.FeatureFlags, locale chathubLocale, disableMemory bool) chathub.Request {
	if len(ledger.Completed) > 0 || len(ledger.Pending) > 0 {
		answerPrompt += "\n" + ledger.RouterContext()
	}
	if len(ledger.Completed) > 0 {
		answerPrompt += "\nFINAL ANSWER RULE: Report only actions supported by completed tool results. If the goal is not fully verified, state exactly what remains unconfirmed."
	}
	req := chathub.Request{Text: answerPrompt, Tone: tone, ConversationID: body.ConversationID, SessionID: body.SessionID, Attachments: body.Attachments, LicenseType: cfg.LicenseType, Scenario: cfg.Scenario, FeatureFlags: flags, Locale: locale.Locale, Market: locale.Market, TimeZone: locale.TimeZone, TimeZoneOffset: locale.TimeZoneOffset, DeviceOS: locale.DeviceOS, DisableMemory: disableMemory}
	if planningMode == "native" {
		req.Tools = body.Tools
		req.ToolChoice = body.ToolChoice
	}
	if mcpServerURL != "" {
		req.Tools = body.Tools
		if req.ToolChoice == nil {
			req.ToolChoice = body.ToolChoice
		}
	}
	if len(body.Tools) > 0 {
		mcpTools := make([]mcp.Tool, 0, len(body.Tools))
		for _, t := range body.Tools {
			var f struct {
				Name, Description string
				Parameters        json.RawMessage `json:"parameters"`
			}
			if json.Unmarshal(t.Function, &f) != nil || f.Name == "" {
				continue
			}
			var schema map[string]any
			if json.Unmarshal(f.Parameters, &schema) != nil {
				schema = map[string]any{"type": "object"}
			}
			mcpTools = append(mcpTools, mcp.Tool{Name: f.Name, Description: f.Description, InputSchema: schema})
		}
		if len(mcpTools) > 0 {
			mcp.GlobalToolRegistry.MergeTools(mcpTools)
		}
	}
	req.MCPServerURL = mcpServerURL
	return req
}

func (s *Server) openaiChat(w http.ResponseWriter, r *http.Request) {
	requestID := requestIDFrom(r)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	startedAt := time.Now()
	log.Printf("[req-trace] id=%s stage=http_start stream=%t", requestID, r.URL.Query().Get("stream") == "true")
	defer func() {
		log.Printf("[req-trace] id=%s stage=http_return total_ms=%d", requestID, time.Since(startedAt).Milliseconds())
	}()
	if r.Method != http.MethodPost {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	const maxChatRequestBody = 10 << 20
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChatRequestBody))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "read body")
		return
	}
	var body oaiReq
	if err := json.Unmarshal(raw, &body); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "bad json")
		return
	}
	responseFormat := body.ResponseFormat
	effort := body.ReasoningEffort
	if body.Reasoning != nil && strings.TrimSpace(body.Reasoning.Effort) != "" {
		effort = body.Reasoning.Effort
	}
	if isImageModel(body.Model) {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "image model must use /v1/images/generations, not chat completions")
		return
	}
	if !isKnownPublicModel(body.Model) {
		if strictModelMode() {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "unknown model: "+body.Model)
			return
		}
		log.Printf("[model] unknown model %q; routing to default tone", body.Model)
	}
	tone, toneErr := reasoningTone(body.Model, effort)
	if toneErr != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", toneErr.Error())
		return
	}
	normalizeLegacyTools(&body)
	body.ConversationID = firstNonEmpty(body.ConversationID, body.ConversationIDC)
	body.SessionID = firstNonEmpty(body.SessionID, body.SessionIDC)
	log.Printf("[req-trace] id=%s stage=body_parsed messages=%d tools=%d choice=%s raw_bytes=%d", requestID, len(body.Messages), len(body.Tools), normalizedToolChoiceMode(body.ToolChoice), len(raw))
	if err := validateToolConversation(body.Messages); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "tool_protocol_error", err.Error())
		return
	}
	// Rebuild a protocol-neutral evidence ledger from actual tool calls/results.
	// Round limits apply only to the current user turn; full history still informs evidence.
	ledger := buildAgentLedger(body.Messages)
	activeLedger := buildAgentLedger(activeMessages(body.Messages))
	if err := activeLedger.CanContinue(maxToolRounds()); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "tool_round_limit", "message": err.Error(), "completed_calls": len(activeLedger.Completed)}})
		return
	}
	// Context budget sliding window: B = ContextWindow - MaxOutput - 512, atom-aware.
	cfgBudget := s.settings.get()
	budget := cfgBudget.ContextWindow - cfgBudget.MaxOutputTokens - 512
	if budget < 1024 {
		budget = 1024
	}
	if truncatedMsgs, truncated, budgetErr := slidingWindow(body.Messages, budget); budgetErr != nil {
		w.Header().Set("X-M365-Context-Truncated", "1")
		writeOpenAIError(w, 400, "context_length_exceeded", budgetErr.Error())
		return
	} else if truncated {
		w.Header().Set("X-M365-Context-Truncated", "1")
		log.Printf("[context-budget] id=%s truncated original=%d budget=%d truncated_msgs=%d", requestID, len(body.Messages), budget, len(truncatedMsgs))
		body.Messages = truncatedMsgs
	}
	// Preserve role boundaries when adapting OpenAI messages to ChatHub's
	// single message.text field. This keeps system/developer instructions,
	// history, and the current user turn distinguishable.
	var prompt string
	prompt, body.Attachments = flattenPromptMessages(body.Messages, body.Attachments)
	log.Printf("[req-trace] id=%s stage=prompt_flattened prompt_len=%d attachments=%d", requestID, len(prompt), len(body.Attachments))
	fmt.Printf("[multimodal-entry] messages=%d attachments=%d prompt_len=%d\n", len(body.Messages), len(body.Attachments), len(prompt))
	prompt = strings.TrimSpace(prompt)
	if responseFormat != nil {
		switch responseFormat.Type {
		case "json_object":
			prompt += "\nYou must respond with valid JSON."
		case "json_schema":
			if responseFormat.JSONSchema != nil {
				if schema, ok := responseFormat.JSONSchema["schema"]; ok {
					prompt += "\nYou must respond with valid JSON that conforms to this schema:\n" + mustJSON(schema)
				} else {
					prompt += "\nYou must respond with valid JSON."
				}
			} else {
				prompt += "\nYou must respond with valid JSON."
			}
		}
	}
	if prompt == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", "messages required")
		return
	}
	if answer, ok := publicIdentityAnswer(body.Messages, body.Model); ok && responseFormat == nil {
		s.writePublicIdentityChatResponse(w, r, &body, prompt, answer, startedAt)
		return
	}

	if body.SessionKey != "" {
		if v, ok := s.sessions.get(body.SessionKey); ok {
			body.AccountID = firstNonEmpty(body.AccountID, v.AccountID)
			body.ConversationID = firstNonEmpty(body.ConversationID, v.ConversationID)
			body.SessionID = firstNonEmpty(body.SessionID, v.SessionID)
		}
	}
	if body.User != "" && body.ConversationID == "" {
		if us, ok := s.userSessions.Get(tenantFromRequest(r), body.User); ok {
			body.AccountID = firstNonEmpty(body.AccountID, us.AccountID)
			body.ConversationID = us.ConversationID
			body.SessionID = us.SessionID
			log.Printf("[user-session] hit user=%s conversation=%s session=%s", body.User, us.ConversationID, us.SessionID)
		}
	}
	tempSession := body.Metadata != nil && body.Metadata.CopilotTempSession
	if tempSession {
		body.ConversationID = ""
		body.SessionID = ""
		log.Printf("[temp-session] copilot_temp_session=true, clearing conversation/session for one-shot request")
	}
	answerPrompt := prompt
	resolvedConversationID := ""
	if body.ConversationID == "" && len(body.Messages) > 0 && (body.Metadata == nil || !body.Metadata.CopilotTempSession) {
		resolved := s.sessionResolver.Resolve(r, &body)
		if !resolved.IsNew {
			resolvedConversationID = resolved.ConversationID
			body.ConversationID = resolved.ConversationID
			body.SessionID = resolved.SessionID
			body.AccountID = firstNonEmpty(body.AccountID, resolved.AccountID)
			log.Printf("[session-resolver] matched=%s conversation=%s history=%d total=%d", resolved.MatchedBy, resolved.ConversationID, resolved.HistoryLen, len(body.Messages))
			if resolved.HistoryLen > 0 && resolved.HistoryLen < len(body.Messages) {
				incPrompt, incAtt := flattenPromptMessages(body.Messages[resolved.HistoryLen:], nil)
				incPrompt = strings.TrimSpace(incPrompt)
				if incPrompt != "" {
					answerPrompt = incPrompt
					body.Attachments = incAtt
				}
			}
		}
	}
	if body.AccountID == "" && s.contextAffinity != nil {
		if affID, ok := s.contextAffinity.Resolve(s, r, &body); ok && affID != "" {
			body.AccountID = affID
			log.Printf("[context-affinity] hit account=%s hash=%s explicit=%q", affID, contextHash(body.Messages), r.Header.Get(sessionHeaderName))
		}
	}
	accountID := body.AccountID
	acc, err := s.resolveAccount(accountID)
	if err != nil {
		log.Printf("[account-route] resolve failed requested=%q err=%v", accountID, err)
		if isThrottledForFailover(err) {
			s.writeFailoverExhausted(w, nil)
			return
		}
		writeUpstreamErrorWithAccount(w, err, accountID)
		return
	}
	log.Printf("[account-route] selected id=%q email=%q token_present=%t oid_present=%t tid_present=%t", acc.ID, acc.Email, acc.AccessToken != "", acc.OID != "", acc.TID != "")
	if acc.OID == "" || acc.TID == "" {
		if o, t := extractOIDTID(acc.AccessToken); o != "" {
			acc.OID, acc.TID = o, t
		}
	}
	if acc.OID == "" || acc.TID == "" {
		writeOpenAIError(w, http.StatusBadRequest, "account_error", "account missing oid/tid")
		return
	}
	s.healthPing(acc.ID, acc.AccessToken, acc.OID, acc.TID, body.SessionID, requestID)

	// Conversation cache: reuse existing M365 conversation for same account+model
	// to avoid re-processing full system prompt + history each request (latency
	// drops from 3-5s to ~1s). Only kicks in when no explicit conversation ID
	// was provided by client, session key, user session, or session resolver.
	convReused := false
	convCacheModel := firstNonEmpty(body.Model, "m365-copilot")
	if body.ConversationID == "" && len(body.Messages) > 1 &&
		(body.Metadata == nil || !body.Metadata.CopilotTempSession) {
		sysHash := systemPromptHash(body.Messages)
		if cached := s.convCache.Lookup(acc.ID, convCacheModel); cached != nil && cached.SystemPrompt == sysHash {
			if len(body.Messages) > cached.MessageCount {
				incPrompt, incAtt := flattenPromptMessages(body.Messages[cached.MessageCount:], nil)
				incPrompt = strings.TrimSpace(incPrompt)
				if incPrompt != "" {
					body.ConversationID = cached.ConversationID
					body.SessionID = cached.SessionID
					answerPrompt = incPrompt
					body.Attachments = incAtt
					convReused = true
					log.Printf("[conv-cache] hit account=%s model=%s conversation=%s cached_msgs=%d new_msgs=%d", acc.ID, convCacheModel, cached.ConversationID, cached.MessageCount, len(body.Messages))
				}
			}
		}
	}
	if !convReused && body.ConversationID == "" {
		log.Printf("[conv-cache] miss account=%s model=%s", acc.ID, convCacheModel)
	}

	// Per-account overrides from the account pool batch settings.
	disableWebSearch := acc.WebSearchDisabled
	if sp := strings.TrimSpace(acc.SystemPrompt); sp != "" {
		answerPrompt = "[Account instructions]\n" + sp + "\n\n" + answerPrompt
	}

	// Normalize tools once. Selection is always made by the upstream model;
	// the gateway only validates its structured decision and converts protocols.
	toolMaps := make([]map[string]any, 0, len(body.Tools))
	for _, tool := range body.Tools {
		var f map[string]any
		_ = json.Unmarshal(tool.Function, &f)
		toolMaps = append(toolMaps, map[string]any{"type": tool.Type, "function": f})
	}
	if body.ToolChoice == nil && len(toolMaps) > 0 {
		body.ToolChoice = "auto"
	}
	var mcpServerURL string
	if len(toolMaps) > 0 {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		mcpServerURL = fmt.Sprintf("%s://%s/v1/mcp/sse", scheme, r.Host)
		log.Printf("[mcp] tools=%d mcp_gateway=%s", len(toolMaps), mcpServerURL)
	}
	validateCalls := func(stage string, calls []detectedToolCall) ([]detectedToolCall, int) {
		valid, rejected := validateDetectedToolCalls(calls, toolMaps, body.ToolChoice)
		for _, call := range rejected {
			log.Printf("[tool-validation] id=%s stage=%s rejected_name=%q reason=%q", requestID, stage, call.Name, call.Reason)
		}
		return valid, len(rejected)
	}
	planningMode := s.settings.get().ToolPlanningMode
	toolCfg := s.settings.get()

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
	defer cancel()
	account := chathub.Account{AccessToken: acc.AccessToken, OID: acc.OID, TID: acc.TID}
	localeInfo := parseLocaleFromHeaders(r)
	// The stream is opened by the actual response path below. Do not emit a
	// tool preamble here: a request may contain tools in its schema while still
	// being an ordinary text question.
	// Streaming requests must not wait for the synchronous tool router. This
	// path forwards ordinary upstream text deltas immediately; tool routing for
	// non-streaming requests remains below until the event-level tool protocol
	// is available end-to-end.
	if planningMode == "router" && body.Stream && len(toolMaps) > 0 && fmt.Sprint(body.ToolChoice) != "none" {
		// Preserve the existing validated tool router for streaming tool turns.
		// Only fall through to text streaming when the router explicitly selects
		// no tool; this prevents a natural-language preamble from becoming a
		// completed assistant turn with the actual call lost.
		routePrompt := modelToolRouterPrompt(answerPrompt+"\n"+ledger.RouterContext(), toolMaps, body.ToolChoice)
		log.Printf("[req-trace] id=%s stage=router_start prompt_len=%d", requestID, len(routePrompt))
		routeRes, routeErr := s.chatWithAccount(ctx, acc.ID, account, chathub.Request{Text: routePrompt, Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario})
		log.Printf("[req-trace] id=%s stage=router_return elapsed_ms=%d err=%t", requestID, time.Since(startedAt).Milliseconds(), routeErr != nil)
		// Router turns run in a throwaway cloud conversation that is never
		// reused by the answer turn; delete it so the conversation list does
		// not accumulate one entry per routed request.
		if routeErr == nil && routeRes.ConversationID != "" {
			s.dropTransientConversation(routeRes.ConversationID)
		}
		if routeErr != nil {
			log.Printf("[tool-router] failed account=%s err=%v", acc.ID, routeErr)
			writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "tool router: "+routeErr.Error())
			return
		}
		calls, parsed := parseModelToolDecision(routeRes.Text, toolMaps, body.ToolChoice)
		calls = filterCompletedCalls(calls, ledger)
		calls, _ = validateCalls("router", calls)
		if !parsed {
			repairRes, repairErr := s.chatWithAccount(ctx, acc.ID, account, chathub.Request{Text: `Repair this tool routing output into JSON only with shape {"calls":[{"name":"function_name","arguments":{}}]}. Use {"calls":[]} if no tool is needed. OUTPUT:\n` + compactToolResult(routeRes.Text, 6000), Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario})
			if repairErr == nil && repairRes.ConversationID != "" {
				s.dropTransientConversation(repairRes.ConversationID)
			}
			if repairErr == nil {
				calls, parsed = parseModelToolDecision(repairRes.Text, toolMaps, body.ToolChoice)
				calls = filterCompletedCalls(calls, ledger)
				calls, _ = validateCalls("router", calls)
			}
		}
		if parsed && len(calls) > 0 {
			scope := fmt.Sprintf("%d:%v:stream", len(body.Messages), completedCallIDs(ledger))
			for i := range calls {
				calls[i].ID = scopedCallID(calls[i].Name, string(calls[i].Arguments), i, scope)
			}
			calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
			if body.ParallelToolCalls != nil && !*body.ParallelToolCalls && len(calls) > 1 {
				calls = calls[:1]
			}
			_ = writeToolResponse(w, "chatcmpl-"+uuid.NewString(), firstNonEmpty(body.Model, "m365-copilot"), true, body.shouldSendStreamUsage(), calls, routeRes)
			return
		}
	}
	if body.Stream {
		answerReq := buildAnswerRequest(answerPrompt, tone, body, ledger, planningMode, mcpServerURL, s.settings.get(), s.featureFlags(), localeInfo, body.Metadata != nil && body.Metadata.CopilotTempSession)
		answerReq.DisableWebSearch = disableWebSearch
		answerReq.FirstTokenTimeout = time.Duration(s.settings.get().FirstTokenTimeoutSeconds) * time.Second
		answerPrompt = answerReq.Text
		log.Printf("[req-trace] id=%s stage=answer_start prompt_len=%d native_tools=%d mcp=%s", requestID, len(answerPrompt), len(answerReq.Tools), mcpServerURL)
		id := "chatcmpl-" + uuid.NewString()
		model := firstNonEmpty(body.Model, "m365-copilot")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", "stream unsupported")
			return
		}
		sw := newSSEWriter(w, flusher)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		keepaliveDone := make(chan struct{})
		defer close(keepaliveDone)
		go func() {
			for {
				select {
				case <-keepaliveDone:
					return
				case <-r.Context().Done():
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					if sw.isCommitted() {
						_ = sw.raw(": keepalive\n\n")
					}
				}
			}
		}()
		var text strings.Builder
		var pending strings.Builder
		var streamedTools []detectedToolCall
		first := true
		identityFilter := newPublicIdentityStreamFilter(model)
		emitText := func(part string) error {
			if part == "" {
				return nil
			}
			part = identityFilter.Push(part)
			if part == "" {
				return nil
			}
			if err := r.Context().Err(); err != nil {
				return err
			}
			delta := map[string]any{"content": part}
			if first {
				delta["role"] = "assistant"
				first = false
			}
			chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}}
			if err := sw.data(mustJSON(chunk)); err != nil {
				return err
			}
			return nil
		}
		reasoningFilter := newPublicReasoningStreamFilter()
		emitReasoning := func(part string) error {
			if part == "" {
				return nil
			}
			if err := r.Context().Err(); err != nil {
				return err
			}
			delta := map[string]any{"reasoning_content": part}
			if first {
				delta["role"] = "assistant"
				first = false
			}
			chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}}
			return sw.data(mustJSON(chunk))
		}
		res, err := s.chatWithAccountEvents(ctx, acc.ID, account, answerReq, func(ev chathub.StreamEvent) error {
			if ev.Kind == "reasoning" {
				return emitReasoning(reasoningFilter.Push(ev.Text))
			}
			if ev.Kind == "tool" && ev.ToolName != "" && len(ev.Arguments) > 0 {
				streamedTools = append(streamedTools, detectedToolCall{ID: "call_" + uuid.NewString(), Name: ev.ToolName, Arguments: ev.Arguments})
				return nil
			}
			if ev.Kind != "text" || ev.Text == "" {
				return nil
			}
			text.WriteString(ev.Text)
			pending.WriteString(ev.Text)
			v := pending.String()
			// Detect fenced code blocks (tool calls) that must not be emitted as text.
			// They will be caught by fencedToolCalls after the stream completes.
			if strings.Contains(v, "```bash") || strings.Contains(v, "\"command\"") {
				return nil
			}
			// If we see an opening ```, buffer until the closing ``` or until we're sure it's not a tool call.
			if i := strings.Index(v, "```"); i >= 0 {
				after := v[i+3:]
				// If there's a closing ```, emit everything up to and including the block.
				if j := strings.Index(after, "```"); j >= 0 {
					closeIdx := i + 3 + j + 3
					if err := emitText(v[:i]); err != nil {
						return err
					}
					pending.Reset()
					pending.WriteString(v[i:closeIdx])
					return nil
				}
				// Opening ``` without closing yet: emit everything before it, keep the fence buffered.
				if err := emitText(v[:i]); err != nil {
					return err
				}
				pending.Reset()
				pending.WriteString(v[i:])
				return nil
			}
			// No fence detected: emit immediately with a small tail buffer for fence detection.
			// This replaces the old 8-rune threshold with a 3-rune buffer (enough to detect "```").
			if runeCount := utf8.RuneCountInString(v); runeCount > 3 {
				cut := 0
				seen := 0
				for i := range v {
					if seen == runeCount-3 {
						cut = i
						break
					}
					seen++
				}
				if err := emitText(v[:cut]); err != nil {
					return err
				}
				pending.Reset()
				pending.WriteString(v[cut:])
			}
			return nil
		})
		if err != nil && text.Len() == 0 && len(streamedTools) == 0 && !convReused && body.AccountID == "" && (body.ConversationID == "" || body.ConversationID == resolvedConversationID) && isThrottledForFailover(err) {
			tried := map[string]bool{acc.ID: true}
			attempted := []string{acc.ID}
			s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
			origErr := err
			_ = origErr
			succeeded := false
			for {
				next, ok := s.nextHealthyExcluding(tried)
				if !ok {
					// no healthy alternative, will report exhausted below
					break
				}
				tried[next.ID] = true
				attempted = append(attempted, next.ID)
				failoverReq := answerReq
				if body.ConversationID == resolvedConversationID {
					failoverReq.ConversationID = ""
					failoverReq.SessionID = ""
				}
				ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
				res2, err2 := s.chatWithAccountEvents(ctx2, next.ID, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, failoverReq, func(ev chathub.StreamEvent) error {
					if ev.Kind == "tool" && ev.ToolName != "" && len(ev.Arguments) > 0 {
						streamedTools = append(streamedTools, detectedToolCall{ID: "call_" + uuid.NewString(), Name: ev.ToolName, Arguments: ev.Arguments})
						return nil
					}
					if ev.Kind != "text" || ev.Text == "" {
						return nil
					}
					text.WriteString(ev.Text)
					pending.WriteString(ev.Text)
					v := pending.String()
					if strings.Contains(v, "```bash") || strings.Contains(v, "\"command\"") {
						return nil
					}
					if i := strings.Index(v, "```"); i >= 0 {
						if err := emitText(v[:i]); err != nil {
							return err
						}
						pending.Reset()
						pending.WriteString(v[i:])
						return nil
					}
					if runeCount := utf8.RuneCountInString(v); runeCount > 8 {
						cut := 0
						seen := 0
						for i := range v {
							if seen == runeCount-8 {
								cut = i
								break
							}
							seen++
						}
						if err := emitText(v[:cut]); err != nil {
							return err
						}
						pending.Reset()
						pending.WriteString(v[cut:])
					}
					return nil
				})
				cancel2()
				if err2 == nil {
					s.accountPool.MarkSuccess(next.ID)
					res = res2
					acc = next
					err = nil
					succeeded = true
					break
				}
				if isThrottledForFailover(err2) {
					s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
					err = err2
					continue
				}
				s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
				if errors.Is(err2, chathub.ErrImageLimit) && s.accountPool != nil {
					s.accountPool.MarkImageLimited(next.ID)
				}
				err = err2
				acc = next
				break
			}
			if !succeeded && isThrottledForFailover(err) && len(attempted) > 1 {
				// check if no healthy left
				if _, ok := s.nextHealthyExcluding(tried); !ok {
					s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
					_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(map[string]any{"error": map[string]any{"message": "所有账号均触发限流，请稍后重试", "type": "rate_limit_error", "code": "rate_limit_exhausted"}, "total_accounts": len(s.tokens.List()), "throttled_accounts": func() int { _, t, _, _ := s.failoverStats(attempted); return t }(), "healthy_accounts": func() int { _, _, h, _ := s.failoverStats(attempted); return h }(), "attempted_accounts": len(attempted), "failover_status": "exhausted"})+"\n\n")
					_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
					return
				}
			}
		} else if err != nil && text.Len() == 0 && len(streamedTools) == 0 && !convReused && body.AccountID == "" && (body.ConversationID == "" || body.ConversationID == resolvedConversationID) && IsAuthFailure(err) {
			originalErr := err
			next, nerr := s.nextHealthyAccount(acc.ID)
			if nerr == nil {
				failoverReq := answerReq
				if body.ConversationID == resolvedConversationID {
					failoverReq.ConversationID = ""
					failoverReq.SessionID = ""
				}
				ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
				defer cancel2()
				res2, err2 := s.chatWithAccountEvents(ctx2, next.ID, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, failoverReq, func(ev chathub.StreamEvent) error {
					if ev.Kind == "tool" && ev.ToolName != "" && len(ev.Arguments) > 0 {
						streamedTools = append(streamedTools, detectedToolCall{ID: "call_" + uuid.NewString(), Name: ev.ToolName, Arguments: ev.Arguments})
						return nil
					}
					if ev.Kind != "text" || ev.Text == "" {
						return nil
					}
					text.WriteString(ev.Text)
					pending.WriteString(ev.Text)
					v := pending.String()
					if strings.Contains(v, "```bash") || strings.Contains(v, "\"command\"") {
						return nil
					}
					if i := strings.Index(v, "```"); i >= 0 {
						if err := emitText(v[:i]); err != nil {
							return err
						}
						pending.Reset()
						pending.WriteString(v[i:])
						return nil
					}
					if runeCount := utf8.RuneCountInString(v); runeCount > 8 {
						cut := 0
						seen := 0
						for i := range v {
							if seen == runeCount-8 {
								cut = i
								break
							}
							seen++
						}
						if err := emitText(v[:cut]); err != nil {
							return err
						}
						pending.Reset()
						pending.WriteString(v[cut:])
					}
					return nil
				})
				if err2 == nil {
					s.accountPool.MarkFailure(acc.ID, originalErr, s.getRateLimitCooldown())
					if errors.Is(originalErr, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(acc.ID)
					}
					s.accountPool.MarkSuccess(next.ID)
					res = res2
					acc = next
					err = nil
				} else {
					s.accountPool.MarkFailure(acc.ID, originalErr, s.getRateLimitCooldown())
					if errors.Is(originalErr, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(acc.ID)
					}
					s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
					if errors.Is(err2, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(next.ID)
					}
					err = err2
				}
			}
		}
		if err != nil {
			log.Printf("[req-trace] id=%s stage=stream_error err=%v", requestID, err)
			s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
			if errors.Is(err, chathub.ErrImageLimit) && s.accountPool != nil {
				s.accountPool.MarkImageLimited(acc.ID)
			}
			if convReused {
				s.invalidateConvCache(acc.ID, convCacheModel)
			}
			msg := upstreamError(err)
			if IsRateLimited(err) {
				msg = "upstream is rate limiting; try again shortly"
			}
			if errors.Is(err, chathub.ErrOffensiveContent) {
				msg = "M365 content policy flagged this request as offensive"
			}
			msg = sanitizePublicInternalText(msg)
			_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(map[string]any{"error": map[string]any{"message": msg, "code": "rate_limit"}})+"\n\n")
			_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
			return
		}
		s.accountPool.MarkSuccess(acc.ID)
		s.applyResultMetering(acc.ID, res)
		if isContentPolicyBlock(res.Text) {
			log.Printf("[content-policy] M365 blocked the request (streaming), sending error")
			s.accountPool.MarkFailure(acc.ID, chathub.ErrOffensiveContent, s.getRateLimitCooldown())
			_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(map[string]any{"error": map[string]any{"message": "M365 content policy blocked this request; try again or switch account", "code": "upstream_content_blocked"}})+"\n\n")
			_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
			return
		}
		if isImageLimitNotice(res.Text) {
			if s.accountPool != nil {
				s.accountPool.MarkImageLimited(acc.ID)
			}
		}
		if text.Len() == 0 && strings.TrimSpace(res.Text) != "" {
			text.WriteString(res.Text)
			pending.WriteString(res.Text)
		}
		rawCalls := streamedTools
		if len(rawCalls) == 0 {
			rawCalls = fencedToolCalls(text.String(), toolMaps, body.ToolChoice)
		}
		calls, rejected := validateCalls("stream", rawCalls)
		toolResult := chathub.Result{Text: text.String()}
		if len(calls) == 0 && rejected > 0 {
			// A native ChatHub event can contain a fabricated or empty tool name.
			// Do not leak it to the local runner: ask the model to remap the intent
			// to exactly one of the tools the client actually declared.
			repairPrompt := modelToolRouterPrompt(prompt+"\n"+ledger.RouterContext(), toolMaps, "required") +
				"\nREPAIR RULE: The previous upstream event selected an undeclared tool. Select one declared tool that performs the intended operation. Never return unknown_tool."
			repairRes, repairErr := s.chatWithAccount(ctx, acc.ID, account, chathub.Request{Text: repairPrompt, Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario})
			if repairErr == nil {
				repaired, parsed := parseModelToolDecision(repairRes.Text, toolMaps, body.ToolChoice)
				if parsed {
					calls, _ = validateCalls("stream-repair", repaired)
					if len(calls) > 0 {
						toolResult = repairRes
					}
				}
			}
			if len(calls) == 0 {
				log.Printf("[tool-validation] id=%s stage=stream-repair failed", requestID)
				_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(map[string]any{"error": map[string]any{"message": "upstream selected an undeclared tool and repair failed", "code": "invalid_tool_call"}})+"\n\n")
				_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
				return
			}
		}
		if len(calls) > 0 {
			log.Printf("[req-trace] id=%s stage=tool_calls_detected count=%d names=%v", requestID, len(calls), func() []string {
				var n []string
				for _, c := range calls {
					n = append(n, c.Name)
				}
				return n
			}())
			calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
			if body.ParallelToolCalls != nil && !*body.ParallelToolCalls && len(calls) > 1 {
				calls = calls[:1]
			}
			_ = writeToolResponse(w, id, model, true, body.shouldSendStreamUsage(), calls, toolResult)
			if body.User != "" && res.ConversationID != "" {
				s.userSessions.Put(tenantFromRequest(r), body.User, res.ConversationID, res.SessionID, acc.ID)
			}
			s.bindConversation(acc, &body, r, res, answerPrompt, startedAt)
			s.storeConvCache(acc.ID, convCacheModel, res, tone, body.Messages, convReused)
			return
		}
		if err := emitText(pending.String()); err != nil {
			log.Printf("[req-trace] id=%s stage=stream_write err=%v", requestID, err)
			return
		}
		if err := emitReasoning(reasoningFilter.Flush()); err != nil {
			log.Printf("[req-trace] id=%s stage=stream_write_reasoning err=%v", requestID, err)
			return
		}
		finishChunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}}
		if res.Throttling != nil {
			finishChunk["x_m365_throttling"] = res.Throttling
		}
		if len(res.Scores) > 0 {
			finishChunk["x_m365_scores"] = res.Scores
		}
		_ = sw.data(mustJSON(finishChunk))
		_ = sw.data("[DONE]")
		if res.Timestamps.RequestSent != "" {
			_ = sw.raw(": m365-metrics " + mustJSON(res.Timestamps) + "\n\n")
		}
		if body.User != "" && res.ConversationID != "" {
			s.userSessions.Put(tenantFromRequest(r), body.User, res.ConversationID, res.SessionID, acc.ID)
		}
		s.bindConversation(acc, &body, r, res, answerPrompt, startedAt)
		s.storeConvCache(acc.ID, convCacheModel, res, tone, body.Messages, convReused)
		return
	}
	// Ask the upstream model to select and validate the next tool. The gateway
	// remains tool-agnostic; it only validates and serializes the decision.
	if planningMode == "router" && len(toolMaps) > 0 && fmt.Sprint(body.ToolChoice) != "none" {
		routePrompt := modelToolRouterPrompt(answerPrompt+"\n"+ledger.RouterContext(), toolMaps, body.ToolChoice)
		routeRes, routeErr := s.chatWithAccount(ctx, acc.ID, account, chathub.Request{Text: routePrompt, Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario})
		if routeErr != nil {
			s.accountPool.MarkFailure(acc.ID, routeErr, s.getRateLimitCooldown())
			if IsFailoverRetriable(routeErr) {
				next, nerr := s.nextHealthyAccount(acc.ID)
				if nerr == nil {
					ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
					defer cancel2()
					if res2, err2 := s.chatWithAccount(ctx2, next.ID, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, chathub.Request{Text: routePrompt, Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario}); err2 == nil {
						routeRes, routeErr = res2, nil
						acc = next
						account = chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}
					} else {
						s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
					}
				}
			}
			if routeErr != nil {
				msg := upstreamError(routeErr)
				if IsRateLimited(routeErr) {
					msg = "upstream is rate limiting; try again shortly"
				}
				writeOpenAIError(w, http.StatusBadGateway, "tool_router_error", msg)
				return
			}
			s.accountPool.MarkSuccess(acc.ID)
		}
		calls, parsed := parseModelToolDecision(routeRes.Text, toolMaps, body.ToolChoice)
		if !parsed {
			repairRes, repairErr := s.chatWithAccount(ctx, acc.ID, account, chathub.Request{Text: `Repair this tool routing output into JSON only with shape {"calls":[{"name":"function_name","arguments":{}}]}. Do not invent calls; use {"calls":[]} if unrecoverable. OUTPUT:
` + compactToolResult(routeRes.Text, 6000), Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario})
			if repairErr == nil {
				calls, parsed = parseModelToolDecision(repairRes.Text, toolMaps, body.ToolChoice)
			}
			if !parsed {
				writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "model returned an invalid tool routing decision")
				return
			}
		}
		calls = filterCompletedCalls(calls, ledger)
		calls, _ = validateCalls("router", calls)
		if len(calls) > 0 {
			scope := fmt.Sprintf("%d:%v", len(body.Messages), completedCallIDs(ledger))
			for i := range calls {
				calls[i].ID = scopedCallID(calls[i].Name, string(calls[i].Arguments), i, scope)
			}
			calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
			if body.ParallelToolCalls != nil && !*body.ParallelToolCalls && len(calls) > 1 {
				calls = calls[:1]
			}
			_ = writeToolResponse(w, "chatcmpl-"+uuid.NewString(), firstNonEmpty(body.Model, "m365-copilot"), body.Stream, body.shouldSendStreamUsage(), calls, routeRes)
			return
		}
		if fmt.Sprint(body.ToolChoice) == "required" {
			defs, _ := json.Marshal(toolMaps)
			retryText := `Select at least one required next tool call from FUNCTION_DEFINITIONS. Validate every argument against its schema. Return JSON only as {"calls":[{"name":"function_name","arguments":{}}]}.
APPLICATION_REQUEST_AND_EVIDENCE:
` + prompt + "\n" + ledger.RouterContext() + "\nFUNCTION_DEFINITIONS:\n" + string(defs)
			retryRes, retryErr := s.chatWithAccount(ctx, acc.ID, account, chathub.Request{Text: retryText, Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario})
			if retryErr == nil {
				calls, parsed = parseModelToolDecision(retryRes.Text, toolMaps, body.ToolChoice)
				calls = filterCompletedCalls(calls, ledger)
				calls, _ = validateCalls("router", calls)
				if parsed && len(calls) > 0 {
					scope := fmt.Sprintf("%d:%v:required-retry", len(body.Messages), completedCallIDs(ledger))
					for i := range calls {
						calls[i].ID = scopedCallID(calls[i].Name, string(calls[i].Arguments), i, scope)
					}
					calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
					if body.ParallelToolCalls != nil && !*body.ParallelToolCalls && len(calls) > 1 {
						calls = calls[:1]
					}
					_ = writeToolResponse(w, "chatcmpl-"+uuid.NewString(), firstNonEmpty(body.Model, "m365-copilot"), body.Stream, body.shouldSendStreamUsage(), calls, retryRes)
					return
				}
			}
			writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "model did not select a required tool after constrained retry")
			return
		}
	}
	answerReq := buildAnswerRequest(answerPrompt, tone, body, ledger, planningMode, mcpServerURL, s.settings.get(), s.featureFlags(), localeInfo, body.Metadata != nil && body.Metadata.CopilotTempSession)
	answerReq.DisableWebSearch = disableWebSearch
	answerReq.FirstTokenTimeout = time.Duration(s.settings.get().FirstTokenTimeoutSeconds) * time.Second
	answerPrompt = answerReq.Text
	var res chathub.Result
	// NOTE: streaming is fully handled by the earlier `if body.Stream` branch,
	// which always returns before reaching here, so this duplicate stream
	// branch is unreachable. Do not add new streaming logic here — the live
	// path is the one above (issue #83 reasoning streaming was fixed there).
	if body.Stream {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", "stream unsupported")
			return
		}
		id := "chatcmpl-" + uuid.NewString()
		model := firstNonEmpty(body.Model, "m365-copilot")
		firstDelta := true
		sw2 := newSSEWriter(w, flusher)
		writeChunk := func(delta map[string]any) error {
			if err := r.Context().Err(); err != nil {
				return err
			}
			// The first SSE chunk must carry the assistant role; subsequent
			// chunks carry content or reasoning deltas.
			if firstDelta {
				firstDelta = false
				withRole := map[string]any{"role": "assistant", "content": nil}
				for k, v := range delta {
					withRole[k] = v
				}
				delta = withRole
			}
			chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []map[string]any{{"index": 0, "delta": delta}}}
			return sw2.data(mustJSON(chunk))
		}
		contentFilter := newPublicIdentityStreamFilter(firstNonEmpty(body.Model, defaultPublicModelName))
		reasoningFilter := newPublicReasoningStreamFilter()
		streamedContentLen := 0
		onDelta := func(content string) error {
			if content = contentFilter.Push(content); content != "" {
				streamedContentLen += len(content)
				return writeChunk(map[string]any{"content": content})
			}
			return nil
		}
		onReasoning := func(reasoning string) error {
			if reasoning = reasoningFilter.Push(reasoning); reasoning != "" {
				return writeChunk(map[string]any{"reasoning_content": reasoning})
			}
			return nil
		}
		res, err = s.chatWithAccountReasoning(ctx, acc.ID, account, answerReq, onDelta, onReasoning)
		if err != nil && !sw2.isCommitted() && !convReused && body.AccountID == "" && (body.ConversationID == "" || body.ConversationID == resolvedConversationID) && isThrottledForFailover(err) {
			tried := map[string]bool{acc.ID: true}
			attempted := []string{acc.ID}
			s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
			for {
				next, ok := s.nextHealthyExcluding(tried)
				if !ok {
					break
				}
				tried[next.ID] = true
				attempted = append(attempted, next.ID)
				failoverReq := answerReq
				if body.ConversationID == resolvedConversationID {
					failoverReq.ConversationID = ""
					failoverReq.SessionID = ""
				}
				ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
				res2, err2 := s.chatWithAccountReasoning(ctx2, next.ID, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, failoverReq, onDelta, onReasoning)
				cancel2()
				if err2 == nil {
					s.accountPool.MarkSuccess(next.ID)
					res = res2
					acc = next
					err = nil
					break
				}
				if isThrottledForFailover(err2) {
					s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
					err = err2
					acc = next
					continue
				}
				s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
				if errors.Is(err2, chathub.ErrImageLimit) && s.accountPool != nil {
					s.accountPool.MarkImageLimited(next.ID)
				}
				err = err2
				acc = next
				break
			}
			if err != nil && isThrottledForFailover(err) {
				if _, ok := s.nextHealthyExcluding(tried); !ok {
					_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(map[string]any{"error": map[string]any{"message": "所有账号均触发限流，请稍后重试", "type": "rate_limit_error", "code": "rate_limit_exhausted"}, "total_accounts": len(s.tokens.List()), "throttled_accounts": func() int { _, t, _, _ := s.failoverStats(attempted); return t }(), "healthy_accounts": func() int { _, _, h, _ := s.failoverStats(attempted); return h }(), "attempted_accounts": len(attempted), "failover_status": "exhausted"})+"\n\n")
					_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
					return
				}
			}
		} else if err != nil && !sw2.isCommitted() && !convReused && body.AccountID == "" && (body.ConversationID == "" || body.ConversationID == resolvedConversationID) && IsAuthFailure(err) {
			originalErr := err
			next, nerr := s.nextHealthyAccount(acc.ID)
			if nerr == nil {
				failoverReq := answerReq
				if body.ConversationID == resolvedConversationID {
					failoverReq.ConversationID = ""
					failoverReq.SessionID = ""
				}
				ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
				defer cancel2()
				if res2, err2 := s.chatWithAccountReasoning(ctx2, next.ID, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, failoverReq, onDelta, onReasoning); err2 == nil {
					s.accountPool.MarkFailure(acc.ID, originalErr, s.getRateLimitCooldown())
					if errors.Is(originalErr, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(acc.ID)
					}
					s.accountPool.MarkSuccess(next.ID)
					res = res2
					acc = next
					err = nil
				} else {
					s.accountPool.MarkFailure(acc.ID, originalErr, s.getRateLimitCooldown())
					if errors.Is(originalErr, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(acc.ID)
					}
					s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
					if errors.Is(err2, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(next.ID)
					}
					err = err2
				}
			}
		}
		if err == nil {
			if content := contentFilter.Flush(); content != "" {
				streamedContentLen += len(content)
				if writeErr := writeChunk(map[string]any{"content": content}); writeErr != nil {
					return
				}
			}
			if reasoning := reasoningFilter.Flush(); reasoning != "" {
				if writeErr := writeChunk(map[string]any{"reasoning_content": reasoning}); writeErr != nil {
					return
				}
			}
			res.Text = sanitizePublicAssistantTextForModel(res.Text, body.Model)
			if fallback := streamFinalResultFallback(streamedContentLen, res.Text); fallback != "" {
				if writeErr := writeChunk(map[string]any{"content": fallback}); writeErr != nil {
					return
				}
				streamedContentLen += len(fallback)
			}
			res.Reasoning = sanitizePublicReasoningText(res.Reasoning)
			s.accountPool.MarkSuccess(acc.ID)
			if res.Throttling != nil && s.accountPool != nil {
				s.accountPool.UpdateThrottling(acc.ID, res.Throttling)
				s.logThrottlingWarning(acc.ID, res.Throttling)
			}
			if isContentPolicyBlock(res.Text) {
				log.Printf("[content-policy] M365 blocked the request (reasoning stream), sending error")
				s.accountPool.MarkFailure(acc.ID, chathub.ErrOffensiveContent, s.getRateLimitCooldown())
				_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(map[string]any{"error": map[string]any{"message": "M365 content policy blocked this request; try again or switch account", "code": "upstream_content_blocked"}})+"\n\n")
				_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
				return
			}
			if isImageLimitNotice(res.Text) {
				if s.accountPool != nil {
					s.accountPool.MarkImageLimited(acc.ID)
				}
			}
		} else {
			log.Printf("[req-trace] id=%s stage=stream_error err=%v", requestID, err)
			s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
			if errors.Is(err, chathub.ErrImageLimit) && s.accountPool != nil {
				s.accountPool.MarkImageLimited(acc.ID)
			}
			if convReused {
				s.invalidateConvCache(acc.ID, convCacheModel)
			}
			msg := upstreamError(err)
			if IsRateLimited(err) {
				msg = "upstream is rate limiting; try again shortly"
			}
			if errors.Is(err, chathub.ErrOffensiveContent) {
				msg = "M365 content policy flagged this request as offensive"
			}
			msg = sanitizePublicInternalText(msg)
			if !sw2.isCommitted() {
				writeOpenAIError(w, http.StatusBadGateway, "upstream_error", msg)
				return
			}
			_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(map[string]any{"error": map[string]any{"message": msg, "code": "rate_limit"}})+"\n\n")
		}
		pt := EstimateTokens(prompt)
		ct := EstimateTokens(res.Text)
		log.Printf("[usage] stream id=%s pt=%d ct=%d res.Text=%d", id, pt, ct, len(res.Text))
		if err == nil && ct == 0 {
			_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(map[string]any{"error": map[string]any{"message": "upstream returned empty completion; the requested model may be unavailable for this tenant", "code": "upstream_error"}})+"\n\n")
		}
		finish := "stop"
		if err != nil {
			finish = "error"
		}
		usageChunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}, "usage": map[string]any{"prompt_tokens": pt, "completion_tokens": ct, "total_tokens": pt + ct}}
		if res.Throttling != nil {
			usageChunk["x_m365_throttling"] = res.Throttling
		}
		if len(res.Scores) > 0 {
			usageChunk["x_m365_scores"] = res.Scores
		}
		_ = sw2.data(mustJSON(usageChunk))
		_ = sw2.data("[DONE]")
		if res.Timestamps.RequestSent != "" {
			_ = sw2.raw(": m365-metrics " + mustJSON(res.Timestamps) + "\n\n")
		}
	} else {
		res, err = s.chatWithAccount(ctx, acc.ID, account, answerReq)
		if IsEmptyCompletion(err) && tone != "magic" {
			log.Printf("[tone-fallback] tone=%q returned empty, retrying with magic", tone)
			magicReq := answerReq
			magicReq.Tone = "magic"
			if res2, err2 := s.chatWithAccount(ctx, acc.ID, account, magicReq); err2 == nil && res2.Text != "" {
				res = res2
				err = nil
			}
		}
		if err != nil && !convReused && body.AccountID == "" && (body.ConversationID == "" || body.ConversationID == resolvedConversationID) && isThrottledForFailover(err) {
			tried := map[string]bool{acc.ID: true}
			attempted := []string{acc.ID}
			s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
			for {
				next, ok := s.nextHealthyExcluding(tried)
				if !ok {
					s.writeFailoverExhausted(w, attempted)
					return
				}
				tried[next.ID] = true
				attempted = append(attempted, next.ID)
				failoverReq := answerReq
				if body.ConversationID == resolvedConversationID {
					failoverReq.ConversationID = ""
					failoverReq.SessionID = ""
				}
				ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
				res2, err2 := s.chatWithAccount(ctx2, next.ID, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, failoverReq)
				cancel2()
				if err2 == nil {
					s.accountPool.MarkSuccess(next.ID)
					res = res2
					acc = next
					err = nil
					break
				}
				if isThrottledForFailover(err2) {
					s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
					err = err2
					acc = next
					continue
				}
				s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
				if errors.Is(err2, chathub.ErrImageLimit) && s.accountPool != nil {
					s.accountPool.MarkImageLimited(next.ID)
				}
				err = err2
				acc = next
				break
			}
			if err != nil && isThrottledForFailover(err) {
				if _, ok := s.nextHealthyExcluding(tried); !ok {
					s.writeFailoverExhausted(w, attempted)
					return
				}
			}
		} else if err != nil && !convReused && body.AccountID == "" && (body.ConversationID == "" || body.ConversationID == resolvedConversationID) && IsAuthFailure(err) {
			originalErr := err
			next, nerr := s.nextHealthyAccount(acc.ID)
			if nerr == nil {
				failoverReq := answerReq
				if body.ConversationID == resolvedConversationID {
					failoverReq.ConversationID = ""
					failoverReq.SessionID = ""
				}
				ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
				defer cancel2()
				res2, err2 := s.chatWithAccount(ctx2, next.ID, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, failoverReq)
				if err2 == nil {
					s.accountPool.MarkFailure(acc.ID, originalErr, s.getRateLimitCooldown())
					if errors.Is(originalErr, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(acc.ID)
					}
					s.accountPool.MarkSuccess(next.ID)
					res = res2
					acc = next
					err = nil
				} else {
					s.accountPool.MarkFailure(acc.ID, originalErr, s.getRateLimitCooldown())
					if errors.Is(originalErr, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(acc.ID)
					}
					s.accountPool.MarkFailure(next.ID, err2, s.getRateLimitCooldown())
					if errors.Is(err2, chathub.ErrImageLimit) && s.accountPool != nil {
						s.accountPool.MarkImageLimited(next.ID)
					}
					err = err2
				}
			}
		}
	}
	if err != nil {
		s.accountPool.MarkFailure(acc.ID, err, s.getRateLimitCooldown())
		if errors.Is(err, chathub.ErrImageLimit) && s.accountPool != nil {
			s.accountPool.MarkImageLimited(acc.ID)
		}
		if convReused {
			s.invalidateConvCache(acc.ID, convCacheModel)
			log.Printf("[conv-cache] invalidated account=%s model=%s after error: %v", acc.ID, convCacheModel, err)
		}
		if isThrottledForFailover(err) {
			s.writeFailoverExhausted(w, []string{acc.ID})
			return
		}
		writeUpstreamErrorWithAccount(w, err, acc.ID)
		return
	}
	s.accountPool.MarkSuccess(acc.ID)
	s.applyResultMetering(acc.ID, res)
	if body.Stream {
		if !tempSession && body.User != "" && res.ConversationID != "" {
			s.userSessions.Put(tenantFromRequest(r), body.User, res.ConversationID, res.SessionID, acc.ID)
		}
		s.bindConversation(acc, &body, r, res, prompt, startedAt)
		if !tempSession {
			s.storeConvCache(acc.ID, convCacheModel, res, tone, body.Messages, convReused)
		}
		return
	}

	if body.SessionKey != "" {
		s.sessions.upsert(conversation{ID: body.SessionKey, AccountID: acc.ID, ConversationID: res.ConversationID, SessionID: res.SessionID, Title: prompt})
	}
	if !tempSession && body.User != "" && res.ConversationID != "" {
		s.userSessions.Put(tenantFromRequest(r), body.User, res.ConversationID, res.SessionID, acc.ID)
		log.Printf("[user-session] put user=%s conversation=%s session=%s", body.User, res.ConversationID, res.SessionID)
	}
	if res.ConversationID != "" {
		s.bindConversation(acc, &body, r, res, prompt, startedAt)
		if !tempSession {
			s.storeConvCache(acc.ID, convCacheModel, res, tone, body.Messages, convReused)
		}
	}
	if res.ConversationID != "" {
		resolved := s.sessionResolver.Resolve(r, &body)
		if !resolved.IsNew {
			w.Header().Set(sessionHeaderName, resolved.SessionID)
		}
	}
	model := body.Model
	if model == "" {
		model = "m365-copilot"
	}
	id := "chatcmpl-" + uuid.NewString()
	if len(toolMaps) > 0 && isToolRefusal(res.Text) {
		log.Printf("[tool-eject] model refused tools, retrying with correction")
		correction := "Your previous response incorrectly denied that caller tools are available. They are real, active, and callable on the caller's Windows machine. Call the appropriate tool now. Do not explain tool availability.\n\nUser request:\n" + prompt
		res2, err2 := s.chatWithAccount(ctx, acc.ID, account, chathub.Request{Text: correction, Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario})
		if err2 == nil && !isToolRefusal(res2.Text) {
			res = res2
		}
	}
	if len(toolMaps) > 0 && isSandboxHallucination(res.Text) {
		log.Printf("[sandbox-eject] model used code interpreter/sandbox, retrying with explicit tool instruction")
		correction := "CRITICAL: You must NOT use any built-in code interpreter, Python sandbox, or cloud execution environment. The caller has provided a bash tool that runs Windows PowerShell 5.1 on their local machine — use it to execute any commands or code. Do NOT say you cannot run code. Do NOT say you only have a Linux container. Do NOT say you have no Windows execution channel. You DO have a bash tool that runs on Windows. Call the bash tool NOW with the appropriate PowerShell command.\n\nUser request:\n" + prompt
		res2, err2 := s.chatWithAccount(ctx, acc.ID, account, chathub.Request{Text: correction, Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario})
		if err2 == nil && !isSandboxHallucination(res2.Text) {
			res = res2
		}
	}
	invalidDetectedTool := false
	if rawCalls := fencedToolCalls(res.Text, toolMaps, body.ToolChoice); len(rawCalls) > 0 {
		calls, rejected := validateCalls("fenced", rawCalls)
		invalidDetectedTool = rejected > 0
		if len(calls) > 0 {
			calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
			if body.ParallelToolCalls != nil && !*body.ParallelToolCalls && len(calls) > 1 {
				calls = calls[:1]
			}
			_ = writeToolResponse(w, id, model, body.Stream, body.shouldSendStreamUsage(), calls, res)
			return
		}
	}
	if rawCalls := nativeToolCalls(res.Events, body.Tools); len(rawCalls) > 0 {
		calls, rejected := validateCalls("native", rawCalls)
		invalidDetectedTool = invalidDetectedTool || rejected > 0
		if len(calls) > 0 {
			calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
			if body.ParallelToolCalls != nil && !*body.ParallelToolCalls && len(calls) > 1 {
				calls = calls[:1]
			}
			_ = writeToolResponse(w, id, model, body.Stream, body.shouldSendStreamUsage(), calls, res)
			return
		}
	}
	// Recover natural-language tool intent in native mode, and repair any
	// structured event that failed the declared-name/schema boundary.
	if (planningMode == "native" || invalidDetectedTool) && len(toolMaps) > 0 && fmt.Sprint(body.ToolChoice) != "none" {
		routePrompt := modelToolRouterPrompt(prompt+"\n"+ledger.RouterContext(), toolMaps, body.ToolChoice)
		routeRes, routeErr := s.chatWithAccount(ctx, acc.ID, account, chathub.Request{Text: routePrompt, Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario})
		if routeErr == nil {
			calls, parsed := parseModelToolDecision(routeRes.Text, toolMaps, body.ToolChoice)
			if !parsed {
				repairRes, repairErr := s.chatWithAccount(ctx, acc.ID, account, chathub.Request{Text: `Repair this tool routing output into JSON only with shape {"calls":[{"name":"function_name","arguments":{}}]}. Use {"calls":[]} if no tool is needed. OUTPUT:\n` + compactToolResult(routeRes.Text, 6000), Tone: tone, Attachments: body.Attachments, LicenseType: toolCfg.LicenseType, Scenario: toolCfg.Scenario})
				if repairErr == nil {
					calls, parsed = parseModelToolDecision(repairRes.Text, toolMaps, body.ToolChoice)
				}
			}
			calls, _ = validateCalls("native-recovery", calls)
			if parsed && len(calls) > 0 {
				scope := fmt.Sprintf("%d:%v:native-recovery", len(body.Messages), completedCallIDs(ledger))
				for i := range calls {
					calls[i].ID = scopedCallID(calls[i].Name, string(calls[i].Arguments), i, scope)
				}
				calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
				_ = writeToolResponse(w, id, model, body.Stream, body.shouldSendStreamUsage(), calls, routeRes)
				return
			}
		}
	}
	if isContentPolicyBlock(res.Text) {
		log.Printf("[content-policy] M365 blocked the request, returning 503")
		writeOpenAIError(w, http.StatusServiceUnavailable, "upstream_content_blocked", "M365 content policy blocked this request; try again or switch account")
		return
	}
	if isImageLimitNotice(res.Text) {
		if s.accountPool != nil {
			s.accountPool.MarkImageLimited(acc.ID)
		}
	}
	if len(toolMaps) > 0 && !completionEvidenceAllows(res.Text, ledger) {
		res.Text = "I cannot confirm completion because no matching tool results were returned. No external action has been verified."
	}
	res.Text = sanitizePublicAssistantTextForModel(res.Text, body.Model)
	res.Reasoning = sanitizePublicReasoningText(res.Reasoning)
	log.Printf("[debug] res.Text bytes=%d content=%q", len(res.Text), res.Text)
	created := time.Now().Unix()

	if body.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeOpenAIError(w, http.StatusInternalServerError, "server_error", "stream unsupported")
			return
		}
		sw3 := newSSEWriter(w, flusher)
		ticker3 := time.NewTicker(15 * time.Second)
		defer ticker3.Stop()
		keepaliveDone3 := make(chan struct{})
		defer close(keepaliveDone3)
		go func() {
			for {
				select {
				case <-keepaliveDone3:
					return
				case <-r.Context().Done():
					return
				case <-ticker3.C:
					_ = sw3.raw(": keepalive\n\n")
				}
			}
		}()
		// one-shot "stream" — emit full content then done
		chunk := map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{"role": "assistant", "content": res.Text},
			}},
		}
		b, _ := json.Marshal(chunk)
		_ = sw3.data(string(b))
		pt := EstimateTokens(prompt)
		ct := EstimateTokens(res.Text)
		usageChunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": pt, "completion_tokens": ct, "total_tokens": pt + ct}}
		if res.Throttling != nil {
			usageChunk["x_m365_throttling"] = res.Throttling
		}
		if len(res.Scores) > 0 {
			usageChunk["x_m365_scores"] = res.Scores
		}
		_ = sw3.data(mustJSON(usageChunk))
		_ = sw3.data("[DONE]")
		if res.Timestamps.RequestSent != "" {
			_ = sw3.raw(": m365-metrics " + mustJSON(res.Timestamps) + "\n\n")
		}
		return
	}

	if responseFormat != nil && (responseFormat.Type == "json_object" || responseFormat.Type == "json_schema") {
		res.Text = normalizeJSONText(res.Text)
	}
	content := any(res.Text)
	if len(res.Images) > 0 {
		parts := []any{map[string]any{"type": "text", "text": res.Text}}
		for _, u := range res.Images {
			du, _ := downloadImageAsDataURIWithToken(u, acc.AccessToken)
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": du}})
		}
		content = parts
	}
	assistant := map[string]any{
		"role":    "assistant",
		"content": content,
	}
	if res.Reasoning != "" {
		assistant["reasoning_content"] = res.Reasoning
	}
	// 上游 ChatHub 不返回 token 计数，按请求/回复文本本地估算填充
	// OpenAI 要求的 usage 字段。
	pt := EstimateTokens(prompt)
	ct := EstimateTokens(res.Text)
	if res.Timestamps.RequestSent != "" {
		w.Header().Set("X-M365-Metrics", mustJSON(res.Timestamps))
	}
	if res.Throttling != nil {
		if b, err := json.Marshal(res.Throttling); err == nil {
			w.Header().Set("X-M365-Throttling", string(b))
		}
	}
	if len(res.Scores) > 0 {
		if b, err := json.Marshal(res.Scores); err == nil {
			w.Header().Set("X-M365-Scores", string(b))
		}
	}
	jsonOut(w, map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       assistant,
			"finish_reason": "stop",
		}},
		"m365": compatM365Metadata(res),
		"usage": map[string]any{
			"prompt_tokens":     pt,
			"completion_tokens": ct,
			"total_tokens":      pt + ct,
		},
	})
}

func (s *Server) writePublicIdentityChatResponse(w http.ResponseWriter, r *http.Request, body *oaiReq, prompt, answer string, startedAt time.Time) {
	model := firstNonEmpty(body.Model, defaultPublicModelName)
	id := "chatcmpl-" + uuid.NewString()
	created := time.Now().Unix()
	inputTokens := EstimateTokens(prompt)
	outputTokens := EstimateTokens(answer)
	usage := map[string]any{"prompt_tokens": inputTokens, "completion_tokens": outputTokens, "total_tokens": inputTokens + outputTokens}
	if s.usage != nil {
		s.usage.record(UsageRecord{
			Time:         time.Now(),
			APIKeyPrefix: extractAPIKey(r),
			Model:        model,
			Endpoint:     "/v1/chat/completions",
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			DurationMs:   time.Since(startedAt).Milliseconds(),
			Status:       http.StatusOK,
		})
	}
	if !body.Stream {
		jsonOut(w, map[string]any{
			"id":      id,
			"object":  "chat.completion",
			"created": created,
			"model":   model,
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": answer},
				"finish_reason": "stop",
			}},
			"usage": usage,
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOpenAIError(w, http.StatusInternalServerError, "server_error", "stream unsupported")
		return
	}
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"delta":         map[string]any{"role": "assistant", "content": answer},
			"finish_reason": nil,
		}},
	}
	_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(chunk)+"\n\n")
	finish := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": usage}
	_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(finish)+"\n\n")
	_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
}

const defaultPublicModelName = "m365-copilot"

func streamFinalResultFallback(streamedContentLen int, final string) string {
	if streamedContentLen == 0 && strings.TrimSpace(final) != "" {
		return final
	}
	return ""
}

const sessionHeaderName = "X-M365-Session-Id"

// bindConversation 在请求完成后登记会话解析器索引与缓存统计，流式与非流式
// 路径共用。会话为内容键，云端的对话由 auto_cleanup 按 2h 闲置窗口回收，
// 这里不再做"用完即删"，否则复用永远不可能命中。
func (s *Server) bindConversation(acc auth.AccountToken, body *oaiReq, r *http.Request, res chathub.Result, prompt string, startedAt time.Time) {
	if res.ConversationID == "" {
		return
	}
	historyBody := *body
	historyBody.Messages = append(cloneMessages(body.Messages), oaiMsg{
		Role:             "assistant",
		Content:          res.Text,
		ReasoningContent: res.Reasoning,
	})
	s.sessionResolver.Bind(res.SessionID, res.ConversationID, acc.ID, &historyBody, "", r)
	if s.contextAffinity != nil {
		s.contextAffinity.BindConversation(acc.ID, res.ConversationID, res.SessionID, &historyBody, r)
	}
	s.conversationManager.Record(res.ConversationID, acc.ID, prompt)
	if s.conversationManager.ShouldCleanup() {
		if cleaned := s.conversationManager.Cleanup(); len(cleaned) > 0 {
			log.Printf("[conversation-manager] auto-cleaned %d conversations", len(cleaned))
		}
	}

	apiKey := extractAPIKey(r)
	historyTokens := int64(0)
	upper := len(body.Messages) - 1
	if upper < 0 {
		upper = 0
	}
	for _, msg := range body.Messages[:upper] {
		historyTokens += EstimateTokens(contentToString(msg.Content))
	}
	newTokens := EstimateTokens(prompt)
	sessions := s.sessionResolver.ListSessions()
	cacheStats.RecordRequest(apiKey, historyTokens > 0, newTokens, historyTokens, len(sessions))
	s.usage.record(UsageRecord{
		Time:         time.Now(),
		APIKeyPrefix: apiKey,
		AccountEmail: acc.Email,
		Model:        firstNonEmpty(body.Model, "m365-copilot"),
		Endpoint:     "/v1/chat/completions",
		Stream:       body.Stream,
		InputTokens:  newTokens,
		OutputTokens: EstimateTokens(res.Text),
		CacheTokens:  historyTokens,
		DurationMs:   time.Since(startedAt).Milliseconds(),
		Status:       200,
	})
}

func extractAPIKey(r *http.Request) string {
	key := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if key != "" {
		return key
	}
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		key = strings.TrimSpace(auth[7:])
	}
	if len(key) > 8 {
		return key[:8] + "..."
	}
	return key
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

type chathubLocale struct {
	Locale         string
	Market         string
	TimeZone       string
	TimeZoneOffset int
	DeviceOS       string
}

func parseLocaleFromHeaders(r *http.Request) chathubLocale {
	loc := chathubLocale{}
	if override := strings.TrimSpace(r.Header.Get("X-M365-Locale")); override != "" {
		loc.Locale = strings.ToLower(override)
	} else {
		loc.Locale = strings.TrimSpace(r.Header.Get("Accept-Language"))
		if loc.Locale == "" {
			loc.Locale = "en-us"
		} else {
			if idx := strings.Index(loc.Locale, ";"); idx >= 0 {
				loc.Locale = strings.TrimSpace(loc.Locale[:idx])
			}
			if idx := strings.Index(loc.Locale, ","); idx >= 0 {
				loc.Locale = strings.TrimSpace(loc.Locale[:idx])
			}
			loc.Locale = strings.ToLower(loc.Locale)
		}
	}
	loc.Market = strings.TrimSpace(r.Header.Get("X-M365-Market"))
	if loc.Market == "" {
		loc.Market = "en-us"
	} else {
		loc.Market = strings.ToLower(strings.TrimSpace(loc.Market))
	}
	tz := strings.TrimSpace(r.Header.Get("X-M365-TimeZone"))
	if tz != "" {
		loc.TimeZone = tz
		if l, err := time.LoadLocation(tz); err == nil {
			_, offset := time.Now().In(l).Zone()
			loc.TimeZoneOffset = offset / 3600
		}
	} else {
		loc.TimeZone = "UTC"
		loc.TimeZoneOffset = 0
	}
	loc.DeviceOS = strings.TrimSpace(r.Header.Get("X-M365-DeviceOS"))
	if loc.DeviceOS == "" {
		loc.DeviceOS = "Windows"
	}
	return loc
}

func extractOIDTID(accessToken string) (oid, tid string) {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return "", ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", ""
	}
	if v, ok := m["oid"].(string); ok {
		oid = v
	}
	if v, ok := m["tid"].(string); ok {
		tid = v
	}
	return oid, tid
}
