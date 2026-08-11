package web

import (
	"os"
	"strings"
	"testing"
)

func TestWebIndexHasChineseLoginLocaleBootstrap(t *testing.T) {
	body, err := os.ReadFile("../../web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, needle := range []string{
		`id="loginLanguageSelect"`,
		"function preferredLocale()",
		"return 'zh';",
		"loginLanguageSelect",
	} {
		if !strings.Contains(page, needle) {
			t.Fatalf("web index missing locale bootstrap %q", needle)
		}
	}
}
