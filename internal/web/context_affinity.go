package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

func emptyMessageContent(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

type contextBinding struct {
	AccountID      string    `json:"accountId"`
	ConversationID string    `json:"conversationId"`
	SessionID      string    `json:"sessionId"`
	LastUsedAt     time.Time `json:"lastUsedAt"`
	Hash           string    `json:"hash"`
	Messages       []oaiMsg  `json:"messages,omitempty"`
	ExplicitID     string    `json:"explicitId,omitempty"`
}

type contextAffinity struct {
	mu         sync.Mutex
	ttl        time.Duration
	max        int
	bindings   map[string]contextBinding
	byHash     map[string]string
	byExplicit map[string]string
}

func newContextAffinity() *contextAffinity {
	return &contextAffinity{
		ttl:        2 * time.Hour,
		max:        1000,
		bindings:   map[string]contextBinding{},
		byHash:     map[string]string{},
		byExplicit: map[string]string{},
	}
}

func (c *contextAffinity) evictLocked(now time.Time) {
	for k, b := range c.bindings {
		if now.Sub(b.LastUsedAt) > c.ttl {
			delete(c.bindings, k)
			if b.Hash != "" {
				if c.byHash[b.Hash] == k {
					delete(c.byHash, b.Hash)
				}
			}
			if b.ExplicitID != "" {
				if c.byExplicit[b.ExplicitID] == k {
					delete(c.byExplicit, b.ExplicitID)
				}
			}
		}
	}
	if len(c.bindings) <= c.max {
		return
	}
	ids := make([]string, 0, len(c.bindings))
	last := make(map[string]time.Time, len(c.bindings))
	for k, b := range c.bindings {
		ids = append(ids, k)
		last[k] = b.LastUsedAt
	}
	sort.Slice(ids, func(i, j int) bool { return last[ids[i]].Before(last[ids[j]]) })
	for _, k := range ids[:len(c.bindings)-c.max] {
		b := c.bindings[k]
		delete(c.bindings, k)
		if b.Hash != "" && c.byHash[b.Hash] == k {
			delete(c.byHash, b.Hash)
		}
		if b.ExplicitID != "" && c.byExplicit[b.ExplicitID] == k {
			delete(c.byExplicit, b.ExplicitID)
		}
	}
}

func canonicalContentForAffinity(v any) any {
	switch x := v.(type) {
	case nil, string, bool, float64:
		return x
	case json.Number:
		return x
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = canonicalContentForAffinity(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = canonicalContentForAffinity(e)
		}
		return out
	default:
		b, _ := json.Marshal(x)
		var m any
		if json.Unmarshal(b, &m) == nil {
			return canonicalContentForAffinity(m)
		}
		return x
	}
}

func affinityMessageCanon(m oaiMsg) map[string]any {
	content := canonicalContentForAffinity(m.Content)
	if len(m.ToolCalls) > 0 && emptyMessageContent(m.Content) {
		content = ""
	}
	out := map[string]any{
		"role":    strings.ToLower(strings.TrimSpace(m.Role)),
		"content": content,
	}
	if m.Name != "" {
		out["name"] = m.Name
	}
	if m.ToolCallID != "" {
		out["tool_call_id"] = m.ToolCallID
	}
	if len(m.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(m.ToolCalls))
		for _, call := range m.ToolCalls {
			fn, _ := call["function"].(map[string]any)
			name := ""
			if fn != nil {
				name, _ = fn["name"].(string)
			}
			args := any(nil)
			if fn != nil {
				args = canonicalContentForAffinity(fn["arguments"])
				if s, ok := args.(string); ok {
					var dec any
					if json.Unmarshal([]byte(s), &dec) == nil {
						args = canonicalContentForAffinity(dec)
					}
				}
			}
			calls = append(calls, map[string]any{"name": name, "arguments": args})
		}
		out["tool_calls"] = calls
	}
	return out
}

