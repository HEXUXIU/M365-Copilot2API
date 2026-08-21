package web

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type UsageRecord struct {
	Time         time.Time `json:"time"`
	APIKeyID     string    `json:"api_key_id,omitempty"`
	APIKeyPrefix string    `json:"api_key_prefix"`
	AccountEmail string    `json:"account_email"`
	Model        string    `json:"model"`
	Endpoint     string    `json:"endpoint"`
	Stream       bool      `json:"stream"`
	InputTokens  int64     `json:"input_tokens"`
	OutputTokens int64     `json:"output_tokens"`
	CacheTokens  int64     `json:"cache_tokens"`
	DurationMs   int64     `json:"duration_ms"`
	Status       int       `json:"status"`
}

const maxUsageRecords = 50000

type usageLog struct {
	mu      sync.Mutex
	Path    string
	records []UsageRecord
	pending []UsageRecord
	persist *persistStore
}

var globalUsage = &usageLog{}

func openUsageLog() *usageLog {
	p := strings.TrimSpace(os.Getenv("M365_USAGE_LOG"))
	if p == "" {
		dir := strings.TrimSpace(os.Getenv("M365_DATA_DIR"))
		if dir == "" {
			h, _ := os.UserHomeDir()
			dir = filepath.Join(h, ".config", "m365-copilot2api")
		}
		p = filepath.Join(dir, "usage.jsonl")
	}
	s := &usageLog{Path: p}
	s.persist = &persistStore{flush: s.flush}
	_ = os.MkdirAll(filepath.Dir(p), 0700)
	s.load()
	return s
}

func (s *usageLog) load() {
	f, err := os.Open(s.Path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var rec UsageRecord
		if json.Unmarshal(scanner.Bytes(), &rec) == nil {
			s.records = append(s.records, rec)
		}
	}
	s.trim()
}

func (s *usageLog) trim() {
	if len(s.records) > maxUsageRecords {
		s.records = s.records[len(s.records)-maxUsageRecords:]
	}
}

func (s *usageLog) record(rec UsageRecord) {
	s.mu.Lock()
	s.records = append(s.records, rec)
	s.trim()
	s.pending = append(s.pending, rec)
	s.mu.Unlock()
	s.persist.markDirty()
}

// flush 批量追加本次累积的记录，锁外写盘。
func (s *usageLog) flush() error {
	s.mu.Lock()
	pending := s.pending
	s.pending = nil
	s.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}
	var buf []byte
	for _, rec := range pending {
		if b, err := json.Marshal(rec); err == nil {
			buf = append(buf, b...)
			buf = append(buf, '\n')
		}
	}
	f, err := os.OpenFile(s.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(buf)
	if err != nil {
		s.mu.Lock()
		s.pending = append(pending, s.pending...)
		s.mu.Unlock()
		return err
	}
	return f.Sync()
}

func (s *usageLog) snapshot(days int) map[string]any {
	s.mu.Lock()
	recs := append([]UsageRecord(nil), s.records...)
	s.mu.Unlock()

	agg := newUsageAgg(days)
	keyCounts := map[string]*usageKeyStat{}
	for _, rec := range recs {
		if rec.Time.Before(agg.cutoff) {
			continue
		}
		agg.add(rec)
		// 按 key ID 聚合；升级前的旧记录没有 ID，按截断前缀独立成桶。
		key := rec.APIKeyID
		if key == "" {
			key = "prefix:" + rec.APIKeyPrefix
		}
		ks, ok := keyCounts[key]
		if !ok {
			ks = &usageKeyStat{prefix: rec.APIKeyPrefix, id: rec.APIKeyID}
			keyCounts[key] = ks
		}
		ks.Requests++
		ks.Tokens += rec.InputTokens + rec.OutputTokens + rec.CacheTokens
	}

	keys := make([]map[string]any, 0, len(keyCounts))
	for _, c := range keyCounts {
		keys = append(keys, map[string]any{"api_key_id": c.id, "api_key_prefix": c.prefix, "requests": c.Requests, "tokens": c.Tokens})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i]["requests"].(int64) > keys[j]["requests"].(int64) })

	stats := agg.result()
	stats["keys"] = keys
	return stats
}

// keySnapshot 返回单个 key 的用量明细。锁内只筛选拷贝该 key 的记录子集（避免全量
// 拷贝 50k 记录的分配开销），聚合在锁外进行。升级前无 ID 的旧记录按 8 字符前缀归入
// 该 key（与 resolveName 的回退口径一致；JWT eyJ 前缀不会误匹配）。
func (s *usageLog) keySnapshot(days int, keyID, keyPrefix string) map[string]any {
	if keyID == "" {
		return newUsageAgg(days).result()
	}
	var legacyBase string
	if len(keyPrefix) >= 8 {
		legacyBase = keyPrefix[:8]
	}
	s.mu.Lock()
	var recs []UsageRecord
	for _, rec := range s.records {
		if rec.APIKeyID == keyID {
			recs = append(recs, rec)
			continue
		}
		if rec.APIKeyID == "" && legacyBase != "" &&
			len(rec.APIKeyPrefix) == 11 && strings.HasSuffix(rec.APIKeyPrefix, "...") &&
			strings.HasPrefix(rec.APIKeyPrefix, legacyBase) {
			recs = append(recs, rec)
		}
	}
	s.mu.Unlock()

	agg := newUsageAgg(days)
	for _, rec := range recs {
		if rec.Time.Before(agg.cutoff) {
			continue
		}
		agg.add(rec)
	}
	return agg.result()
}

