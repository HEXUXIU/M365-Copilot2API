package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// sessionBinding 璁板綍涓€娆″唴瀹归敭澶嶇敤鐨勪細璇濄€侷dentity 瀛楁锛圛P/user锛変粎浣?
// 璇婃柇鍏冩暟鎹繚鐣欙紝鍖归厤鍒ゅ畾鍙緷璧栦笂涓嬫枃鍐呭锛岃 Resolve 鐨勫唴瀹归敭閫昏緫銆?
type sessionBinding struct {
	SessionID      string    `json:"sessionId"`
	ConversationID string    `json:"conversationId"`
	AccountID      string    `json:"accountId"`
	CreatedAt      time.Time `json:"createdAt"`
	LastUsedAt     time.Time `json:"lastUsedAt"`
	IPFingerprint  string    `json:"ipFingerprint,omitempty"`
	UserField      string    `json:"userField,omitempty"`
	ContextFinger  string    `json:"contextFinger,omitempty"`
	// ContextHistory 鎸佷箙鍖栦繚瀛樻渶杩戜竴娆″崗璁殑瀹屾暣娑堟伅锛屼緵閲嶅惎鍚庣户缁仛
	// 鍐呭鍓嶇紑鍖归厤锛岄伩鍏嶈繘绋嬮噸鍚鑷存墍鏈変細璇濋敭鍏ㄩ儴澶辨晥銆?
	ContextHistory []oaiMsg `json:"contextHistory,omitempty"`
}

type sessionResolver struct {
	mu          sync.Mutex
	path        string
	sessions    map[string]sessionBinding
	byExplicit  map[string]string // explicitID -> sessionID
	byUserField map[string]string // userField -> sessionID
	byIPFinger  map[string]string // ipFingerprint -> sessionID
	byContext   map[string]string // contextFingerprint -> sessionID
	ttl         time.Duration
	contextTTL  time.Duration
	maxSessions int
	persist     *persistStore
}

const defaultMaxSessions = 1000

func openSessionResolver() *sessionResolver {
	// 闂茬疆 2 灏忔椂鍗宠涓鸿繃鏈燂紙鐢ㄦ埛锛? 灏忔椂涓嶆椿璺冨凡缁忕畻涔咃級銆備細璇濊繃鏈熷悗
	// 浠?sessions.json 鍓旈櫎锛屼簯绔璇濅氦缁?auto_cleanup 鎸夌浉鍚岀獥鍙ｅ洖鏀躲€?
	ttl := 2 * time.Hour
	if v := os.Getenv("M365_SESSION_TTL_MINUTES"); v != "" {
		if d, err := time.ParseDuration(v + "m"); err == nil {
			ttl = d
		}
	}
	contextTTL := 2 * time.Hour
	if v := os.Getenv("M365_CONTEXT_TTL_MINUTES"); v != "" {
		if d, err := time.ParseDuration(v + "m"); err == nil {
			contextTTL = d
		}
	}
	path := os.Getenv("M365_SESSION_CACHE")
	if path == "" {
		path = "sessions.json"
	}
	sr := &sessionResolver{
		path:        path,
		sessions:    map[string]sessionBinding{},
		byExplicit:  map[string]string{},
		byUserField: map[string]string{},
		byIPFinger:  map[string]string{},
		byContext:   map[string]string{},
		ttl:         ttl,
		contextTTL:  contextTTL,
		maxSessions: defaultMaxSessions,
	}
	sr.persist = &persistStore{flush: sr.flush}
	sr.loadLocked()
	return sr
}

func (sr *sessionResolver) loadLocked() {
	if b, err := os.ReadFile(sr.path); err == nil {
		var list []sessionBinding
		if err := json.Unmarshal(b, &list); err == nil {
			now := time.Now().UTC()
			for _, s := range list {
				if now.Sub(s.LastUsedAt) > sr.ttl {
					continue
				}
				sr.reindexLocked(s)
			}
		}
	}
}

