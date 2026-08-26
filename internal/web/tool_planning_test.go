package web

import "testing"

func TestToolPlanningModeDefaultsToRouter(t *testing.T) {
	for _, raw := range []string{"", "router", "ROUTER", "unexpected"} {
		if got := toolPlanningMode(raw); got != "router" {
			t.Fatalf("toolPlanningMode(%q)=%q, want router", raw, got)
		}
	}
}

func TestToolPlanningModeAcceptsNative(t *testing.T) {
	if got := toolPlanningMode(" native "); got != "native" {
		t.Fatalf("toolPlanningMode(native)=%q, want native", got)
	}
}

func TestToolProtocolModeDefaultsToLegacy(t *testing.T) {
	for _, raw := range []string{"", "unknown", " legacy "} {
		if got := toolProtocolMode(raw); got != "legacy" {
			t.Fatalf("toolProtocolMode(%q)=%q, want legacy", raw, got)
		}
	}
}

func TestToolProtocolModeAcceptsPiCompat(t *testing.T) {
	if got := toolProtocolMode(" PI_COMPAT "); got != "pi_compat" {
		t.Fatalf("toolProtocolMode(pi_compat)=%q", got)
	}
}
