package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"m365-copilot2api/internal/auth"
	"m365-copilot2api/internal/chathub"
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

// rateLimitCooldown is how long a rate-limited account stays out of rotation.
const rateLimitCooldown = 2 * time.Minute

// maxAccountProbe bounds the round-robin walk when skipping unhealthy accounts.
const maxAccountProbe = 16

type Server struct {
	mu                  sync.Mutex
	tokens              *auth.Store
	accountPool         *accountHealth
	pkce                map[string]pendingPKCE
	chat                *chathub.Client
	sessions            *sessionStore
	userSessions        *userSessionStore
	sessionResolver     *sessionResolver
	conversationManager *conversationManager
	adminPassword       string
	adminSessions       map[string]time.Time
	mustChangePassword  bool
	loginAttempts       map[string]loginAttempt
	apiKeys             *apiKeyStore
	debug               *debugStore
	settings            *settingsStore
	responseMu          sync.Mutex
	responseMessages    map[string]map[string]respHistory
	usage               *usageLog
}

const maxResponsesPerTenant = 256

type respHistory struct {
	At       time.Time
	Messages []oaiMsg
}

func New() (*Server, error) {
	store, err := auth.OpenStore("")
	if err != nil {
		return nil, err
	}
	password, mustChange := loadAdminPassword()
	sessionTTL := 30 * time.Minute
	if v := os.Getenv("M365_USER_SESSION_TTL_MINUTES"); v != "" {
		if d, err := time.ParseDuration(v + "m"); err == nil {
			sessionTTL = d
		}
	}
	return &Server{
		tokens:      store,
		accountPool: newAccountHealth(),
		pkce:        map[string]pendingPKCE{},
		chat: func() *chathub.Client {
			c := chathub.NewClient()
			c.Trace = func(meta map[string]any) { fmt.Printf("[multimodal-trace] %s\\n", mustJSON(meta)) }
			return c
		}(),
		sessions:            openSessionStore(),
		userSessions:        openUserSessionStore(sessionTTL),
		sessionResolver:     openSessionResolver(),
		conversationManager: openConversationManager(),
		adminPassword:       password,
		adminSessions:       map[string]time.Time{},
		mustChangePassword:  mustChange,
		loginAttempts:       map[string]loginAttempt{},
		apiKeys:             openAPIKeys(),
		debug:               openDebugStore(),
		settings:            openSettingsStore(),
		responseMessages:    map[string]map[string]respHistory{},
		usage:               openUsageLog(),
	}, nil
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

func (s *Server) Routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/admin/login", s.adminLogin)
	m.HandleFunc("/api/admin/logout", s.adminLogout)
	m.HandleFunc("/api/admin/session", s.adminSession)
	m.HandleFunc("/api/admin/change-password", s.adminChangePassword)
	m.HandleFunc("/api/admin/keys", s.adminKeys)
	m.HandleFunc("/api/admin/models", s.adminModels)
	m.HandleFunc("/api/admin/models/test", s.adminModelTest)
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
	m.HandleFunc("/api/accounts/delete", s.deleteAccount)
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
	m.HandleFunc("/api/m365/conversations/delete", s.handleM365Delete)
	m.HandleFunc("/api/m365/conversations/cleanup", s.handleM365Cleanup)
	m.HandleFunc("/api/stats", s.handleCacheStats)
	m.HandleFunc("/api/stats/reset", s.handleCacheStatsReset)
	m.HandleFunc("/api/usage", s.adminUsage)
	m.HandleFunc("/api/usage/logs", s.adminUsageLogs)
	m.HandleFunc("/v1/models", s.openaiModels)
	m.HandleFunc("/v1/chat/completions", s.openaiChat)
	m.HandleFunc("/v1/responses", s.responses)
	m.HandleFunc("/v1/messages", s.anthropicMessages)
	m.HandleFunc("/v1/images/generations", s.imageGenerations)
	m.HandleFunc("/", s.rootPage)
	return recoverPanics(requestID(httpTrace(securityHeaders(s.adminMiddleware(s.debugMiddleware(m))))))
}