// flush 在锁内生成快照，锁外写盘。
func (sr *sessionResolver) flush() error {
	sr.mu.Lock()
	list := make([]sessionBinding, 0, len(sr.sessions))
	for _, s := range sr.sessions {
		list = append(list, s)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	sr.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(sr.path, b, 0o600)
}

func (sr *sessionResolver) reindexLocked(s sessionBinding) {
	sr.sessions[s.SessionID] = s
	if s.UserField != "" {
		sr.byUserField[s.UserField] = s.SessionID
	}
	if s.IPFingerprint != "" {
		sr.byIPFinger[s.IPFingerprint] = s.SessionID
	}
	if s.ContextFinger != "" {
		sr.byContext[s.ContextFinger] = s.SessionID
	}
}

func (sr *sessionResolver) evictLocked() {
	now := time.Now().UTC()
	for id, s := range sr.sessions {
		if now.Sub(s.LastUsedAt) > sr.ttl {
			sr.dropLocked(id, s)
		}
	}
	if len(sr.sessions) > sr.maxSessions {
		// Bound memory by dropping the least recently used sessions.
		ids := make([]string, 0, len(sr.sessions))
		last := make(map[string]time.Time, len(sr.sessions))
		for id, s := range sr.sessions {
			ids = append(ids, id)
			last[id] = s.LastUsedAt
		}
		sort.Slice(ids, func(i, j int) bool { return last[ids[i]].Before(last[ids[j]]) })
		for _, id := range ids[:len(sr.sessions)-sr.maxSessions] {
			sr.dropLocked(id, sr.sessions[id])
		}
	}
}

func (sr *sessionResolver) dropLocked(id string, s sessionBinding) {
	delete(sr.sessions, id)
	if sr.byUserField[s.UserField] == id {
		delete(sr.byUserField, s.UserField)
	}
	if sr.byIPFinger[s.IPFingerprint] == id {
		delete(sr.byIPFinger, s.IPFingerprint)
	}
	if sr.byContext[s.ContextFinger] == id {
		delete(sr.byContext, s.ContextFinger)
	}
}

type ResolveResult struct {
	SessionID      string
	ConversationID string
	AccountID      string
	MatchedBy      string
	IsNew          bool
	// HistoryLen 鏄鐢ㄥ懡涓椂"浜戠瀵硅瘽宸插寘鍚殑娑堟伅鏉℃暟"锛?
	// 鍗冲閲忓彂閫佺殑璧风偣涓嬫爣锛坆ody.Messages[HistoryLen:] 鍙彂鏂板閮ㄥ垎锛夈€?
	HistoryLen int
}

func clientIPFingerprint(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ua := r.Header.Get("User-Agent")
	data := host + "|" + ua
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:16])
}

func contextFingerprint(messages []oaiMsg) string {
	if len(messages) == 0 {
		return ""
	}
	var parts []string
	limit := len(messages)
	if limit > 3 {
		limit = 3
	}
	for i := len(messages) - limit; i < len(messages); i++ {
		m := messages[i]
		parts = append(parts, m.Role+":"+contentToString(m.Content))
	}
	data := strings.Join(parts, "||")
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:16])
}

func contextSimilarity(a, b []oaiMsg) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// 全历史相似：把双方所有消息文本拼起来做 Jaccard，比只比最后一条
	// 更能代表整段上下文，避免两条短消息（如"继续"）就误判高度相似。
	var ta, tb strings.Builder
	for _, m := range a {
		ta.WriteString(m.Role + ":" + contentToString(m.Content) + "\n")
	}
	for _, m := range b {
		tb.WriteString(m.Role + ":" + contentToString(m.Content) + "\n")
	}
	return jaccardSimilarity(ta.String(), tb.String())
}