func contextHash(messages []oaiMsg) string {
	if len(messages) == 0 {
		return ""
	}
	canon := make([]map[string]any, len(messages))
	for i, m := range messages {
		canon[i] = affinityMessageCanon(m)
	}
	b, _ := json.Marshal(canon)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (c *contextAffinity) lookupPrefixLocked(messages []oaiMsg) (string, int) {
	bestKey := ""
	bestN := 0
	var bestTime time.Time
	for k, b := range c.bindings {
		if len(b.Messages) == 0 || len(messages) < len(b.Messages) {
			continue
		}
		n := len(b.Messages)
		match := true
		for i := 0; i < n; i++ {
			if !messagesEqual(b.Messages[i], messages[i]) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if n > bestN || (n == bestN && b.LastUsedAt.After(bestTime)) {
			bestKey = k
			bestN = n
			bestTime = b.LastUsedAt
		}
	}
	return bestKey, bestN
}

// Resolve returns accountID for given context if sticky exists, or allocates a new one via round-robin.
// Caller must respect explicit accountId/conversation binding before calling this: if those are present,
// this function returns ("", false) meaning no affinity decision.
// Thread-safe.
func (c *contextAffinity) Resolve(s *Server, r *http.Request, body *oaiReq) (string, bool) {
	if c == nil || s == nil || body == nil {
		return "", false
	}
	if strings.TrimSpace(body.AccountID) != "" {
		return "", false
	}
	if body.ConversationID != "" || body.SessionID != "" || body.SessionKey != "" {
		return "", false
	}
	explicitID := strings.TrimSpace(r.Header.Get(sessionHeaderName))
	// Prefer explicit session header as affinity key if present
	if explicitID != "" {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.evictLocked(time.Now())
		if key, ok := c.byExplicit[explicitID]; ok {
			if b, ok := c.bindings[key]; ok {
				if s.accountUsable(b.AccountID) {
					b.LastUsedAt = time.Now()
					c.bindings[key] = b
					return b.AccountID, true
				}
			}
		}
		// allocate new for this explicit session
		accID := c.pickNextHealthyLocked(s)
		if accID == "" {
			return "", false
		}
		key := explicitID
		if _, exists := c.bindings[key]; exists {
			key = explicitID + ":" + contextHash(body.Messages)[:8]
		}
		b := contextBinding{AccountID: accID, LastUsedAt: time.Now(), Hash: contextHash(body.Messages), Messages: cloneMessages(body.Messages), ExplicitID: explicitID}
		c.bindings[key] = b
		c.byExplicit[explicitID] = key
		if b.Hash != "" {
			c.byHash[b.Hash] = key
		}
		return accID, true
	}

	hash := contextHash(body.Messages)
	if hash == "" {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictLocked(time.Now())
	if key, ok := c.byHash[hash]; ok {
		if b, ok := c.bindings[key]; ok {
			if s.accountUsable(b.AccountID) {
				b.LastUsedAt = time.Now()
				c.bindings[key] = b
				return b.AccountID, true
			}
		}
	}
	if key, _ := c.lookupPrefixLocked(body.Messages); key != "" {
		if b, ok := c.bindings[key]; ok {
			if s.accountUsable(b.AccountID) {
				newHash := hash
				// migrate binding to new hash (sticky continuation)
				oldHash := b.Hash
				b.Hash = newHash
				b.Messages = cloneMessages(body.Messages)
				b.LastUsedAt = time.Now()
				c.bindings[key] = b
				if oldHash != "" && oldHash != newHash {
					delete(c.byHash, oldHash)
				}
				c.byHash[newHash] = key
				return b.AccountID, true
			}
		}
	}
	accID := c.pickNextHealthyLocked(s)
	if accID == "" {
		return "", false
	}
	key := hash
	if _, exists := c.bindings[key]; exists {
		key = hash + ":" + hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))[:8]
	}
	b := contextBinding{AccountID: accID, LastUsedAt: time.Now(), Hash: hash, Messages: cloneMessages(body.Messages)}
	c.bindings[key] = b
	c.byHash[hash] = key
	return accID, true
}

// ForgetAccount drops every sticky binding that points at accountID. Call it
// when an account turns out to be unusable so later requests are re-routed
// instead of repeatedly hitting a dead account.
func (c *contextAffinity) ForgetAccount(accountID string) {
	if c == nil || accountID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, b := range c.bindings {
		if b.AccountID != accountID {
			continue
		}
		delete(c.bindings, k)
		if b.Hash != "" && c.byHash[b.Hash] == k {
			delete(c.byHash, b.Hash)
		}
		if b.ExplicitID != "" && c.byExplicit[b.ExplicitID] == k {
			delete(c.byExplicit, b.ExplicitID)
		}
	}
}

func (c *contextAffinity) pickNextHealthyLocked(s *Server) string {
	accounts := s.tokens.List()
	if len(accounts) == 0 {
		return ""
	}
	used := make(map[string]bool, len(c.bindings))
	for _, b := range c.bindings {
		if time.Since(b.LastUsedAt) < 5*time.Minute {
			used[b.AccountID] = true
		}
	}
	// prefer unused healthy account in round-robin order
	var firstHealthy string
	for attempts := 0; attempts < len(accounts)*2; attempts++ {
		acc, ok := s.tokens.Next()
		if !ok {
			break
		}
		if !s.accountAvailable(acc.ID) {
			continue
		}
		if firstHealthy == "" {
			firstHealthy = acc.ID
		}
		if !used[acc.ID] {
			return acc.ID
		}
	}
	return firstHealthy
}

func (c *contextAffinity) BindConversation(accountID, conversationID, sessionID string, body *oaiReq, r *http.Request) {
	if c == nil || accountID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	hash := contextHash(body.Messages)
	explicitID := strings.TrimSpace(r.Header.Get(sessionHeaderName))
	if explicitID != "" {
		if key, ok := c.byExplicit[explicitID]; ok {
			if b, ok := c.bindings[key]; ok {
				b.AccountID = accountID
				b.ConversationID = conversationID
				b.SessionID = sessionID
				b.LastUsedAt = time.Now()
				if hash != "" {
					oldHash := b.Hash
					b.Hash = hash
					b.Messages = cloneMessages(body.Messages)
					if oldHash != "" && oldHash != hash {
						delete(c.byHash, oldHash)
					}
					c.byHash[hash] = key
				}
				c.bindings[key] = b
				return
			}
		}
	}
	if hash != "" {
		if key, ok := c.byHash[hash]; ok {
			if b, ok := c.bindings[key]; ok {
				b.AccountID = accountID
				b.ConversationID = conversationID
				b.SessionID = sessionID
				b.LastUsedAt = time.Now()
				b.Messages = cloneMessages(body.Messages)
				c.bindings[key] = b
				return
			}
		}
	}
	// also check prefix match for continuation
	if key, _ := c.lookupPrefixLocked(body.Messages); key != "" {
		if b, ok := c.bindings[key]; ok {
			b.AccountID = accountID
			b.ConversationID = conversationID
			b.SessionID = sessionID
			b.LastUsedAt = time.Now()
			if hash != "" {
				oldHash := b.Hash
				b.Hash = hash
				b.Messages = cloneMessages(body.Messages)
				if oldHash != "" && oldHash != hash {
					delete(c.byHash, oldHash)
				}
				c.byHash[hash] = key
			}
			c.bindings[key] = b
			return
		}
	}
}
