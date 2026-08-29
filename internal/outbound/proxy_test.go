package outbound

import (
	"errors"
	"testing"
	"time"
)

func TestRemoveProxyNormalizesAndRejectsMissing(t *testing.T) {
	if err := ConfigurePool([]string{"http://example.com/"}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveProxy("http://example.com"); err != nil {
		t.Fatal(err)
	}
	if len(ProxyPoolStatus()) != 0 {
		t.Fatalf("pool not empty: %#v", ProxyPoolStatus())
	}
	if err := RemoveProxy("http://missing.example"); err == nil {
		t.Fatal("expected missing proxy error")
	}
}

func TestProxyCooldownStatusIncludesReasonTimeAndScope(t *testing.T) {
	p, err := NewPool([]string{"http://proxy.example:8080"})
	if err != nil {
		t.Fatal(err)
	}
	p.MarkProxyFailure("http://proxy.example:8080", errors.New("connection refused"))
	status := p.List()
	if len(status) != 1 {
		t.Fatalf("status=%#v", status)
	}
	entry := status[0]
	if entry["cooldownScope"] != "proxy" || entry["cooldownReason"] != "TCP" {
		t.Fatalf("missing proxy cooldown classification: %#v", entry)
	}
	triggeredAt, ok := entry["cooldownTriggeredAt"].(time.Time)
	if !ok || triggeredAt.IsZero() {
		t.Fatalf("missing proxy cooldown trigger time: %#v", entry)
	}
	until, ok := entry["cooldownUntil"].(time.Time)
	if !ok || !until.After(triggeredAt) {
		t.Fatalf("invalid proxy cooldown recovery time: %#v", entry)
	}
}
