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
