package web

import (
	"encoding/json"
	"testing"

	"m365-copilot2api/internal/chathub"
)

func TestEnsureWebSearchEnabledDefaultOn(t *testing.T) {
	t.Setenv("M365_ENABLE_WEB_SEARCH", "")
	body := &oaiReq{}
	ensureWebSearchEnabled(body)
	if len(body.Tools) != 1 || body.Tools[0].Type != "web_search" || body.Tools[0].Function != nil {
		t.Fatalf("expected exactly one injected web_search declaration, got %+v", body.Tools)
	}
}

func TestEnsureWebSearchEnabledNoDuplicateByType(t *testing.T) {
	t.Setenv("M365_ENABLE_WEB_SEARCH", "")
	body := &oaiReq{Tools: []chathub.Tool{{Type: "web_search"}}}
	ensureWebSearchEnabled(body)
	if len(body.Tools) != 1 {
		t.Fatalf("web_search must not be injected twice, got %+v", body.Tools)
	}
}

func TestEnsureWebSearchEnabledNoDuplicateByFunctionName(t *testing.T) {
	t.Setenv("M365_ENABLE_WEB_SEARCH", "")
	body := &oaiReq{Tools: []chathub.Tool{{Type: "function", Function: json.RawMessage(`{"name":"web_search"}`)}}}
	ensureWebSearchEnabled(body)
	if len(body.Tools) != 1 {
		t.Fatalf("web_search must not be injected when declared by function name, got %+v", body.Tools)
	}
}

func TestEnsureWebSearchEnabledAppendsToOtherTools(t *testing.T) {
	t.Setenv("M365_ENABLE_WEB_SEARCH", "")
	body := &oaiReq{Tools: []chathub.Tool{{Type: "function", Function: json.RawMessage(`{"name":"get_weather"}`)}}}
	ensureWebSearchEnabled(body)
	if len(body.Tools) != 2 || body.Tools[1].Type != "web_search" {
		t.Fatalf("expected web_search appended after get_weather, got %+v", body.Tools)
	}
}

func TestEnsureWebSearchEnabledDisabledValues(t *testing.T) {
	for _, v := range []string{"0", "false", "off", "no", "FALSE", "Off"} {
		t.Setenv("M365_ENABLE_WEB_SEARCH", v)
		body := &oaiReq{}
		ensureWebSearchEnabled(body)
		if len(body.Tools) != 0 {
			t.Fatalf("M365_ENABLE_WEB_SEARCH=%s must not inject, got %+v", v, body.Tools)
		}
	}
}

// The injected declaration must never activate the tool router: web_search
// is a Copilot built-in excluded from the router decision set.
func TestInjectedWebSearchStaysOutOfRouter(t *testing.T) {
	t.Setenv("M365_ENABLE_WEB_SEARCH", "")
	body := &oaiReq{}
	ensureWebSearchEnabled(body)
	toolMaps := make([]map[string]any, 0, len(body.Tools))
	for _, tool := range body.Tools {
		var f map[string]any
		_ = json.Unmarshal(tool.Function, &f)
		toolMaps = append(toolMaps, map[string]any{"type": tool.Type, "function": f})
	}
	if routes := routeableTools(toolMaps); len(routes) != 0 {
		t.Fatalf("web_search must be excluded from router decisions, got %+v", routes)
	}
	if !isWebSearchTool(toolMaps[0]) {
		t.Fatal("injected web_search must be recognized as web search tool")
	}
}

func TestRouteableToolsKeepsClientFunctions(t *testing.T) {
	body := &oaiReq{
		Tools: []chathub.Tool{
			{Type: "function", Function: json.RawMessage(`{"name":"get_weather"}`)},
			{Type: "web_search"},
		},
	}
	toolMaps := make([]map[string]any, 0, len(body.Tools))
	for _, tool := range body.Tools {
		var f map[string]any
		_ = json.Unmarshal(tool.Function, &f)
		toolMaps = append(toolMaps, map[string]any{"type": tool.Type, "function": f})
	}
	routes := routeableTools(toolMaps)
	if len(routes) != 1 {
		t.Fatalf("expected get_weather only in router set, got %+v", routes)
	}
	if f, _ := routes[0]["function"].(map[string]any); f["name"] != "get_weather" {
		t.Fatalf("routeableTools kept the wrong tool: %+v", routes)
	}
}