func jaccardSimilarity(a, b string) float64 {
	setA := tokenize(a)
	setB := tokenize(b)
	if len(setA) == 0 && len(setB) == 0 {
		return 1.0
	}
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}
	intersection := 0
	for k := range setA {
		if setB[k] {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func tokenize(s string) map[string]bool {
	tokens := map[string]bool{}
	words := strings.Fields(strings.ToLower(s))
	for _, w := range words {
		tokens[w] = true
	}
	return tokens
}

func (sr *sessionResolver) Resolve(r *http.Request, body *oaiReq) ResolveResult {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.evictLocked()

	explicitID := r.Header.Get("X-M365-Session-Id")

	// 瀹㈡埛绔樉寮忔寚瀹氱殑浼氳瘽 ID 鏄渶楂樹紭鍏堢殑缁帴璇箟锛氫笉鍙備笌浠讳綍韬唤鍒ゅ畾锛?
	// 鐢辫皟鐢ㄦ柟涓诲姩鍐冲畾瑕佺户缁摢涓簯绔璇濄€?
	if explicitID != "" {
		if sessID, ok := sr.byExplicit[explicitID]; ok {
			if sess, ok := sr.sessions[sessID]; ok {
				sess.LastUsedAt = time.Now().UTC()
				sr.sessions[sessID] = sess
				sr.persist.markDirty()
				return ResolveResult{
					SessionID:      sess.SessionID,
					ConversationID: sess.ConversationID,
					AccountID:      sess.AccountID,
					MatchedBy:      "explicit",
					IsNew:          false,
					HistoryLen:     len(sess.ContextHistory),
				}
			}
		}
		if sess, ok := sr.sessions[explicitID]; ok {
			sess.LastUsedAt = time.Now().UTC()
			sr.sessions[explicitID] = sess
			sr.persist.markDirty()
			return ResolveResult{
				SessionID:      sess.SessionID,
				ConversationID: sess.ConversationID,
				AccountID:      sess.AccountID,
				MatchedBy:      "explicit",
				IsNew:          false,
				HistoryLen:     len(sess.ContextHistory),
			}
		}
	}

	// 鍐呭閿細鍗忚娑堟伅鍚嶅簭鍒椾弗鏍肩瓑浜庢煇涓凡璁板綍浼氳瘽鐨勫巻鍙叉椂鐩存帴澶嶇敤杩欎釜
	// 浜戠瀵硅瘽锛屼絾鍙湪鍚屼竴 IP/UA 鎸囩汗涓嬶紝閬垮厤鐭秷鎭湪涓嶅悓鐢ㄦ埛闂翠簰绔?
	// HistoryLen 杩斿洖璇ュ墠缂€闀垮害锛屼笂灞傛嵁姝ゅ彧鍙戦€?messages[HistoryLen:] 澧為噺銆?
	ipFinger := clientIPFingerprint(r)
	if bestID, n := sr.matchContextLocked(ipFinger, body.Messages); bestID != "" {
		sess := sr.sessions[bestID]
		sess.LastUsedAt = time.Now().UTC()
		sr.sessions[bestID] = sess
		sr.persist.markDirty()
		return ResolveResult{
			SessionID:      sess.SessionID,
			ConversationID: sess.ConversationID,
			AccountID:      sess.AccountID,
			MatchedBy:      fmt.Sprintf("context_prefix_%d", n),
			IsNew:          false,
			HistoryLen:     n,
		}
	}

	// 寮辩害鏉熷厹搴曪細鍐呭涓嶆瀯鎴愪弗鏍煎墠缂€锛屼絾涓庢煇涓巻鍙查珮搴︾浉浼硷紙濡傚鎴风
	// 鏈湴鎴柇浜嗗巻鍙诧級锛屼粛澶嶇敤璇ヤ細璇濄€傛鏃跺閲忚竟鐣屾湭鐭ワ紝涓婂眰鍙戦€佸叏閲忋€?
	threshold := 0.6
	if v := os.Getenv("M365_CONTEXT_SIMILARITY"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			threshold = f
		}
	}
	bestMatchID := ""
	bestSimilarity := 0.0
	for id, sess := range sr.sessions {
		if time.Since(sess.LastUsedAt) > sr.contextTTL {
			continue
		}
		// 兜底同样受身份约束：只有同一 IP/UA 才可能复用。
		if sess.IPFingerprint != ipFinger {
			continue
		}
		// 兜底也需要至少两轮历史，单条短消息（如"继续"）不触发。
		if len(sess.ContextHistory) < 2 {
			continue
		}
		sim := contextSimilarity(sess.ContextHistory, body.Messages)
		if sim > bestSimilarity {
			bestSimilarity = sim
			bestMatchID = id
		}
	}
	if bestMatchID != "" && bestSimilarity >= threshold {
		sess := sr.sessions[bestMatchID]
		sess.LastUsedAt = time.Now().UTC()
		sr.sessions[bestMatchID] = sess
		sr.persist.markDirty()
		return ResolveResult{
			SessionID:      sess.SessionID,
			ConversationID: sess.ConversationID,
			AccountID:      sess.AccountID,
			MatchedBy:      fmt.Sprintf("context_similar_%.2f", bestSimilarity),
			IsNew:          false,
		}
	}

	return ResolveResult{IsNew: true}
}

// matchContextLocked 浠庡叏閮ㄤ細璇濅腑鎵惧埌鍏?contextHistory 涓ユ牸浣滀负娑堟伅鍓嶇紑鐨?
// 閭ｄ釜浼氳瘽锛涘彧閫夊墠缂€鏈€闀跨殑涓€涓紝閬垮厤鐭墠缂€鍦ㄤ笉鍚屼細璇濋棿浜掓挒銆傝繑鍥?
// (sessionID, 鍖归厤鍒扮殑娑堟伅鏉℃暟)銆?
func (sr *sessionResolver) matchContextLocked(ipFinger string, messages []oaiMsg) (string, int) {
	if len(messages) == 0 {
		return "", 0
	}
	bestID := ""
	bestN := 0
	for id, sess := range sr.sessions {
		if time.Since(sess.LastUsedAt) > sr.contextTTL {
			continue
		}
		if sess.IPFingerprint != ipFinger {
			continue
		}
		n := contextPrefixLen(sess.ContextHistory, messages)
		// 门槛：至少 2 条消息的前缀才算复用，避免不同用户以同一条
		// 短消息（如"继续"）互相串会话。
		if n > 1 && n > bestN {
			bestN = n
			bestID = id
		}
	}
	return bestID, bestN
}

// contextPrefixLen 杩斿洖 hist 鏄惁涓ユ牸鏄?msgs 鐨勫墠缂€銆俬ist 涓虹┖鎴栦笉鏄墠缂€
// 鏃惰繑鍥?0锛涘懡涓椂杩斿洖 len(hist)锛屽嵆澧為噺鍙戦€佽捣鐐广€?
func contextPrefixLen(hist, msgs []oaiMsg) int {
	if len(hist) == 0 || len(msgs) < len(hist) {
		return 0
	}
	for i := range hist {
		if !messagesEqual(hist[i], msgs[i]) {
			return 0
		}
	}
	return len(hist)
}

// messagesEqual 鍒ゅ畾涓ゆ潯娑堟伅鍦ㄤ細璇濋敭鎰忎箟涓婄瓑浠凤細role 涓庢枃鏈唴瀹逛竴鑷淬€?
// 蹇界暐 tool_calls 鐨?ID 缁嗚妭锛堜細璇濋敭鍙叧蹇冨唴瀹瑰浣曡妯″瀷娑堝寲锛夈€?
func messagesEqual(a, b oaiMsg) bool {
	if !strings.EqualFold(strings.TrimSpace(a.Role), strings.TrimSpace(b.Role)) {
		return false
	}
	ta := contentToString(a.Content)
	tb := contentToString(b.Content)
	toolOnly := len(a.ToolCalls) > 0 && len(b.ToolCalls) > 0 && emptyMessageContent(a.Content) && emptyMessageContent(b.Content)
	if !toolOnly && ta != tb {
		return false
	}
	if len(a.ToolCalls) != len(b.ToolCalls) {
		return false
	}
	for i := range a.ToolCalls {
		if toolCallEqual(a.ToolCalls[i], b.ToolCalls[i]) {
			continue
		}
		return false
	}
	return true
}

// toolCallEqual 比较 name 与 arguments，忽略 ID：同一段工具调用重放时
// ID 由客户端重新生成，不应影响会话键。
func toolCallEqual(x, y map[string]any) bool {
	xFunc, _ := x["function"].(map[string]any)
	yFunc, _ := y["function"].(map[string]any)
	xn, _ := xFunc["name"].(string)
	yn, _ := yFunc["name"].(string)
	if xn != yn {
		return false
	}
	xa, _ := json.Marshal(canonicalToolArgumentsValue(xFunc["arguments"]))
	ya, _ := json.Marshal(canonicalToolArgumentsValue(yFunc["arguments"]))
	return string(xa) == string(ya)
}

func (sr *sessionResolver) Bind(sessionID, conversationID, accountID string, body *oaiReq, r *http.Request) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.evictLocked()

	now := time.Now().UTC()
	explicitID := r.Header.Get("X-M365-Session-Id")
	if explicitID != "" && sessionID == "" {
		sessionID = explicitID
	}
	// 同一云端对话只保留一条记录：内容键命中后增量轮次更新已存在会话，
	// 而不是每次 Bind 都新建一条，避免 sessions.json 膨胀。
	if sessionID != "" {
		if sess, ok := sr.sessions[sessionID]; ok {
			sess.ConversationID = conversationID
			sess.AccountID = accountID
			sess.LastUsedAt = now
			sess.UserField = body.User
			sess.IPFingerprint = clientIPFingerprint(r)
			sess.ContextFinger = contextFingerprint(body.Messages)
			sess.ContextHistory = mergeMessageHistory(sess.ContextHistory, body.Messages)
			sr.sessions[sessionID] = sess
			sr.reindexLocked(sess)
			sr.persist.markDirty()
			return
		}
	}
	if sessionID == "" {
		for sid, sess := range sr.sessions {
			if sess.ConversationID == conversationID {
				sess.LastUsedAt = now
				sess.AccountID = accountID
				sess.UserField = body.User
				sess.IPFingerprint = clientIPFingerprint(r)
				sess.ContextFinger = contextFingerprint(body.Messages)
				sess.ContextHistory = mergeMessageHistory(sess.ContextHistory, body.Messages)
				sr.sessions[sid] = sess
				sr.reindexLocked(sess)
				sr.persist.markDirty()
				return
			}
		}
		sessionID = uuid.NewString()
	}

	sess := sessionBinding{
		SessionID:      sessionID,
		ConversationID: conversationID,
		AccountID:      accountID,
		CreatedAt:      now,
		LastUsedAt:     now,
		IPFingerprint:  clientIPFingerprint(r),
		UserField:      body.User,
		ContextFinger:  contextFingerprint(body.Messages),
		ContextHistory: cloneMessages(body.Messages),
	}

	sr.reindexLocked(sess)
	sr.persist.markDirty()
}

