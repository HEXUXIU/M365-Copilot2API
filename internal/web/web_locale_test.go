package web

import (
	"os"
	"strings"
	"testing"
)

func TestWebIndexDefaultsToChineseUntilLocaleIsSelected(t *testing.T) {
	body, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, needle := range []string{
		"const localeSelectionKey='m365_locale_selected';",
		"function preferredLocale()",
		"return 'zh-CN';",
		"localStorage.setItem(localeSelectionKey,'1')",
	} {
		if !strings.Contains(page, needle) {
			t.Fatalf("web index missing Chinese default bootstrap %q", needle)
		}
	}
}

func TestWebIndexIncludesAccountMonitoringControls(t *testing.T) {
	body, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, needle := range []string{
		`data-f="cooldown"`,
		`x.status==='cooldown'`,
		`/api/accounts/schedule`,
		`x.callCount||0`,
		`x.rateLimited`,
		`Limited after ${x.callCount||0} calls`,
	} {
		if !strings.Contains(page, needle) {
			t.Fatalf("web index missing cooldown control %q", needle)
		}
	}
}

func TestWebIndexIncludesConversationKeyFilterAndKeyNameUsage(t *testing.T) {
	body, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, needle := range []string{
		`id="convKeyFilter"`,
		"_convsCache",
		"x.apiKeyName",
		"m.api_key_name||m.api_key_prefix",
		"'All keys':",
		"'Tokens (7d)':",
		"'Unknown key':",
		"<th>API Key</th>",
	} {
		if !strings.Contains(page, needle) {
			t.Fatalf("web index missing api-key attribution UI %q", needle)
		}
	}

	detail, err := os.ReadFile("../../web/conversation.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{
		`id="apiKey"`,
		"data.apiKeyName||data.apiKeyId",
	} {
		if !strings.Contains(string(detail), needle) {
			t.Fatalf("conversation detail page missing api-key attribution %q", needle)
		}
	}
}

func TestWebIndexIncludesKeyUsageModal(t *testing.T) {
	body, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, needle := range []string{
		`id="keyUsageModal"`,
		"openKeyUsage(",
		"renderTrend('kuTrendWrap'",
		"renderBars('kuModels'",
		"renderBars('kuEndpoints'",
		"/api/usage/key?id=",
		"'Usage detail':",
	} {
		if !strings.Contains(page, needle) {
			t.Fatalf("web index missing key usage modal %q", needle)
		}
	}
}

// TestWebIndexOverviewCardUnits 锁定仪表盘概览卡的数值/单位结构：
// 主数值放内层 span，单位 <small> 不再被 loadStats 的 textContent 覆盖；
// Request count 卡主数值是今日请求数；Cache hits 单位是次数而非 token。
func TestWebIndexOverviewCardUnits(t *testing.T) {
	body, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, needle := range []string{
		`<span class="usage-overview-value"><span id="ov24hTok">0</span><small>tokens</small></span>`,
		`<span class="usage-overview-value"><span id="ovTodayReq">0</span><small>requests</small></span>`,
		`<span class="usage-overview-value"><span id="ovCacheHits">0</span><small>hits</small></span>`,
		"$('ovTodayReq').textContent=fmtNum(sum.today_requests||0);",
		"tokens today",
		`<span class="stat-label">Total requests</span>`,
		"'requests':",
		"'tokens':",
	} {
		if !strings.Contains(page, needle) {
			t.Fatalf("web index missing overview unit fix %q", needle)
		}
	}
	if strings.Contains(page, `id="ovCacheHits">0<small>tokens`) {
		t.Fatalf("cache hits card still carries a tokens unit")
	}
}
