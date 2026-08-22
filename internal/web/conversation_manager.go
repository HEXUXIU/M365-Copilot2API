package web

import (
	"encoding/json"
	"log"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

type ConversationCleanupMode string

const (
	CleanupAfterResponse ConversationCleanupMode = "after_response"
	CleanupOnExit        ConversationCleanupMode = "on_exit"
	CleanupKeepN         ConversationCleanupMode = "keep_n"
	CleanupMaxAge        ConversationCleanupMode = "max_age"
)

type managedConversation struct {
	ID         string    `json:"id"`
	AccountID  string    `json:"accountId"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	Title      string    `json:"title,omitempty"`
}

type conversationManager struct {
	mu        sync.Mutex
	path      string
	data      map[string]managedConversation
	mode      ConversationCleanupMode
	keepN     int
	maxAge    time.Duration
	whitelist map[string]bool
	persist   *persistStore
}

type conversationPersist struct {
	Conversations map[string]managedConversation `json:"conversations"`
	Whitelist     []string                       `json:"whitelist,omitempty"`
}

func openConversationManager() *conversationManager {
	mode := CleanupAfterResponse
	if v := os.Getenv("M365_CLEANUP_MODE"); v != "" {
		mode = ConversationCleanupMode(v)
	}
	keepN := 5
	if v := os.Getenv("M365_CLEANUP_KEEP_N"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			keepN = n
		}
	}
	maxAge := 24 * time.Hour
	if v := os.Getenv("M365_CLEANUP_MAX_AGE_HOURS"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 {
			maxAge = time.Duration(h) * time.Hour
		}
	}
	path := os.Getenv("M365_CONVERSATION_CACHE")
	if path == "" {
		path = "conversations.json"
	}
	cm := &conversationManager{
		path:      path,
		data:      map[string]managedConversation{},
		mode:      mode,
		keepN:     keepN,
		maxAge:    maxAge,
		whitelist: map[string]bool{},
	}
	cm.persist = &persistStore{flush: cm.flush}
	cm.loadLocked()
	return cm
}

func (cm *conversationManager) loadLocked() {
	b, err := os.ReadFile(cm.path)
	if err != nil {
		return
	}
	var p conversationPersist
	if json.Unmarshal(b, &p) == nil && p.Conversations != nil {
		cm.data = p.Conversations
		for _, id := range p.Whitelist {
			cm.whitelist[id] = true
		}
		return
	}
	_ = json.Unmarshal(b, &cm.data)
}

// flush 在锁内生成快照，锁外写盘。
func (cm *conversationManager) flush() error {
	cm.mu.Lock()
	p := conversationPersist{Conversations: cm.data}
	for id := range cm.whitelist {
		p.Whitelist = append(p.Whitelist, id)
	}
	b, err := json.MarshalIndent(p, "", "  ")
	cm.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(cm.path, b, 0o600)
}

func (cm *conversationManager) Record(conversationID, accountID, title string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	now := time.Now().UTC()
	existing, hasExisting := cm.data[conversationID]
	rec := managedConversation{
		ID:         conversationID,
		AccountID:  accountID,
		CreatedAt:  now,
		LastUsedAt: now,
		Title:      title,
	}
	if hasExisting {
		rec.CreatedAt = existing.CreatedAt
	}
	cm.data[conversationID] = rec
	cm.persist.markDirty()
	log.Printf("[conversation-manager] recorded conversation %s", conversationID)
}

func (cm *conversationManager) Whitelist(conversationID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.whitelist[conversationID] = true
	cm.persist.markDirty()
}

func (cm *conversationManager) Unwhitelist(conversationID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.whitelist, conversationID)
	cm.persist.markDirty()
}

func (cm *conversationManager) IsWhitelisted(conversationID string) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.whitelist[conversationID]
}

func (cm *conversationManager) WhitelistedIDs() []string {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	out := make([]string, 0, len(cm.whitelist))
	for id := range cm.whitelist {
		out = append(out, id)
	}
	return out
}

func (cm *conversationManager) Delete(conversationID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.data, conversationID)
	cm.persist.markDirty()
	log.Printf("[conversation-manager] deleted conversation %s", conversationID)
}

func (cm *conversationManager) List() []managedConversation {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	out := make([]managedConversation, 0, len(cm.data))
	for _, v := range cm.data {
		out = append(out, v)
	}
	return out
}

func (cm *conversationManager) Cleanup() []string {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	var toDelete []string
	now := time.Now().UTC()

	switch cm.mode {
	case CleanupAfterResponse:
		for id, c := range cm.data {
			if cm.whitelist[id] {
				continue
			}
			if now.Sub(c.LastUsedAt) > 30*time.Second {
				toDelete = append(toDelete, id)
			}
		}
	case CleanupMaxAge:
		cutoff := now.Add(-cm.maxAge)
		for id, c := range cm.data {
			if cm.whitelist[id] {
				continue
			}
			if c.CreatedAt.Before(cutoff) {
				toDelete = append(toDelete, id)
			}
		}
	case CleanupKeepN:
		if len(cm.data) > cm.keepN {
			type item struct {
				id       string
				lastUsed time.Time
			}
			items := make([]item, 0, len(cm.data))
			for id, c := range cm.data {
				items = append(items, item{id, c.LastUsedAt})
			}
			sort.Slice(items, func(i, j int) bool {
				return items[i].lastUsed.After(items[j].lastUsed)
			})
			for i := cm.keepN; i < len(items); i++ {
				if cm.whitelist[items[i].id] {
					continue
				}
				toDelete = append(toDelete, items[i].id)
			}
		}
	}

	for _, id := range toDelete {
		delete(cm.data, id)
	}
	if len(toDelete) > 0 {
		cm.persist.markDirty()
		log.Printf("[conversation-manager] cleaned up %d conversations", len(toDelete))
	}
	return toDelete
}

func (cm *conversationManager) ShouldCleanup() bool {
	return cm.mode != CleanupOnExit
}

func (cm *conversationManager) Mode() ConversationCleanupMode {
	return cm.mode
}

func (cm *conversationManager) SetMode(mode ConversationCleanupMode) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.mode = mode
}
