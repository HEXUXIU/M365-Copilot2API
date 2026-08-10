package chathub

import (
	"encoding/json"
	"strings"
)

type Tool struct {
	Type     string          `json:"type"`
	Function json.RawMessage `json:"function,omitempty"`
}

// webSearchPlugin mirrors the plugins payload the M365 web UI sends when the
// web search toggle is on (PlugInInfo: [{"Id":"BingWebSearch","Source":
// "BuiltIn"}]). Copilot runs the Bing search server-side and returns
// grounded answers with SearchResults citations; a Client-side duplicate
// would only surface a tool call the caller cannot execute.
var webSearchPlugin = map[string]any{"Id": "BingWebSearch", "Source": "BuiltIn"}

func clientPlugins(tools []Tool, mcpServerURL string) []any {
	plugins := make([]any, 0, len(tools)+1)
	if mcpServerURL != "" {
		plugins = append(plugins, map[string]any{
			"Id":                "mcp-gateway",
			"Source":            "MCPServer",
			"Description":       "MCP Gateway tools",
			"Transport":         "mcp",
			"TransportUrl":      mcpServerURL,
			"TransportProtocol": "https://copilot.microsoft.com/schemas/plugins/local/transport/1.0",
		})
	}
	for _, t := range tools {
		if strings.EqualFold(t.Type, "web_search") {
			plugins = append(plugins, webSearchPlugin)
			continue
		}
		var f struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}
		if json.Unmarshal(t.Function, &f) != nil || f.Name == "" {
			continue
		}
		if strings.EqualFold(f.Name, "web_search") {
			plugins = append(plugins, webSearchPlugin)
			continue
		}
		plugins = append(plugins, map[string]any{"Id": f.Name, "Source": "Client", "Description": f.Description, "Parameters": f.Parameters})
	}
	return plugins
}
