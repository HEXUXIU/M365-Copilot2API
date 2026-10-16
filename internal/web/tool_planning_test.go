package web

import "testing"

func TestToolPlanningModeDefaultsToRouter(t *testing.T) {
	for _, raw := range []string{"", "router", "ROUTER", "unexpected"} {
		if got := toolPlanningMode(raw); got != "router" {
			t.Fatalf("toolPlanningMode(%q)=%q, want router", raw, got)
		}
	}
}

func TestToolPlanningModeAcceptsNativeOptIn(t *testing.T) {
	if got := toolPlanningMode(" native "); got != "native" {
		t.Fatalf("toolPlanningMode(native)=%q, want native", got)
	}
}
