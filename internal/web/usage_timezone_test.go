package web

import (
	"testing"
	"time"
)

// Kolkata is UTC+05:30, a half-hour offset zone. time.Truncate rounds against
// the Unix epoch, so it lands on the wrong clock boundary there.
func halfHourZone(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}

func TestTrendBucketerAlignsHourlyBucketsToLocalHour(t *testing.T) {
	loc := halfHourZone(t)
	bucket := trendBucketer("15:04", time.Hour, loc)

	// 09:47 local is 04:17 UTC; truncating to the hour yields 04:00 UTC, which
	// is 09:30 local and would be mislabelled as 09:00.
	got := bucket(time.Date(2026, 3, 4, 9, 47, 12, 0, loc))
	if got != "09:00" {
		t.Fatalf("expected bucket label 09:00, got %q", got)
	}
}

func TestTrendBucketerSeparatesDistinctLocalHours(t *testing.T) {
	loc := halfHourZone(t)
	bucket := trendBucketer("15:04", time.Hour, loc)

	before := bucket(time.Date(2026, 3, 4, 9, 58, 0, 0, loc))
	after := bucket(time.Date(2026, 3, 4, 10, 3, 0, 0, loc))
	if before != "09:00" || after != "10:00" {
		t.Fatalf("expected 09:00 then 10:00, got %q then %q", before, after)
	}
}

func TestTrendBucketerMinuteGranularity(t *testing.T) {
	loc := halfHourZone(t)
	bucket := trendBucketer("15:04", time.Minute, loc)

	if got := bucket(time.Date(2026, 3, 4, 9, 47, 12, 0, loc)); got != "09:47" {
		t.Fatalf("expected bucket label 09:47, got %q", got)
	}
}

func TestTrendGranularity(t *testing.T) {
	now := time.Now()
	t.Run("multi-day uses daily buckets", func(t *testing.T) {
		layout, width := trendGranularity(7, nil, now.AddDate(0, 0, -7))
		if layout != "01-02" || width != 0 {
			t.Fatalf("got layout=%q width=%v, want 01-02 and no truncation", layout, width)
		}
	})
	t.Run("24h range with a full hour of data uses hourly", func(t *testing.T) {
		recs := []UsageRecord{{Time: now.Add(-90 * time.Minute)}, {Time: now}}
		layout, width := trendGranularity(1, recs, now.AddDate(0, 0, -1))
		if layout != "15:04" || width != time.Hour {
			t.Fatalf("got layout=%q width=%v, want 15:04 and one hour", layout, width)
		}
	})
	t.Run("sub-hour span falls back to per-minute", func(t *testing.T) {
		recs := []UsageRecord{{Time: now.Add(-20 * time.Minute)}, {Time: now}}
		_, width := trendGranularity(1, recs, now.AddDate(0, 0, -1))
		if width != time.Minute {
			t.Fatalf("got width=%v, want one minute", width)
		}
	})
	t.Run("empty log does not panic", func(t *testing.T) {
		if _, width := trendGranularity(1, nil, now.AddDate(0, 0, -1)); width != time.Hour {
			t.Fatalf("got width=%v, want one hour", width)
		}
	})
}

func TestUsageSnapshotTodayUsesLocalMidnight(t *testing.T) {
	loc := halfHourZone(t)
	// snapshot() buckets against time.Local, so the zone has to be installed
	// rather than merely passed around.
	prev := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = prev })

	now := time.Now().In(loc)
	// 02:00 local sits between midnight and 05:30. Truncating to 24h against the
	// Unix epoch yields 05:30 local in this zone, which would wrongly exclude it.
	early := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, loc)

	u := &usageLog{records: []UsageRecord{
		{Time: early, Model: "m", Endpoint: "/e", InputTokens: 2, OutputTokens: 2},
	}}

	summary := u.snapshot(1)["summary"].(map[string]any)
	if got := summary["today_requests"]; got != int64(1) {
		t.Fatalf("a 02:00 local record must count as today in a half-hour zone, got %v", got)
	}
	if got := summary["today_tokens"]; got != int64(4) {
		t.Fatalf("expected 4 tokens today, got %v", got)
	}
}

func TestTransportCategoryExcludesUpstreamOverload(t *testing.T) {
	// An upstream 503 is a server-side capacity signal, not a local connectivity
	// fault, so it must not be reported to clients as network_error.
	if IsTransportCategory(CategoryOverload503) {
		t.Fatal("upstream overload must not be classified as a transport failure")
	}
	for _, cat := range []ErrorCategory{CategorySOCKS5, CategoryDNS, CategoryTCP, CategoryTLS, CategoryWSHandshake, CategoryWSReadTimeout} {
		if !IsTransportCategory(cat) {
			t.Fatalf("%v should be classified as a transport failure", cat)
		}
	}
	// Overload must still be eligible for failover.
	if !IsTransientTransport(&UpstreamHTTPError{Status: 503}) {
		t.Fatal("upstream overload must remain failover-retriable")
	}
}
