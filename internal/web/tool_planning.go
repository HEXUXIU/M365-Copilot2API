package web

import "strings"

// toolPlanningMode selects how the gateway turns a model response into tool
// calls. "router" is the default (per report 09, M365 rejects inline API
// plugins so "native" produces zero tool calls unless an external reachable
// plugin/MCP endpoint is used). Set M365_TOOL_PLANNING_MODE=native to opt in
// to native mode explicitly.
func toolPlanningMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "native") {
		return "native"
	}
	return "router"
}
