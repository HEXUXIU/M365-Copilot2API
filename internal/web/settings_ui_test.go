package web

import (
	"os"
	"strings"
	"testing"
)

func TestSettingsPageExposesCacheControls(t *testing.T) {
	body, err := os.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, want := range []string{
		`id="cacheBalanced"`,
		`id="cacheSticky"`,
		`id="setStickyFullContext"`,
		`id="setAccountConcurrency"`,
		`id="setStickyConcurrency"`,
		`body.accountConcurrencyLimit=accountConcurrency`,
		`body.stickyAccountConcurrency=stickyConcurrency`,
		`body.cacheStrategy=currentCacheStrategy`,
		`body.stickyFullContext=$('setStickyFullContext').checked`,
		`$('stickyFullContextGroup').hidden=!sticky`,
		`id="setToolProtocolMode"`,
		`body.toolProtocolMode=$('setToolProtocolMode').value`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("settings page missing %q", want)
		}
	}
}
