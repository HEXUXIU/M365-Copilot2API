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
