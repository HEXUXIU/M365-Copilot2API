package web

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUsageSnapshotAggregatesKeyIDWithLegacyFallback(t *testing.T) {
	t.Setenv("M365_USAGE_LOG", filepath.Join(t.TempDir(), "usage.jsonl"))
	log := openUsageLog()

	now := time.Now()
	log.record(UsageRecord{Time: now, APIKeyID: "key-1", APIKeyPrefix: "m365_aaa...", Model: "m", InputTokens: 10, OutputTokens: 5})
	log.record(UsageRecord{Time: now, APIKeyID: "key-1", APIKeyPrefix: "m365_bbb...", Model: "m", InputTokens: 20, OutputTokens: 5})
	log.record(UsageRecord{Time: now, APIKeyPrefix: "m365_leg...", Model: "m", InputTokens: 1, OutputTokens: 1})

	stats := log.snapshot(7)
	keys, ok := stats["keys"].([]map[string]any)
	if !ok {
		t.Fatalf("snapshot keys has type %T, want []map[string]any", stats["keys"])
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 key buckets, got %d: %v", len(keys), keys)
	}

	byID := map[string]map[string]any{}
	for _, row := range keys {
		id, _ := row["api_key_id"].(string)
		byID[id] = row
	}
	idRow, ok := byID["key-1"]
	if !ok {
		t.Fatalf("missing key-1 bucket: %v", keys)
	}
	if idRow["requests"] != int64(2) || idRow["tokens"] != int64(40) {
		t.Fatalf("key-1 bucket=%v, want requests=2 tokens=40", idRow)
	}
	if idRow["api_key_prefix"] != "m365_aaa..." {
		t.Fatalf("key-1 prefix=%v, want first-seen m365_aaa...", idRow["api_key_prefix"])
	}

	legacyRow, ok := byID[""]
	if !ok {
		t.Fatalf("missing legacy bucket: %v", keys)
	}
	if legacyRow["requests"] != int64(1) || legacyRow["api_key_prefix"] != "m365_leg..." {
		t.Fatalf("legacy bucket=%v", legacyRow)
	}
}

func TestUsageKeySnapshotFiltersByIDAndLegacyPrefix(t *testing.T) {
	t.Setenv("M365_USAGE_LOG", filepath.Join(t.TempDir(), "usage.jsonl"))
	log := openUsageLog()

	now := time.Now()
	yesterday := now.Add(-26 * time.Hour)
	log.record(UsageRecord{Time: yesterday, APIKeyID: "key-a", APIKeyPrefix: "m365_aaa...", Model: "gpt-4o", Endpoint: "/v1/chat/completions", InputTokens: 100, OutputTokens: 50})
	log.record(UsageRecord{Time: now, APIKeyID: "key-a", APIKeyPrefix: "m365_aaa...", Model: "gpt-4o-mini", Endpoint: "/v1/messages", InputTokens: 10, OutputTokens: 5})
	log.record(UsageRecord{Time: now, APIKeyID: "key-b", APIKeyPrefix: "m365_bbb...", Model: "gpt-4o", InputTokens: 7})
	// 升级前的旧记录（无 ID）：按 8 字符前缀归入 key-a（key-a 的 12 字符前缀为 m365_aaa12345）。
	log.record(UsageRecord{Time: now, APIKeyPrefix: "m365_aaa...", Model: "gpt-4o", Endpoint: "/v1/chat/completions", InputTokens: 1, OutputTokens: 1})
	// 前缀属于其他 key / JWT 的旧记录：不得计入 key-a。
	log.record(UsageRecord{Time: now, APIKeyPrefix: "m365_zzz...", Model: "gpt-4o", InputTokens: 3})
	log.record(UsageRecord{Time: now, APIKeyPrefix: "eyJhbGci...", Model: "gpt-4o", InputTokens: 4})

	stats := log.keySnapshot(7, "key-a", "m365_aaa12345")
	summary, ok := stats["summary"].(map[string]any)
	if !ok {
		t.Fatalf("keySnapshot summary has type %T", stats["summary"])
	}
	if summary["requests"] != int64(3) || summary["tokens"] != int64(167) {
		t.Fatalf("key-a summary=%v, want requests=3 tokens=167", summary)
	}
	models, ok := stats["models"].([]map[string]any)
	if !ok || len(models) != 2 {
		t.Fatalf("key-a models=%v, want 2 entries", stats["models"])
	}
	endpoints, ok := stats["endpoints"].([]map[string]any)
	if !ok || len(endpoints) != 2 {
		t.Fatalf("key-a endpoints=%v, want 2 entries", stats["endpoints"])
	}
	trend, ok := stats["trend"].([]map[string]any)
	if !ok || len(trend) != 2 {
		t.Fatalf("key-a trend=%v, want 2 days", stats["trend"])
	}

	// 其他 key 只统计自己的记录（无旧前缀回退）。
	statsB := log.keySnapshot(7, "key-b", "m365_bbb12345")
	summaryB, _ := statsB["summary"].(map[string]any)
	if summaryB["requests"] != int64(1) {
		t.Fatalf("key-b summary=%v, want requests=1", summaryB)
	}
}
