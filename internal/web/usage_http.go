package web

import (
	"net/http"
	"strconv"
)

func (s *Server) adminUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}
	stats := s.usage.snapshot(days)
	// snapshot 已释放 usageLog 锁，这里再进 apiKeyStore 解析名称，两把锁不嵌套。
	if rows, ok := stats["keys"].([]map[string]any); ok {
		for _, row := range rows {
			id, _ := row["api_key_id"].(string)
			prefix, _ := row["api_key_prefix"].(string)
			if name := s.apiKeyName(id, prefix); name != "" {
				row["api_key_name"] = name
			}
		}
	}
	jsonOut(w, map[string]any{
		"days":  days,
		"stats": stats,
	})
}

// usageLogRow 在原始记录上附带读取时解析的 key 名称（改名即时生效）。
type usageLogRow struct {
	UsageRecord
	APIKeyName string `json:"api_key_name,omitempty"`
}

func (s *Server) adminUsageLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 50
	offset := 0
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 2000 {
			limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	res := s.usage.logs(limit, offset)
	if recs, ok := res["logs"].([]UsageRecord); ok {
		rows := make([]usageLogRow, 0, len(recs))
		for _, rec := range recs {
			rows = append(rows, usageLogRow{UsageRecord: rec, APIKeyName: s.apiKeyName(rec.APIKeyID, rec.APIKeyPrefix)})
		}
		res["logs"] = rows
	}
	jsonOut(w, res)
}