func (s *Server) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/login" || r.URL.Path == "/api/admin/session" || r.URL.Path == "/api/admin/change-password" || r.URL.Path == "/api/admin/logout" || r.URL.Path == "/" || r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			if !s.validAPIKey(r) {
				http.Error(w, `{"error":{"message":"valid API key required","type":"auth_error"}}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if s.adminPassword == "" {
			http.Error(w, `{"error":{"message":"administrator password is not configured","type":"configuration_error"}}`, http.StatusServiceUnavailable)
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
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return net.ParseIP(host).IsLoopback() && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
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
		writeOpenAIError(w, http.StatusTooManyRequests, "rate_limit_error", "too many failed login attempts; try again later")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	decodeErr := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	s.mu.Lock()
	password := s.adminPassword
	mustChange := s.mustChangePassword
	s.mu.Unlock()
	if decodeErr != nil || body.Password == "" || subtle.ConstantTimeCompare([]byte(body.Password), []byte(password)) != 1 {
		s.recordLoginFailure(ip, now)
		writeOpenAIError(w, http.StatusUnauthorized, "auth_error", "invalid administrator password")
		return
	}
	s.clearLoginFailures(ip)
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
			http.Error(w, "bad json", 400)
			return
		}
		if strings.TrimSpace(b.Name) == "" {
			b.Name = "API key"
		}
		rec, raw, e := s.apiKeys.create(b.Name)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		jsonOut(w, map[string]any{"key": raw, "record": rec})
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		deleted, e := s.apiKeys.delete(id)
		if e != nil {
			http.Error(w, e.Error(), http.StatusInternalServerError)
			return
		}
		if !deleted {
			http.Error(w, "key not found", 404)
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
			http.Error(w, "bad json", 400)
			return
		}
		updated, e := s.apiKeys.update(b.ID, b.Name, b.Revoked)
		if e != nil {
			http.Error(w, e.Error(), http.StatusInternalServerError)
			return
		}
		if !updated {
			http.Error(w, "key not found", 404)
			return
		}
		jsonOut(w, map[string]string{"status": "updated"})
	default:
		http.Error(w, "method not allowed", 405)
	}
}
func (s *Server) validAPIKey(r *http.Request) bool {
	raw := strings.TrimSpace(r.Header.Get("X-API-Key"))
	if raw == "" {
		v := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(v), "bearer ") {
			raw = strings.TrimSpace(v[7:])
		}
	}
	return raw != "" && s.apiKeys.valid(raw)
}

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	list := s.tokens.List()
	jsonOut(w, map[string]any{
		"status":       "ok",
		"auth":         []string{"pkce"},
		"chat":         "chathub",
		"clientId":     auth.ClientID(),
		"scope":        auth.Scope(),
		"tokenCache":   s.tokens.Path(),
		"accountCount": len(list),
	})
}

func (s *Server) accounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	list := s.tokens.List()
	type view struct {
		ID          string    `json:"id"`
		Email       string    `json:"email"`
		DisplayName string    `json:"displayName,omitempty"`
		Status      string    `json:"status"`
		OID         string    `json:"oid,omitempty"`
		TID         string    `json:"tid,omitempty"`
		ExpiresAt   time.Time `json:"expiresAt,omitempty"`
		UpdatedAt   time.Time `json:"updatedAt,omitempty"`
	}
	out := make([]view, 0, len(list))
	for _, a := range list {
		out = append(out, view{
			ID: a.ID, Email: a.Email, DisplayName: a.DisplayName,
			Status: a.Status, OID: a.OID, TID: a.TID,
			ExpiresAt: a.ExpiresAt, UpdatedAt: a.UpdatedAt,
		})
	}
	jsonOut(w, map[string]any{"accounts": out})
}

func (s *Server) refreshAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		http.Error(w, "bad json", http.StatusBadRequest)
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

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if err := s.tokens.Delete(body.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOut(w, map[string]string{"status": "deleted"})
}

func (s *Server) startPKCE(w http.ResponseWriter, _ *http.Request) {
	v, err := auth.Verifier()
	if err != nil {
		http.Error(w, "pkce failure", http.StatusInternalServerError)
		return
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		http.Error(w, "state failure", http.StatusInternalServerError)
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
		http.Error(w, "missing state", http.StatusBadRequest)
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
		http.Error(w, "missing state or authorization result", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	p, ok := s.pkce[state]
	if !ok || time.Since(p.Created) > 10*time.Minute {
		if ok {
			delete(s.pkce, state)
		}
		s.mu.Unlock()
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}
	if p.Status != "pending" {
		s.mu.Unlock()
		http.Error(w, "authorization result already consumed", http.StatusConflict)
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
		http.Error(w, "Microsoft authorization failed: "+oauthError, http.StatusBadRequest)
		return
	}
	tok, err := auth.ExchangeCode(code, p.Verifier, p.RedirectURI)
	if err != nil {
		logOAuthError("code_exchange", err)
		s.mu.Lock()
		p.Status = "error"
		p.Error = err.Error()
		s.pkce[state] = p
		s.mu.Unlock()
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	acc, err := s.tokens.Upsert(tok)
	if err != nil {
		s.mu.Lock()
		p.Status = "error"
		p.Error = err.Error()
		s.pkce[state] = p
		s.mu.Unlock()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	p.Status = "authenticated"
	p.Account = map[string]any{"id": acc.ID, "email": acc.Email, "displayName": acc.DisplayName, "status": acc.Status, "oid": acc.OID, "tid": acc.TID}
	s.pkce[state] = p
	s.mu.Unlock()
	// Browser loopback callbacks should finish in a friendly page instead of
	// displaying a raw JSON response. Keep JSON for the manual/API flow.
	if strings.HasPrefix(p.RedirectURI, "http://127.0.0.1:") || strings.HasPrefix(p.RedirectURI, "http://localhost:") {
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
		acc, ok := s.tokens.Next()
		if !ok {
			return auth.AccountToken{}, fmt.Errorf("no accounts; login first")
		}
		accountID = acc.ID
		// Round-robin may land on a cooling-down or auth-failed account;
		// walk the pool for the next healthy one without infinite loops.
		for i := 0; !s.accountPool.Available(accountID) && i < maxAccountProbe; i++ {
			acc, ok = s.tokens.Next()
			if !ok {
				break
			}
			accountID = acc.ID
		}
		if !s.accountPool.Available(accountID) {
			return auth.AccountToken{}, fmt.Errorf("all accounts are cooling down or failing auth; try again later")
		}
	}
	acc, err := s.tokens.EnsureValid(accountID)
	return acc, err
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
		if !s.accountPool.Available(acc.ID) {
			continue
		}
		return s.tokens.EnsureValid(acc.ID)
	}
	return auth.AccountToken{}, fmt.Errorf("no healthy account available for failover")
}

type chatBody struct {
	AccountID      string               `json:"accountId"`
	Message        string               `json:"message"`
	Prompt         string               `json:"prompt"`
	Tone           string               `json:"tone"`
	ConversationID string               `json:"conversationId"`
	SessionID      string               `json:"sessionId"`
	SessionKey     string               `json:"sessionKey"`
	Attachments    []chathub.Attachment `json:"attachments,omitempty"`
	Tools          []chathub.Tool       `json:"tools,omitempty"`
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
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body chatBody
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(firstNonEmpty(body.Message, body.Prompt))
	if text == "" && len(body.Attachments) == 0 {
		http.Error(w, "message or attachment required", http.StatusBadRequest)
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
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if acc.OID == "" || acc.TID == "" {
		// try extract from access token claims on the fly
		if claimsOID, claimsTID := extractOIDTID(acc.AccessToken); claimsOID != "" {
			acc.OID = claimsOID
			acc.TID = claimsTID
		}
	}
	if acc.OID == "" || acc.TID == "" {
		http.Error(w, "account missing oid/tid — re-login with PKCE browser client", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
	defer cancel()
	res, err := s.chat.Chat(ctx, chathub.Account{
		AccessToken: acc.AccessToken,
		OID:         acc.OID,
		TID:         acc.TID,
	}, chathub.Request{
		Text:           text,
		Tone:           body.Tone,
		ConversationID: body.ConversationID,
		SessionID:      body.SessionID,
		Attachments:    body.Attachments,
	})
	if err != nil {
		// Failover: a rate-limited or auth-failed account must not take down the
		// request when the pool has other healthy accounts. Only auto-selected
		// requests fail over; an explicitly chosen account is respected, and a
		// conversation-bound chat stays on its account.
		if body.AccountID == "" && body.ConversationID == "" && (IsRateLimited(err) || IsAuthFailure(err)) {
			next, nerr := s.nextHealthyAccount(acc.ID)
			if nerr == nil {
				ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
				defer cancel2()
				res2, err2 := s.chat.Chat(ctx2, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, chathub.Request{
					Text:           text,
					Tone:           body.Tone,
					ConversationID: body.ConversationID,
					SessionID:      body.SessionID,
					Attachments:    body.Attachments,
				})
				if err2 == nil {
					s.accountPool.MarkFailure(acc.ID, err, rateLimitCooldown)
					s.accountPool.MarkSuccess(next.ID)
					jsonOut(w, map[string]any{
						"status": "ok", "text": res2.Text, "conversationId": res2.ConversationID,
						"sessionId": res2.SessionID, "requestId": res2.RequestID,
						"throttling": res2.Throttling, "result": res2.RawResult, "events": res2.Events,
						"images": res2.Images, "account": map[string]any{"id": next.ID, "email": next.Email},
					})
					return
				}
				err = err2
			}
		}
		s.accountPool.MarkFailure(acc.ID, err, rateLimitCooldown)
		writeUpstreamError(w, err)
		return
	}
	s.accountPool.MarkSuccess(acc.ID)
	if body.SessionKey != "" {
		s.sessions.upsert(conversation{ID: body.SessionKey, AccountID: acc.ID, ConversationID: res.ConversationID, SessionID: res.SessionID, Title: text})
	}
	jsonOut(w, map[string]any{
		"status":         "ok",
		"text":           res.Text,
		"conversationId": res.ConversationID,
		"sessionId":      res.SessionID,
		"requestId":      res.RequestID,
		"throttling":     res.Throttling,
		"result":         res.RawResult,
		"events":         res.Events,
		"images":         res.Images,
		"account":        map[string]any{"id": acc.ID, "email": acc.Email},
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

func (s *Server) adminModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	jsonOut(w, map[string]any{"object": "list", "data": modelCatalog()})
}

// adminModelTest 由控制台模型测试调用，通过管理员会话鉴权，不依赖明文 API Key
// （密钥加固后 list 不再返回 raw，前端无法再自行携带 key 调用 /v1 端点）。
func (s *Server) adminModelTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var b struct {
		Model string `json:"model"`
	}
	if json.NewDecoder(r.Body).Decode(&b) != nil || strings.TrimSpace(b.Model) == "" {
		http.Error(w, "bad json: model required", http.StatusBadRequest)
		return
	}
	acc, err := s.resolveAccount("")
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "account_error", err.Error())
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
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
	defer cancel()
	res, err := s.chat.Chat(ctx, chathub.Account{AccessToken: acc.AccessToken, OID: acc.OID, TID: acc.TID}, chathub.Request{
		Text: `Say "OK" in one word.`,
		Tone: tone,
	})
	ms := time.Since(start).Milliseconds()
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "m365_error", upstreamError(err))
		return
	}
	jsonOut(w, map[string]any{"ok": true, "model": b.Model, "reply": res.Text, "latency_ms": ms})
}

func (s *Server) openaiModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	Role       string           `json:"role"`
	Content    any              `json:"content"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []map[string]any `json:"tool_calls,omitempty"`
}

