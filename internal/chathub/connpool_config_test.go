package chathub

import (
	"net/http"
	"testing"
)

func TestConnPoolConfiguration(t *testing.T) {
	t.Setenv("M365_WS_POOL_SIZE", "7")
	t.Setenv("M365_WS_POOL_TTL_SECONDS", "300")
	p := NewConnPool(nil, http.Header{})
	defer p.Close()
	if got := p.Capacity(); got != 7 {
		t.Fatalf("pool capacity = %d, want 7", got)
	}
	stats := p.Stats()
	if got := stats["max_per_key"]; got != 7 {
		t.Fatalf("stats max_per_key = %v, want 7", got)
	}
	if got := stats["ttl_seconds"]; got != 300 {
		t.Fatalf("stats ttl_seconds = %v, want 300", got)
	}
}

func TestConnPoolConfigurationBoundsInvalidValues(t *testing.T) {
	t.Setenv("M365_WS_POOL_SIZE", "999")
	t.Setenv("M365_WS_POOL_TTL_SECONDS", "10")
	p := NewConnPool(nil, http.Header{})
	defer p.Close()
	if got := p.Capacity(); got != maxPoolPerKeyLimit {
		t.Fatalf("pool capacity = %d, want %d", got, maxPoolPerKeyLimit)
	}
	if got := p.Stats()["ttl_seconds"]; got != int(defaultPoolConnTTL.Seconds()) {
		t.Fatalf("stats ttl_seconds = %v, want %d", got, int(defaultPoolConnTTL.Seconds()))
	}
}

func TestNewClientInitializesConnectionPool(t *testing.T) {
	c := NewClient()
	if c.Pool == nil {
		t.Fatal("NewClient connection pool is nil")
	}
	c.Pool.Close()
}
