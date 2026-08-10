package chathub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClassifyUpdateMessages(t *testing.T) {
	got := classifyUpdateMessages([]any{
		map[string]any{"author": "bot", "text": "我先查一下", "messageType": ""},
		map[string]any{"messageType": "Progress", "contentType": "SearchResults", "text": "正在搜索"},
		map[string]any{"toolName": "web_search", "arguments": map[string]any{"query": "golang"}},
	})
	if len(got) != 3 || got[0].Kind != "text" || got[1].Kind != "text" || got[2].Kind != "tool" {
		t.Fatalf("unexpected events: %#v", got)
	}
	// SearchResults surfaces as a visible citation, not a dropped progress frame.
	if got[1].ContentType != "SearchResults" || !strings.HasPrefix(got[1].Text, "\U0001F50E ") {
		t.Fatalf("SearchResults should surface as a citation: %#v", got[1])
	}
	if got[2].ToolName != "web_search" || len(got[2].Arguments) == 0 {
		t.Fatalf("tool fields missing: %#v", got[2])
	}
}

func TestClassifyUpdateMessagesDropsEmptyWebSearch(t *testing.T) {
	got := classifyUpdateMessages([]any{
		map[string]any{"messageType": "Progress", "contentType": "ToolCall", "toolName": "web_search", "arguments": map[string]any{}},
		map[string]any{"toolName": "web_search", "arguments": map[string]any{"query": "ok"}},
	})
	if len(got) != 2 || got[0].Kind == "tool" || got[1].Kind != "tool" || got[1].ToolName != "web_search" {
		t.Fatalf("empty web_search must not become a tool call, got %#v", got)
	}
}

func TestExtractToolEventsDropsEmptyWebSearch(t *testing.T) {
	seen := map[string]bool{}
	got := extractToolEvents([]any{
		map[string]any{"plugin": map[string]any{"functionName": "web_search", "functionArguments": map[string]any{}}},
		map[string]any{"plugin": map[string]any{"functionName": "web_search", "functionArguments": map[string]any{"query": "x"}}},
	}, seen)
	if len(got) != 1 || got[0].ToolName != "web_search" || len(got[0].Arguments) == 0 {
		t.Fatalf("empty web_search must be dropped, got %#v", got)
	}
}

func TestWebSearchUsable(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]any
		want bool
	}{
		{"non web_search always usable", "other_tool", map[string]any{"x": 1}, true},
		{"web_search with query", "web_search", map[string]any{"query": "golang"}, true},
		{"web_search with q", "web_search", map[string]any{"q": "golang"}, true},
		{"web_search empty object", "web_search", map[string]any{}, false},
		{"web_search blank query", "web_search", map[string]any{"query": "  "}, false},
		{"web_search null", "web_search", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b []byte
			if tc.args != nil {
				b, _ = json.Marshal(tc.args)
			}
			if got := WebSearchUsable(tc.tool, b); got != tc.want {
				t.Fatalf("WebSearchUsable = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClassifyChainOfThoughtAsReasoning(t *testing.T) {
	got := classifyUpdateMessages([]any{
		map[string]any{"author": "bot", "text": "**搜索用户需求**\n- 查询相关文档", "messageType": "Progress", "contentOrigin": "ChainOfThoughtSummary"},
		map[string]any{"author": "bot", "text": "使用工具查找", "messageType": "Progress", "addToChainOfThought": true},
		map[string]any{"author": "bot", "text": "普通进度", "messageType": "Progress", "contentOrigin": "SomeOtherOrigin"},
	})
	if len(got) != 3 {
		t.Fatalf("unexpected event count: %#v", got)
	}
	if got[0].Kind != "reasoning" || got[0].Text == "" {
		t.Fatalf("expected reasoning, got %#v", got[0])
	}
	if got[1].Kind != "reasoning" {
		t.Fatalf("expected reasoning via addToChainOfThought, got %#v", got[1])
	}
	if got[2].Kind != "progress" {
		t.Fatalf("ordinary progress must stay progress, got %#v", got[2])
	}
}

func TestExtractToolEventsNestedAndDeduped(t *testing.T) {
	seen := map[string]bool{}
	arg := map[string]any{"plugin": map[string]any{"functionName": "list_files", "functionArguments": map[string]any{"path": "."}}}
	got := extractToolEvents([]any{arg, arg}, seen)
	if len(got) != 1 || got[0].ToolName != "list_files" {
		t.Fatalf("unexpected nested tools: %#v", got)
	}
}
