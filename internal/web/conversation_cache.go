package web

import (
	"crypto/sha256"
	"encoding/hex"
	"m365-copilot2api/internal/chathub"
	"sync"
	"time"
)

type cachedConversation struct {
	ConversationID string
	SessionID      string
	Tone           string
	TurnCount      int
	MessageCount   int
	CreatedAt      time.Time
	LastUsedAt     time.Time
	SystemPrompt   string
}

type conversationCache struct {
	mu      sync.Mutex
	entries map[string]*cachedConversation
	maxAge  time.Duration
}

func newConversationCache() *conversationCache {
	return &conversationCache{
		entries: make(map[string]*cachedConversation),
		maxAge:  20 * time.Minute,
	}
}

func (c *conversationCache) key(accountID, model string) string {
	return accountID + "|" + model
}

func (c *conversationCache) Lookup(accountID, model string) *cachedConversation {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[c.key(accountID, model)]
	if entry == nil {
		return nil
	}
	if time.Since(entry.LastUsedAt) > c.maxAge {
		delete(c.entries, c.key(accountID, model))
		return nil
	}
	return entry
}

func (c *conversationCache) Store(accountID, model string, conv *cachedConversation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conv.LastUsedAt = time.Now()
	c.entries[c.key(accountID, model)] = conv
}

func (c *conversationCache) Invalidate(accountID, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, c.key(accountID, model))
}

func (c *conversationCache) GC() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.entries {
		if now.Sub(v.LastUsedAt) > c.maxAge {
			delete(c.entries, k)
		}
	}
}

func (c *conversationCache) Stats() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{"cached_conversations": len(c.entries)}
}

func systemPromptHash(messages []oaiMsg) string {
	for _, m := range messages {
		if m.Role == "system" || m.Role == "developer" {
			text := contentToString(m.Content)
			if len(text) > 500 {
				text = text[:500]
			}
			h := sha256.Sum256([]byte(text))
			return hex.EncodeToString(h[:])
		}
	}
	for _, m := range messages {
		if m.Role == "user" {
			text := contentToString(m.Content)
			if len(text) > 200 {
				text = text[:200]
			}
			h := sha256.Sum256([]byte("user:" + text))
			return hex.EncodeToString(h[:])
		}
	}
	return ""
}

func extractLastUserMessage(messages []oaiMsg) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return contentToString(messages[i].Content)
		}
	}
	return ""
}

func (s *Server) storeConvCache(accID, model string, res chathub.Result, tone string, messages []oaiMsg, reused bool) {
	if res.ConversationID == "" {
		return
	}
	cached := s.convCache.Lookup(accID, model)
	entry := &cachedConversation{
		ConversationID: res.ConversationID,
		SessionID:      res.SessionID,
		Tone:           tone,
		MessageCount:   len(messages),
		SystemPrompt:   systemPromptHash(messages),
	}
	if cached != nil && cached.ConversationID == res.ConversationID {
		entry.TurnCount = cached.TurnCount + 1
	} else {
		entry.TurnCount = 1
	}
	s.convCache.Store(accID, model, entry)
}

func (s *Server) invalidateConvCache(accID, model string) {
	s.convCache.Invalidate(accID, model)
}
