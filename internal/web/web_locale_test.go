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
