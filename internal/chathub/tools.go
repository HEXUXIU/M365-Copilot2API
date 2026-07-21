package chathub

import "encoding/json"

type Tool struct {
	Type     string          `json:"type"`
	Function json.RawMessage `json:"function,omitempty"`
}

func clientPlugins(tools []Tool, mcpServerURL string) []any {
	plugins := make([]any, 0, len(tools)+1)
	if mcpServerURL != "" {
		plugins = append(plugins, map[string]any{
			"Id":                   "mcp-gateway",
			"Source":               "MCPServer",
			"Description":          "MCP Gateway tools",
			"Transport":            "mcp",
			"TransportUrl":         mcpServerURL,
			"TransportProtocol":    "https://copilot.microsoft.com/schemas/plugins/local/transport/1.0",
		})
	}
	for _, t := range tools {
		var f struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		}
		if json.Unmarshal(t.Function, &f) != nil || f.Name == "" {
			continue
		}
		plugins = append(plugins, map[string]any{"Id": f.Name, "Source": "Client", "Description": f.Description, "Parameters": f.Parameters})
	}
	return plugins
}