func (s *usageLog) logs(limit, offset int) map[string]any {
	s.mu.Lock()
	recs := append([]UsageRecord(nil), s.records...)
	s.mu.Unlock()

	total := len(recs)
	if offset > total {
		offset = total
	}
	start := total - offset - limit
	if start < 0 {
		start = 0
	}
	end := total - offset
	if end < 0 {
		end = 0
	}
	if start >= end {
		return map[string]any{"logs": []UsageRecord{}, "total": total}
	}
	out := make([]UsageRecord, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, recs[i])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return map[string]any{"logs": out, "total": total}
}

type usageCountStat struct {
	Requests int64
	Tokens   int64
}

// usageKeyStat 是按 key 维度聚合的桶：id 为空表示旧版前缀桶。
type usageKeyStat struct {
	id       string
	prefix   string
	Requests int64
	Tokens   int64
}

type usageTrendPoint struct {
	Date     string `json:"date"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

// usageAgg 聚合一段记录的 summary/models/endpoints/trend 维度（不含 keys 维度）。
// cutoff 之前的记录由调用方跳过；keys 维度只有全量快照需要，留在 snapshot 内。
type usageAgg struct {
	cutoff     time.Time
	today      time.Time
	dayAgo     time.Time
	loc        *time.Location
	requests   int64
	in         int64
	out        int64
	cache      int64
	durationMs int64
	todayReq   int64
	todayTok   int64
	h24Req     int64
	h24Tok     int64
	models     map[string]*usageCountStat
	endpoints  map[string]*usageCountStat
	trendMap   map[string]*usageTrendPoint
}

func newUsageAgg(days int) *usageAgg {
	now := time.Now()
	return &usageAgg{
		cutoff:    now.AddDate(0, 0, -days),
		loc:       now.Location(),
		today:     now.In(now.Location()).Truncate(24 * time.Hour),
		dayAgo:    now.Add(-24 * time.Hour),
		models:    map[string]*usageCountStat{},
		endpoints: map[string]*usageCountStat{},
		trendMap:  map[string]*usageTrendPoint{},
	}
}

func (a *usageAgg) add(rec UsageRecord) {
	a.requests++
	reqTok := rec.InputTokens + rec.OutputTokens + rec.CacheTokens
	a.in += rec.InputTokens
	a.out += rec.OutputTokens
	a.cache += rec.CacheTokens
	a.durationMs += rec.DurationMs
	if rec.Time.After(a.today) {
		a.todayReq++
		a.todayTok += reqTok
	}
	if rec.Time.After(a.dayAgo) {
		a.h24Req++
		a.h24Tok += reqTok
	}
	if mc, ok := a.models[rec.Model]; ok {
		mc.Requests++
		mc.Tokens += reqTok
	} else {
		a.models[rec.Model] = &usageCountStat{Requests: 1, Tokens: reqTok}
	}
	if ec, ok := a.endpoints[rec.Endpoint]; ok {
		ec.Requests++
		ec.Tokens += reqTok
	} else {
		a.endpoints[rec.Endpoint] = &usageCountStat{Requests: 1, Tokens: reqTok}
	}
	date := rec.Time.In(a.loc).Format("01-02")
	if tp, ok := a.trendMap[date]; ok {
		tp.Requests++
		tp.Tokens += reqTok
	} else {
		a.trendMap[date] = &usageTrendPoint{Date: date, Requests: 1, Tokens: reqTok}
	}
}

func (a *usageAgg) result() map[string]any {
	avgMs := int64(0)
	if a.requests > 0 {
		avgMs = a.durationMs / a.requests
	}

	model := make([]map[string]any, 0, len(a.models))
	for name, c := range a.models {
		model = append(model, map[string]any{"name": name, "requests": c.Requests, "tokens": c.Tokens})
	}
	sort.Slice(model, func(i, j int) bool { return model[i]["tokens"].(int64) > model[j]["tokens"].(int64) })

	ep := make([]map[string]any, 0, len(a.endpoints))
	for k, c := range a.endpoints {
		ep = append(ep, map[string]any{"endpoint": k, "requests": c.Requests, "tokens": c.Tokens})
	}
	sort.Slice(ep, func(i, j int) bool { return ep[i]["tokens"].(int64) > ep[j]["tokens"].(int64) })

	trend := make([]map[string]any, 0, len(a.trendMap))
	for _, t := range a.trendMap {
		trend = append(trend, map[string]any{"date": t.Date, "requests": t.Requests, "tokens": t.Tokens})
	}
	sort.Slice(trend, func(i, j int) bool { return trend[i]["date"].(string) < trend[j]["date"].(string) })

	return map[string]any{
		"summary": map[string]any{
			"requests":         a.requests,
			"tokens":           a.in + a.out + a.cache,
			"input":            a.in,
			"output":           a.out,
			"cache":            a.cache,
			"avg_ms":           avgMs,
			"today_requests":   a.todayReq,
			"today_tokens":     a.todayTok,
			"last24h_requests": a.h24Req,
			"last24h_tokens":   a.h24Tok,
		},
		"models":    model,
		"endpoints": ep,
		"trend":     trend,
	}
}