func (sr *sessionResolver) GetSession(sessionID string) (sessionBinding, bool) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	s, ok := sr.sessions[sessionID]
	return s, ok
}

func (sr *sessionResolver) GetConversation(conversationID string) (sessionBinding, bool) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	for _, session := range sr.sessions {
		if session.ConversationID == conversationID {
			session.ContextHistory = cloneMessages(session.ContextHistory)
			return session, true
		}
	}
	return sessionBinding{}, false
}

func (sr *sessionResolver) ListSessions() []sessionBinding {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	out := make([]sessionBinding, 0, len(sr.sessions))
	for _, s := range sr.sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastUsedAt.After(out[j].LastUsedAt)
	})
	return out
}

func (sr *sessionResolver) DeleteSession(sessionID string) bool {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	s, ok := sr.sessions[sessionID]
	if !ok {
		return false
	}
	delete(sr.sessions, sessionID)
	delete(sr.byExplicit, sessionID)
	if s.UserField != "" {
		delete(sr.byUserField, s.UserField)
	}
	if s.IPFingerprint != "" {
		delete(sr.byIPFinger, s.IPFingerprint)
	}
	if s.ContextFinger != "" {
		delete(sr.byContext, s.ContextFinger)
	}
	sr.persist.markDirty()
	return true
}

