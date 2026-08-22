package web

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type UsageRecord struct {
	Time         time.Time `json:"time"`
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
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		log.Printf("[usage] MkdirAll failed: %v", err)
	}
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

	cutoff := time.Now().AddDate(0, 0, -days)
	loc := time.Now().Location()
	today := time.Now().In(loc).Truncate(24 * time.Hour)
	dayAgo := time.Now().Add(-24 * time.Hour)

	var (
		requests, in, out, cache, durationMs int64
		todayReq, todayTok                   int64
		h24Req, h24Tok                       int64
	)
	keyCounts := map[string]*usageCountStat{}
	modelCounts := map[string]*usageCountStat{}
	endpointCounts := map[string]*usageCountStat{}
	trendMap := map[string]*usageTrendPoint{}

	for _, rec := range recs {
		if rec.Time.Before(cutoff) {
			continue
		}
		requests++
		reqTok := rec.InputTokens + rec.OutputTokens + rec.CacheTokens
		in += rec.InputTokens
		out += rec.OutputTokens
		cache += rec.CacheTokens
		durationMs += rec.DurationMs
		if rec.Time.After(today) {
			todayReq++
			todayTok += reqTok
		}
		if rec.Time.After(dayAgo) {
			h24Req++
			h24Tok += reqTok
		}
		key := rec.APIKeyPrefix
		ks, ok := keyCounts[key]
		if !ok {
			ks = &usageCountStat{}
			keyCounts[key] = ks
		}
		ks.Requests++
		ks.Tokens += reqTok
		if mc, ok := modelCounts[rec.Model]; ok {
			mc.Requests++
			mc.Tokens += reqTok
		} else {
			modelCounts[rec.Model] = &usageCountStat{Requests: 1, Tokens: reqTok}
		}
		if ec, ok := endpointCounts[rec.Endpoint]; ok {
			ec.Requests++
			ec.Tokens += reqTok
		} else {
			endpointCounts[rec.Endpoint] = &usageCountStat{Requests: 1, Tokens: reqTok}
		}
		date := rec.Time.In(loc).Format("01-02")
		if tp, ok := trendMap[date]; ok {
			tp.Requests++
			tp.Tokens += reqTok
		} else {
			trendMap[date] = &usageTrendPoint{Date: date, Requests: 1, Tokens: reqTok}
		}
	}

	avgMs := int64(0)
	if requests > 0 {
		avgMs = durationMs / requests
	}

	model := make([]map[string]any, 0, len(modelCounts))
	for name, c := range modelCounts {
		model = append(model, map[string]any{"name": name, "requests": c.Requests, "tokens": c.Tokens})
	}
	sort.Slice(model, func(i, j int) bool { return model[i]["tokens"].(int64) > model[j]["tokens"].(int64) })

	ep := make([]map[string]any, 0, len(endpointCounts))
	for k, c := range endpointCounts {
		ep = append(ep, map[string]any{"endpoint": k, "requests": c.Requests, "tokens": c.Tokens})
	}
	sort.Slice(ep, func(i, j int) bool { return ep[i]["tokens"].(int64) > ep[j]["tokens"].(int64) })

	keys := make([]map[string]any, 0, len(keyCounts))
	for k, c := range keyCounts {
		keys = append(keys, map[string]any{"api_key_prefix": k, "requests": c.Requests, "tokens": c.Tokens})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i]["requests"].(int64) > keys[j]["requests"].(int64) })

	trend := make([]map[string]any, 0, len(trendMap))
	for _, t := range trendMap {
		trend = append(trend, map[string]any{"date": t.Date, "requests": t.Requests, "tokens": t.Tokens})
	}
	sort.Slice(trend, func(i, j int) bool { return trend[i]["date"].(string) < trend[j]["date"].(string) })

	return map[string]any{
		"summary": map[string]any{
			"requests":         requests,
			"tokens":           in + out + cache,
			"input":            in,
			"output":           out,
			"cache":            cache,
			"avg_ms":           avgMs,
			"today_requests":   todayReq,
			"today_tokens":     todayTok,
			"last24h_requests": h24Req,
			"last24h_tokens":   h24Tok,
		},
		"models":    model,
		"endpoints": ep,
		"keys":      keys,
		"trend":     trend,
	}
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

type usageTrendPoint struct {
	Date     string `json:"date"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
}
