package web

import "strings"

// toolPlanningMode selects how the gateway turns a model response into tool
// calls. "native" forwards the caller's tool definitions to ChatHub as plugins
// and reads the tool invocations back from the upstream event stream — the
// official path, with no second model call. "router" is the legacy fallback
// that asks the model to emit a JSON routing decision in text; it is opt-in
// only (M365_TOOL_PLANNING_MODE=router) because it doubles latency and is
// fragile.
func toolPlanningMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "router") {
		return "router"
	}
	return "native"
}