type oaiReq struct {
	Model          string          `json:"model"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Messages       []oaiMsg        `json:"messages"`
	Stream         bool            `json:"stream"`
	// optional account routing
	User           string `json:"user"`
	AccountID      string `json:"accountId"`
	ConversationID string `json:"conversation_id"`
	SessionID      string `json:"session_id"`
	SessionKey     string `json:"session_key"`
	// CamelCase aliases mirroring the response metadata fields; clients echo
	// m365.conversationId / m365.sessionId back verbatim.
	ConversationIDC string               `json:"conversationId,omitempty"`
	SessionIDC      string               `json:"sessionId,omitempty"`
	Attachments     []chathub.Attachment `json:"attachments,omitempty"`
	Tools           []chathub.Tool       `json:"tools,omitempty"`
	// Legacy OpenAI-compatible clients still send functions/function_call.
	Functions       []json.RawMessage `json:"functions,omitempty"`
	ToolChoice      any               `json:"tool_choice,omitempty"`
	FunctionCall    any               `json:"function_call,omitempty"`
	Reasoning       *reasoningConfig  `json:"reasoning,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"`
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// writeStreamFinish emits a terminal OpenAI-compatible chunk with a non-null
// finish_reason before the stream ends, so strict clients do not treat an
// otherwise successful response as incomplete.
func writeStreamFinish(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, id, model string) {
	finishChunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model, "choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}}
	_ = sseRaw(ctx, w, flusher, "data: "+mustJSON(finishChunk)+"\n\n")
}

func contentToString(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, part := range v {
			if m, ok := part.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" || t == "input_text" || t == "output_text" {
					if s, _ := m["text"].(string); s != "" {
						b.WriteString(s)
					}
				}
			}
		}
		return b.String()
	default:
		return fmt.Sprint(v)
	}
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
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	const maxChatRequestBody = 10 << 20
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChatRequestBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var body oaiReq
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	responseFormat := body.ResponseFormat
	effort := body.ReasoningEffort
	if body.Reasoning != nil && strings.TrimSpace(body.Reasoning.Effort) != "" {
		effort = body.Reasoning.Effort
	}
	tone, toneErr := reasoningTone(body.Model, effort)
	if toneErr != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", toneErr.Error())
		return
	}
	normalizeLegacyTools(&body)
	ensureWebSearchEnabled(&body)
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
	// Preserve role boundaries when adapting OpenAI messages to ChatHub's
	// single message.text field. This keeps system/developer instructions,
	// history, and the current user turn distinguishable.
	var prompt string
	prompt, body.Attachments = flattenPromptMessages(body.Messages, body.Attachments)
	log.Printf("[req-trace] id=%s stage=prompt_flattened prompt_len=%d attachments=%d", requestID, len(prompt), len(body.Attachments))
	fmt.Printf("[multimodal-entry] messages=%d attachments=%d prompt_len=%d\n", len(body.Messages), len(body.Attachments), len(prompt))
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		http.Error(w, "messages required", http.StatusBadRequest)
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
		if us, ok := s.userSessions.Get(body.User); ok {
			body.AccountID = firstNonEmpty(body.AccountID, us.AccountID)
			body.ConversationID = us.ConversationID
			body.SessionID = us.SessionID
			log.Printf("[user-session] hit user=%s conversation=%s session=%s", body.User, us.ConversationID, us.SessionID)
		}
	}
	// 内容键会话复用：命中后云端对话已存全量历史，只需把客户端新增的
	// 消息拼成增量 prompt 发送（对齐 DeepSeek 上下文缓存语义）。
	answerPrompt := prompt
	if body.ConversationID == "" && len(body.Messages) > 0 {
		resolved := s.sessionResolver.Resolve(r, &body)
		if !resolved.IsNew {
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
	accountID := body.AccountID
	acc, err := s.resolveAccount(accountID)
	if err != nil {
		log.Printf("[account-route] resolve failed requested=%q err=%v", accountID, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("[account-route] selected id=%q email=%q token_present=%t oid_present=%t tid_present=%t", acc.ID, acc.Email, acc.AccessToken != "", acc.OID != "", acc.TID != "")
	if acc.OID == "" || acc.TID == "" {
		if o, t := extractOIDTID(acc.AccessToken); o != "" {
			acc.OID, acc.TID = o, t
		}
	}
	if acc.OID == "" || acc.TID == "" {
		http.Error(w, "account missing oid/tid", http.StatusBadRequest)
		return
	}

	// Normalize tools once. Selection is always made by the upstream model;
	// the gateway only validates its structured decision and converts protocols.
	toolMaps := make([]map[string]any, 0, len(body.Tools))
	for _, tool := range body.Tools {
		var f map[string]any
		_ = json.Unmarshal(tool.Function, &f)
		toolMaps = append(toolMaps, map[string]any{"type": tool.Type, "function": f})
	}
	routeMaps := routeableTools(toolMaps)
	if body.ToolChoice == nil && len(toolMaps) > 0 {
		body.ToolChoice = "auto"
	}
	planningMode := s.settings.get().ToolPlanningMode

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
	defer cancel()
	account := chathub.Account{AccessToken: acc.AccessToken, OID: acc.OID, TID: acc.TID}
	// The stream is opened by the actual response path below. Do not emit a
	// tool preamble here: a request may contain tools in its schema while still
	// being an ordinary text question.
	// Streaming requests must not wait for the synchronous tool router. This
	// path forwards ordinary upstream text deltas immediately; tool routing for
	// non-streaming requests remains below until the event-level tool protocol
	// is available end-to-end.
	if planningMode == "router" && body.Stream && len(routeMaps) > 0 && fmt.Sprint(body.ToolChoice) != "none" {
		// Preserve the existing validated tool router for streaming tool turns.
		// Only fall through to text streaming when the router explicitly selects
		// no tool; this prevents a natural-language preamble from becoming a
		// completed assistant turn with the actual call lost.
		routePrompt := modelToolRouterPrompt(prompt+"\n"+ledger.RouterContext(), routeMaps, body.ToolChoice)
		log.Printf("[req-trace] id=%s stage=router_start prompt_len=%d", requestID, len(routePrompt))
		routeRes, routeErr := s.chat.Chat(ctx, account, chathub.Request{Text: routePrompt, Tone: tone, Attachments: body.Attachments})
		log.Printf("[req-trace] id=%s stage=router_return elapsed_ms=%d err=%t", requestID, time.Since(startedAt).Milliseconds(), routeErr != nil)
		// Router turns run in a throwaway cloud conversation that is never
		// reused by the answer turn; delete it so the conversation list does
		// not accumulate one entry per routed request.
		if routeErr == nil && routeRes.ConversationID != "" {
			s.dropTransientConversation(routeRes.ConversationID)
		}
		if routeErr != nil {
			http.Error(w, "tool router: "+routeErr.Error(), http.StatusBadGateway)
			return
		}
		calls, parsed := parseModelToolDecision(routeRes.Text, routeMaps, body.ToolChoice)
		calls = filterCompletedCalls(calls, ledger)
		if !parsed {
			repairRes, repairErr := s.chat.Chat(ctx, account, chathub.Request{Text: `Repair this tool routing output into JSON only with shape {"calls":[{"name":"function_name","arguments":{}}]}. Use {"calls":[]} if no tool is needed. OUTPUT:\n` + compactToolResult(routeRes.Text, 6000), Tone: tone, Attachments: body.Attachments})
			if repairErr == nil && repairRes.ConversationID != "" {
				s.dropTransientConversation(repairRes.ConversationID)
			}
			if repairErr == nil {
				calls, parsed = parseModelToolDecision(repairRes.Text, routeMaps, body.ToolChoice)
				calls = filterCompletedCalls(calls, ledger)
			}
		}
		if parsed && len(calls) > 0 {
			scope := fmt.Sprintf("%d:%v:stream", len(body.Messages), completedCallIDs(ledger))
			for i := range calls {
				calls[i].ID = scopedCallID(calls[i].Name, string(calls[i].Arguments), i, scope)
			}
			calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
			_ = writeToolResponse(w, "chatcmpl-"+uuid.NewString(), firstNonEmpty(body.Model, "m365-copilot"), true, calls, routeRes)
			return
		}
	}
	if body.Stream {
		answerPrompt = answerPrompt + "\n" + ledger.RouterContext() + "\nFINAL ANSWER RULE: Answer the user directly. If a tool is explicitly required, emit its structured call; otherwise return ordinary text."
		log.Printf("[req-trace] id=%s stage=answer_start prompt_len=%d", requestID, len(answerPrompt))
		answerReq := chathub.Request{Text: answerPrompt, Tone: tone, ConversationID: body.ConversationID, SessionID: body.SessionID, Attachments: body.Attachments, Tools: body.Tools, ToolChoice: body.ToolChoice}
		id := "chatcmpl-" + uuid.NewString()
		model := firstNonEmpty(body.Model, "m365-copilot")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "stream unsupported", http.StatusInternalServerError)
			return
		}
		if err := sseRaw(r.Context(), w, flusher, ": connected\n\n"); err != nil {
			return
		}
		var text strings.Builder
		var pending strings.Builder
		var streamedTools []detectedToolCall
		first := true
		emitText := func(part string) error {
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
			rc := http.NewResponseController(w)
			_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, err := fmt.Fprintf(w, "data: %s\n\n", mustJSON(chunk)); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		}
		res, err := s.chat.ChatWithEvents(ctx, account, answerReq, func(ev chathub.StreamEvent) error {
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
			// If the text contains a bash block or a JSON command, don't emit it as text
			// It will be caught by fencedToolCalls after the stream completes
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
		if err != nil {
			log.Printf("[req-trace] id=%s stage=stream_error err=%v", requestID, err)
			s.accountPool.MarkFailure(acc.ID, err, rateLimitCooldown)
			msg := upstreamError(err)
			if IsRateLimited(err) {
				msg = "upstream is rate limiting; try again shortly"
			}
			_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(map[string]any{"error": map[string]any{"message": msg, "code": "rate_limit"}})+"\n\n")
			_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
			return
		}
		s.accountPool.MarkSuccess(acc.ID)
		// Some ChatHub updates contain no text event and place the completed
		// answer only in the final Result. Recover it before deciding that the
		// response is empty; this also preserves fenced-tool parsing.
		if text.Len() == 0 && strings.TrimSpace(res.Text) != "" {
			text.WriteString(res.Text)
			pending.WriteString(res.Text)
		}
		calls := streamedTools
		if len(calls) == 0 {
			calls = fencedToolCalls(text.String(), toolMaps, body.ToolChoice)
		}
		if len(calls) > 0 {
			calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
			_ = writeToolResponse(w, id, model, true, calls, chathub.Result{Text: text.String()})
			if body.User != "" && res.ConversationID != "" {
				s.userSessions.Put(body.User, res.ConversationID, res.SessionID, acc.ID)
			}
			s.bindConversation(acc, &body, r, res, answerPrompt, startedAt)
			return
		}
		if err := emitText(pending.String()); err != nil {
			log.Printf("[req-trace] id=%s stage=stream_write err=%v", requestID, err)
			return
		}
		writeStreamFinish(r.Context(), w, flusher, id, model)
		_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
		if body.User != "" && res.ConversationID != "" {
			s.userSessions.Put(body.User, res.ConversationID, res.SessionID, acc.ID)
		}
		s.bindConversation(acc, &body, r, res, answerPrompt, startedAt)
		return
	}
	// Ask the upstream model to select and validate the next tool. The gateway
	// remains tool-agnostic; it only validates and serializes the decision.
	if planningMode == "router" && len(routeMaps) > 0 && fmt.Sprint(body.ToolChoice) != "none" {
		routePrompt := modelToolRouterPrompt(prompt+"\n"+ledger.RouterContext(), routeMaps, body.ToolChoice)
		routeRes, routeErr := s.chat.Chat(ctx, account, chathub.Request{Text: routePrompt, Tone: tone, Attachments: body.Attachments})
		if routeErr != nil {
			http.Error(w, "tool router: "+routeErr.Error(), http.StatusBadGateway)
			return
		}
		calls, parsed := parseModelToolDecision(routeRes.Text, routeMaps, body.ToolChoice)
		if !parsed {
			repairRes, repairErr := s.chat.Chat(ctx, account, chathub.Request{Text: `Repair this tool routing output into JSON only with shape {"calls":[{"name":"function_name","arguments":{}}]}. Do not invent calls; use {"calls":[]} if unrecoverable. OUTPUT:
` + compactToolResult(routeRes.Text, 6000), Tone: tone, Attachments: body.Attachments})
			if repairErr == nil {
				calls, parsed = parseModelToolDecision(repairRes.Text, routeMaps, body.ToolChoice)
			}
			if !parsed {
				http.Error(w, "model returned an invalid tool routing decision", http.StatusBadGateway)
				return
			}
		}
		if len(calls) > 0 {
			scope := fmt.Sprintf("%d:%v", len(body.Messages), completedCallIDs(ledger))
			for i := range calls {
				calls[i].ID = scopedCallID(calls[i].Name, string(calls[i].Arguments), i, scope)
			}
			calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
			_ = writeToolResponse(w, "chatcmpl-"+uuid.NewString(), firstNonEmpty(body.Model, "m365-copilot"), body.Stream, calls, routeRes)
			return
		}
		if fmt.Sprint(body.ToolChoice) == "required" {
			defs, _ := json.Marshal(routeMaps)
			retryText := `Select at least one required next tool call from FUNCTION_DEFINITIONS. Validate every argument against its schema. Return JSON only as {"calls":[{"name":"function_name","arguments":{}}]}.
APPLICATION_REQUEST_AND_EVIDENCE:
` + prompt + "\n" + ledger.RouterContext() + "\nFUNCTION_DEFINITIONS:\n" + string(defs)
			retryRes, retryErr := s.chat.Chat(ctx, account, chathub.Request{Text: retryText, Tone: tone, Attachments: body.Attachments})
			if retryErr == nil {
				calls, parsed = parseModelToolDecision(retryRes.Text, routeMaps, body.ToolChoice)
				calls = filterCompletedCalls(calls, ledger)
				if parsed && len(calls) > 0 {
					scope := fmt.Sprintf("%d:%v:required-retry", len(body.Messages), completedCallIDs(ledger))
					for i := range calls {
						calls[i].ID = scopedCallID(calls[i].Name, string(calls[i].Arguments), i, scope)
					}
					calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
					_ = writeToolResponse(w, "chatcmpl-"+uuid.NewString(), firstNonEmpty(body.Model, "m365-copilot"), body.Stream, calls, retryRes)
					return
				}
			}
			http.Error(w, "model did not select a required tool after constrained retry", http.StatusBadGateway)
			return
		}
	}
	if len(ledger.Completed) > 0 || len(ledger.Pending) > 0 {
		answerPrompt += "\n" + ledger.RouterContext()
	}
	if len(ledger.Completed) > 0 {
		answerPrompt += "\nFINAL ANSWER RULE: Report only actions supported by completed tool results. If the goal is not fully verified, state exactly what remains unconfirmed."
	}
	answerReq := chathub.Request{Text: answerPrompt, Tone: tone, ConversationID: body.ConversationID, SessionID: body.SessionID, Attachments: body.Attachments}
	if planningMode == "native" {
		answerReq.Tools = body.Tools
		answerReq.ToolChoice = body.ToolChoice
	}
	var res chathub.Result
	if body.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "stream unsupported", http.StatusInternalServerError)
			return
		}
		id := "chatcmpl-" + uuid.NewString()
		model := firstNonEmpty(body.Model, "m365-copilot")
		firstDelta := true
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
			rc := http.NewResponseController(w)
			_ = rc.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, err := fmt.Fprintf(w, "data: %s\n\n", mustJSON(chunk)); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		}
		onDelta := func(content string) error {
			if content != "" {
				return writeChunk(map[string]any{"content": content})
			}
			return nil
		}
		onReasoning := func(reasoning string) error {
			if reasoning != "" {
				return writeChunk(map[string]any{"reasoning_content": reasoning})
			}
			return nil
		}
		if err := sseRaw(r.Context(), w, flusher, ": connected\n\n"); err != nil {
			return
		}
		res, err = s.chat.ChatWithReasoning(ctx, account, answerReq, onDelta, onReasoning)
		if err == nil {
			s.accountPool.MarkSuccess(acc.ID)
			writeStreamFinish(r.Context(), w, flusher, id, model)
			_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
		} else {
			log.Printf("[req-trace] id=%s stage=stream_error err=%v", requestID, err)
			s.accountPool.MarkFailure(acc.ID, err, rateLimitCooldown)
			msg := upstreamError(err)
			if IsRateLimited(err) {
				msg = "upstream is rate limiting; try again shortly"
			}
			_ = sseRaw(r.Context(), w, flusher, "data: "+mustJSON(map[string]any{"error": map[string]any{"message": msg, "code": "rate_limit"}})+"\n\n")
			_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
		}
	} else {
		res, err = s.chat.Chat(ctx, account, answerReq)
		if err != nil && body.AccountID == "" && body.ConversationID == "" && (IsRateLimited(err) || IsAuthFailure(err)) {
			// Failover only when nothing pins the request to a conversation or
			// account; a fresh chat can safely retry on the next healthy account.
			next, nerr := s.nextHealthyAccount(acc.ID)
			if nerr == nil {
				ctx2, cancel2 := context.WithTimeout(r.Context(), time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second)
				defer cancel2()
				res2, err2 := s.chat.Chat(ctx2, chathub.Account{AccessToken: next.AccessToken, OID: next.OID, TID: next.TID}, answerReq)
				if err2 == nil {
					res = res2
					acc = next
					err = nil
					s.accountPool.MarkSuccess(next.ID)
				}
			}
		}
	}
	if err != nil {
		s.accountPool.MarkFailure(acc.ID, err, rateLimitCooldown)
		writeUpstreamError(w, err)
		return
	}
	if body.Stream {
		if body.User != "" && res.ConversationID != "" {
			s.userSessions.Put(body.User, res.ConversationID, res.SessionID, acc.ID)
		}
		s.bindConversation(acc, &body, r, res, prompt, startedAt)
		return
	}

	if body.SessionKey != "" {
		s.sessions.upsert(conversation{ID: body.SessionKey, AccountID: acc.ID, ConversationID: res.ConversationID, SessionID: res.SessionID, Title: prompt})
	}
	if body.User != "" && res.ConversationID != "" {
		s.userSessions.Put(body.User, res.ConversationID, res.SessionID, acc.ID)
		log.Printf("[user-session] put user=%s conversation=%s session=%s", body.User, res.ConversationID, res.SessionID)
	}
	if res.ConversationID != "" {
		s.bindConversation(acc, &body, r, res, prompt, startedAt)
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
	if calls := fencedToolCalls(res.Text, toolMaps, body.ToolChoice); len(calls) > 0 {
		calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
		_ = writeToolResponse(w, id, model, body.Stream, calls, res)
		return
	}
	if calls := nativeToolCalls(res.Events, body.Tools); len(calls) > 0 {
		calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
		_ = writeToolResponse(w, id, model, body.Stream, calls, res)
		return
	}
	// Recover natural-language tool intent when native mode emits no
	// structured ChatHub tool event. Plain text remains a zero-call result.
	if planningMode == "native" && len(routeMaps) > 0 && fmt.Sprint(body.ToolChoice) != "none" {
		routePrompt := modelToolRouterPrompt(prompt+"\n"+ledger.RouterContext(), routeMaps, body.ToolChoice)
		routeRes, routeErr := s.chat.Chat(ctx, account, chathub.Request{Text: routePrompt, Tone: tone, Attachments: body.Attachments})
		if routeErr == nil {
			calls, parsed := parseModelToolDecision(routeRes.Text, routeMaps, body.ToolChoice)
			if !parsed {
				repairRes, repairErr := s.chat.Chat(ctx, account, chathub.Request{Text: `Repair this tool routing output into JSON only with shape {"calls":[{"name":"function_name","arguments":{}}]}. Use {"calls":[]} if no tool is needed. OUTPUT:\n` + compactToolResult(routeRes.Text, 6000), Tone: tone, Attachments: body.Attachments})
				if repairErr == nil {
					calls, parsed = parseModelToolDecision(repairRes.Text, routeMaps, body.ToolChoice)
				}
			}
			if parsed && len(calls) > 0 {
				scope := fmt.Sprintf("%d:%v:native-recovery", len(body.Messages), completedCallIDs(ledger))
				for i := range calls {
					calls[i].ID = scopedCallID(calls[i].Name, string(calls[i].Arguments), i, scope)
				}
				calls = limitToolCalls(calls, adaptiveToolCallLimit(calls, configuredToolCallLimit(s.settings)))
				_ = writeToolResponse(w, id, model, body.Stream, calls, routeRes)
				return
			}
		}
	}
	if !completionEvidenceAllows(res.Text, ledger) {
		res.Text = "I cannot confirm completion because no matching tool results were returned. No external action has been verified."
	}
	log.Printf("[debug] res.Text bytes=%d content=%q", len(res.Text), res.Text)
	created := time.Now().Unix()

	if body.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "stream unsupported", http.StatusInternalServerError)
			return
		}
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
		_ = sseRaw(r.Context(), w, flusher, "data: "+string(b)+"\n\n")
		writeStreamFinish(r.Context(), w, flusher, id, model)
		_ = sseRaw(r.Context(), w, flusher, "data: [DONE]\n\n")
		return
	}

	if responseFormat != nil && (responseFormat.Type == "json_object" || responseFormat.Type == "json_schema") {
		res.Text = normalizeJSONText(res.Text)
	}
	content := any(res.Text)
	if len(res.Images) > 0 {
		parts := []any{map[string]any{"type": "text", "text": res.Text}}
		for _, u := range res.Images {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": u}})
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

const sessionHeaderName = "X-M365-Session-Id"

// bindConversation 在请求完成后登记会话解析器索引与缓存统计，流式与非流式
// 路径共用。会话为内容键，云端的对话由 auto_cleanup 按 2h 闲置窗口回收，
// 这里不再做"用完即删"，否则复用永远不可能命中。
func (s *Server) bindConversation(acc auth.AccountToken, body *oaiReq, r *http.Request, res chathub.Result, prompt string, startedAt time.Time) {
	if res.ConversationID == "" {
		return
	}
	s.sessionResolver.Bind("", res.ConversationID, acc.ID, body, r)
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
