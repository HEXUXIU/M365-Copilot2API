package chathub

import "encoding/json"

type Tool struct {
	Type     string          `json:"type"`
	Function json.RawMessage `json:"function,omitempty"`
}

// UnmarshalJSON accepts both standard OpenAI nested tools
// ({"type":"function","function":{"name":...}}) and flat Responses/Anthropic
// tools ({"type":"function","name":...,"parameters":...} or "input_schema")
// forwarded by upstream relays such as CPA / 9Router / Sub2API.
func (t *Tool) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type        string          `json:"type"`
		Function    json.RawMessage `json:"function"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	t.Type = raw.Type
	if t.Type == "" {
		t.Type = "function"
	}
	if len(raw.Function) > 0 && string(raw.Function) != "null" {
		t.Function = raw.Function
		return nil
	}
	if raw.Name != "" {
		params := raw.Parameters
		if len(params) == 0 || string(params) == "null" {
			params = raw.InputSchema
		}
		fn := map[string]any{
			"name":        raw.Name,
			"description": raw.Description,
		}
		if len(params) > 0 && string(params) != "null" {
			var p any
			if json.Unmarshal(params, &p) == nil {
				fn["parameters"] = p
			}
		} else if t.Type == "custom" {
			fn["parameters"] = map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"input": map[string]any{"type": "string"}},
				"required":             []string{"input"},
				"additionalProperties": false,
			}
		}
		b, _ := json.Marshal(fn)
		t.Function = b
	}
	return nil
}

func clientPlugins(tools []Tool, mcpServerURL string, disableWebSearch bool) []any {
	plugins := make([]any, 0, 2)
	if !disableWebSearch && mcpServerURL == "" && len(tools) == 0 {
		plugins = append(plugins, map[string]any{"Id": "BingWebSearch", "Source": "BuiltIn"})
	}
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
	return plugins
}
