package web

import (
	"net/http"
	"sort"
	"time"
)

// slaMetrics is the SLA snapshot exposed at /api/metrics. It is computed from
// the same usage log the console already reads, so no extra instrumentation is
// needed to answer "how is the gateway doing" with real numbers.
type slaMetrics struct {
	GeneratedAt   string         `json:"generated_at"`
	UptimeSeconds int64          `json:"uptime_seconds"`
	Requests      int64          `json:"requests"`
	Errors        int64          `json:"errors"`
	SuccessRate   float64        `json:"success_rate"`
	LatencyMs     latencyStats   `json:"latency_ms"`
	PerEndpoint   []endpointStat `json:"per_endpoint"`
	Accounts      accountSummary `json:"accounts"`
}

type latencyStats struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	P99 int64 `json:"p99"`
	Max int64 `json:"max"`
}

type endpointStat struct {
	Endpoint    string  `json:"endpoint"`
	Requests    int64   `json:"requests"`
	Errors      int64   `json:"errors"`
	SuccessRate float64 `json:"success_rate"`
	P50Ms       int64   `json:"p50_ms"`
	P95Ms       int64   `json:"p95_ms"`
	P99Ms       int64   `json:"p99_ms"`

	Durations []int64 `json:"-"`
}

type accountSummary struct {
	Total    int `json:"total"`
	Online   int `json:"online"`
	Cooldown int `json:"cooldown"`
	Expired  int `json:"expired"`
}

var gatewayStarted = time.Now()

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

func summarizeLatency(durations []int64) latencyStats {
	if len(durations) == 0 {
		return latencyStats{}
	}
	sorted := append([]int64(nil), durations...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return latencyStats{
		P50: percentile(sorted, 0.50),
		P95: percentile(sorted, 0.95),
		P99: percentile(sorted, 0.99),
		Max: sorted[len(sorted)-1],
	}
}

func (s *Server) slaMetrics() slaMetrics {
	// The usage log keeps the most recent records; that window is what an SLA
	// snapshot should describe rather than the whole process lifetime.
	raw := s.usage.logs(maxUsageRecords, 0)
	recs, _ := raw["logs"].([]UsageRecord)

	var total, errors int64
	all := make([]int64, 0, len(recs))
	byEndpoint := map[string]*endpointStat{}
	for _, r := range recs {
		total++
		if r.Status >= 400 {
			errors++
		}
		all = append(all, r.DurationMs)
		st := byEndpoint[r.Endpoint]
		if st == nil {
			st = &endpointStat{Endpoint: r.Endpoint}
			byEndpoint[r.Endpoint] = st
		}
		st.Requests++
		if r.Status >= 400 {
			st.Errors++
		}
		st.Durations = append(st.Durations, r.DurationMs)
	}

	per := make([]endpointStat, 0, len(byEndpoint))
	for _, st := range byEndpoint {
		if st.Requests > 0 {
			st.SuccessRate = float64(st.Requests-st.Errors) / float64(st.Requests)
		}
		lat := summarizeLatency(st.Durations)
		st.P50Ms, st.P95Ms, st.P99Ms = lat.P50, lat.P95, lat.P99
		st.Durations = nil
		per = append(per, *st)
	}
	sort.Slice(per, func(i, j int) bool { return per[i].Requests > per[j].Requests })

	var successRate float64
	if total > 0 {
		successRate = float64(total-errors) / float64(total)
	}

	return slaMetrics{
		GeneratedAt:   time.Now().Format(time.RFC3339),
		UptimeSeconds: int64(time.Since(gatewayStarted).Seconds()),
		Requests:      total,
		Errors:        errors,
		SuccessRate:   successRate,
		LatencyMs:     summarizeLatency(all),
		PerEndpoint:   per,
		Accounts:      s.accountSummary(),
	}
}

func (s *Server) accountSummary() accountSummary {
	out := accountSummary{}
	for _, a := range s.tokens.List() {
		out.Total++
		switch a.Status {
		case "online":
			out.Online++
		case "cooldown":
			out.Cooldown++
		case "expired":
			out.Expired++
		}
	}
	return out
}

func (s *Server) adminMetrics(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, s.slaMetrics())
}