// UnbindByConversation drops every session bound to the given conversation.
// Called after an automatic cleanup deletes the cloud conversation, so the
// anti-CrossID resolver never reuses a dead conversation.
func (sr *sessionResolver) UnbindByConversation(conversationID string) int {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	removed := 0
	for sid, s := range sr.sessions {
		if s.ConversationID != conversationID {
			continue
		}
		delete(sr.sessions, sid)
		delete(sr.byExplicit, sid)
		if s.UserField != "" {
			delete(sr.byUserField, s.UserField)
		}
		if s.IPFingerprint != "" {
			delete(sr.byIPFinger, s.IPFingerprint)
		}
		if s.ContextFinger != "" {
			delete(sr.byContext, s.ContextFinger)
		}
		removed++
	}
	if removed > 0 {
		sr.persist.markDirty()
	}
	return removed
}

func cloneMessages(msgs []oaiMsg) []oaiMsg {
	out := make([]oaiMsg, len(msgs))
	copy(out, msgs)
	return out
}

func mergeMessageHistory(existing, incoming []oaiMsg) []oaiMsg {
	if len(existing) == 0 {
		return cloneMessages(incoming)
	}
	if len(incoming) == 0 {
		return cloneMessages(existing)
	}

	maxOverlap := len(existing)
	if len(incoming) < maxOverlap {
		maxOverlap = len(incoming)
	}
	for overlap := maxOverlap; overlap > 0; overlap-- {
		matched := true
		for i := 0; i < overlap; i++ {
			if !messagesEqual(existing[len(existing)-overlap+i], incoming[i]) {
				matched = false
				break
			}
		}
		if matched {
			merged := cloneMessages(existing)
			return append(merged, cloneMessages(incoming[overlap:])...)
		}
	}

	merged := cloneMessages(existing)
	return append(merged, cloneMessages(incoming)...)
}
