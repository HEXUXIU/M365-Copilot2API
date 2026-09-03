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

func TestClassifyUpdateMessagesSharesToolDedupWithNestedExtractor(t *testing.T) {
	seen := map[string]bool{}
	arg := map[string]any{
		"toolName":  "exec",
		"arguments": map[string]any{"input": "Get-Location"},
	}
	first := extractToolEvents(arg, seen)
	second := classifyUpdateMessagesWithSeen([]any{arg}, seen)
	if len(first) != 1 || len(second) != 0 {
		t.Fatalf("duplicate tool crossed event paths: first=%#v second=%#v seen=%#v", first, second, seen)
	}
}

func TestClassifyUpdateMessagesKeepsDistinctParallelCallsWithSameArguments(t *testing.T) {
	seen := map[string]bool{}
	args := map[string]any{"input": "Get-Location"}
	got := classifyUpdateMessagesWithSeen([]any{
		map[string]any{"toolName": "exec", "callId": "call-a", "arguments": args},
		map[string]any{"toolName": "exec", "callId": "call-b", "arguments": args},
	}, seen)
	if len(got) != 2 {
		t.Fatalf("distinct parallel calls were collapsed: %#v", got)
	}
}

func TestClassifyUpdateMessagesDoesNotUseMessageIDAsToolIdentity(t *testing.T) {
	seen := map[string]bool{}
	args := map[string]any{"input": "Get-Location"}
	got := classifyUpdateMessagesWithSeen([]any{
		map[string]any{"toolName": "exec", "messageId": "message-1", "arguments": args},
		map[string]any{"toolName": "exec", "messageId": "message-1", "arguments": map[string]any{"input": "Get-ChildItem"}},
	}, seen)
	if len(got) != 2 {
		t.Fatalf("distinct calls sharing an envelope message ID were collapsed: %#v", got)
	}
}

func TestClassifyUpdateMessagesDeduplicatesSameCallIDAcrossRepresentations(t *testing.T) {
	seen := map[string]bool{}
	args := map[string]any{"input": "Get-Location"}
	first := extractToolEvents(map[string]any{"toolName": "exec", "call_id": "call-same", "arguments": args}, seen)
	second := classifyUpdateMessagesWithSeen([]any{
		map[string]any{"toolName": "exec", "call_id": "call-same", "arguments": args},
	}, seen)
	if len(first) != 1 || len(second) != 0 {
		t.Fatalf("same call identity was emitted twice: first=%#v second=%#v", first, second)
	}
}

func TestParseSuggestedResponseMessageFields(t *testing.T) {
	got := parseSuggestedResponse(map[string]any{
		"commandText": "next",
		"text":        "Next question",
		"messageId":   "msg-1",
		"author":      "bot",
	})
	if got.CommandText != "next" || got.Text != "Next question" || got.MessageID != "msg-1" || got.Author != "bot" {
		t.Fatalf("unexpected suggested response: %#v", got)
	}
}

func TestMessageReferenceMapDeduplicatesByKey(t *testing.T) {
	references := map[string]Reference{}
	references["ref-1"] = Reference{TargetLink: "https://example.com/old", Title: "Old"}
	references["ref-1"] = Reference{TargetLink: "https://example.com/new", Title: "New"}
	if len(references) != 1 || references["ref-1"].TargetLink != "https://example.com/new" {
		t.Fatalf("references were not deduplicated by key: %#v", references)
	}
}
