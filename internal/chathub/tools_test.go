package chathub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClientPluginsWebSearchBuiltIn(t *testing.T) {
	// web_search must be declared exactly like the web UI toggle does:
	// {"Id":"BingWebSearch","Source":"BuiltIn"}, so Copilot searches
	// server-side instead of surfacing an unexecutable client tool call.
	byType := Tool{Type: "web_search", Function: nil}
	byName := Tool{Type: "function", Function: json.RawMessage(`{"name":"web_search","description":"search","parameters":null}`)}
	for _, tc := range []struct {
		name  string
		tools []Tool
	}{{"by type", []Tool{byType}}, {"by name", []Tool{byName}}} {
		t.Run(tc.name, func(t *testing.T) {
			plugins := clientPlugins(tc.tools, "")
			if len(plugins) != 1 {
				t.Fatalf("plugins = %#v", plugins)
			}
			m, _ := plugins[0].(map[string]any)
			if m["Id"] != "BingWebSearch" || m["Source"] != "BuiltIn" {
				t.Fatalf("web_search plugin = %#v, want BingWebSearch/BuiltIn", m)
			}
		})
	}
}

func TestClientPluginsOtherToolsStillClient(t *testing.T) {
	fn := Tool{Type: "function", Function: json.RawMessage(`{"name":"get_weather","description":"weather","parameters":{"type":"object"}}`)}
	plugins := clientPlugins([]Tool{fn}, "")
	if len(plugins) != 1 {
		t.Fatalf("plugins = %#v", plugins)
	}
	m, _ := plugins[0].(map[string]any)
	if m["Id"] != "get_weather" || m["Source"] != "Client" {
		t.Fatalf("ordinary tool plugin = %#v, want get_weather/Client", m)
	}
}

func TestToolProtocolPromptDeclaresWebSearch(t *testing.T) {
	ws := Tool{Type: "web_search", Function: nil}
	prompt := toolProtocolPrompt("Find the latest price.", []Tool{ws}, "auto")
	if !strings.Contains(prompt, "web_search") || !strings.Contains(prompt, `"query"`) {
		t.Fatalf("web_search declaration missing from prompt:\n%s", prompt)
	}
	fn := Tool{Type: "function", Function: json.RawMessage(`{"name":"get_weather","description":"weather","parameters":{"type":"object"}}`)}
	prompt = toolProtocolPrompt("What is the weather?", []Tool{fn}, "auto")
	if !strings.Contains(prompt, "get_weather") || strings.Contains(prompt, "web_search") {
		t.Fatalf("function tool rendering broken:\n%s", prompt)
	}
}
