package chathub

import "testing"

func TestClassifyUpdateMessages(t *testing.T) {
	got := classifyUpdateMessages([]any{
		map[string]any{"author": "bot", "text": "我先查一下", "messageType": ""},
		map[string]any{"messageType": "Progress", "contentType": "SearchResults", "text": "正在搜索"},
		map[string]any{"toolName": "web_search", "arguments": map[string]any{"query": "golang"}},
	})
	if len(got) != 3 || got[0].Kind != "text" || got[1].Kind != "progress" || got[2].Kind != "tool" {
		t.Fatalf("unexpected events: %#v", got)
	}
	if got[2].ToolName != "web_search" || len(got[2].Arguments) == 0 {
		t.Fatalf("tool fields missing: %#v", got[2])
	}
}

func TestClassifyChainOfThoughtAsReasoning(t *testing.T) {
	got := classifyUpdateMessages([]any{
		map[string]any{"author": "bot", "text": "正在分析", "messageType": "Progress", "contentType": "EarlyProgress"},
		map[string]any{"author": "bot", "text": "**搜索用户需求**\n- 查询相关文档", "messageType": "Progress", "contentOrigin": "ChainOfThoughtSummary"},
		map[string]any{"author": "bot", "text": "使用工具查找", "messageType": "Progress", "addToChainOfThought": true},
		map[string]any{"author": "bot", "text": "普通进度", "messageType": "Progress", "contentOrigin": "SomeOtherOrigin"},
	})
	if len(got) != 4 {
		t.Fatalf("unexpected event count: %#v", got)
	}
	if got[0].Kind != "reasoning" || got[0].Text == "" {
		t.Fatalf("expected reasoning, got %#v", got[0])
	}
	if got[1].Kind != "reasoning" {
		t.Fatalf("expected chain-of-thought summary, got %#v", got[1])
	}
	if got[2].Kind != "reasoning" {
		t.Fatalf("expected reasoning via addToChainOfThought, got %#v", got[2])
	}
	if got[3].Kind != "progress" {
		t.Fatalf("ordinary progress must stay progress, got %#v", got[3])
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
