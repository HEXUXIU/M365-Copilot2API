package web

import "testing"

func TestToolPlanningModeDefaultsToNative(t *testing.T) {
	for _, raw := range []string{"", "native", "NATIVE", "unexpected"} {
		if got := toolPlanningMode(raw); got != "native" {
			t.Fatalf("toolPlanningMode(%q)=%q, want native", raw, got)
		}
	}
}

func TestToolPlanningModeAcceptsRouterOptIn(t *testing.T) {
	if got := toolPlanningMode(" router "); got != "router" {
		t.Fatalf("toolPlanningMode(router)=%q, want router", got)
	}
}
