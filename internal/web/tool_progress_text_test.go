package web

import (
	"strings"
	"testing"
)

func TestToolProgressAdvancesAcrossToolStages(t *testing.T) {
	calls := []detectedToolCall{{Name: "exec"}}
	first := toolProgressTextForStage("请检查环境", calls, 0)
	second := toolProgressTextForStage("请检查环境", calls, 1)
	if first == second || !strings.Contains(first, "第 1 步") || !strings.Contains(second, "第 2 步") {
		t.Fatalf("progress did not advance: first=%q second=%q", first, second)
	}
	if strings.Contains(second, "我先处理第 1 步") {
		t.Fatalf("continuation repeated first-turn preamble: %q", second)
	}
}

func TestToolProgressUsesActionSpecificTextAndLocale(t *testing.T) {
	zh := toolProgressTextForStage("打开网页并确认状态", []detectedToolCall{{Name: "browser"}}, 0)
	en := toolProgressTextForStage("Open the page and verify its state", []detectedToolCall{{Name: "browser"}}, 0)
	if !strings.Contains(zh, "打开页面") || !strings.Contains(en, "open the page") {
		t.Fatalf("action-specific progress missing: zh=%q en=%q", zh, en)
	}
}

func TestModelAuthoredToolStatusIsExtractedAndIgnoredByParser(t *testing.T) {
	raw := "**STATUS:** 我先读取项目配置，确认当前环境后再执行请求。\nI am ready.\nCALL_TOOL: exec({\"input\":\"Get-Location\"})"
	if got := toolDecisionStatus(raw); got != "我先读取项目配置，确认当前环境后再执行请求。" {
		t.Fatalf("status=%q", got)
	}
	calls, parsed := parseModelToolDecision(raw, []map[string]any{{
		"type": "custom",
		"function": map[string]any{
			"name":       "exec",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}}, "required": []any{"input"}},
		},
	}}, "required")
	if !parsed || len(calls) != 1 || calls[0].Name != "exec" {
		t.Fatalf("parsed=%t calls=%#v", parsed, calls)
	}
}

func TestModelToolProgressOnlyForExplicitModelStatus(t *testing.T) {
	if got := modelToolProgress("STATUS: I will inspect the environment before running the requested command.\nCALL_TOOL: exec({})"); got != "I will inspect the environment before running the requested command." {
		t.Fatalf("model status was not preserved: %q", got)
	}
	if got := modelToolProgress("CALL_TOOL: exec({})"); got != "" {
		t.Fatalf("gateway invented progress: %q", got)
	}
}
