package web

import (
	"os"
	"strings"
	"testing"
)

func TestPublicUIHidesUnreliableQuotaEstimates(t *testing.T) {
	files := []string{"web/index.html", "../../web/index.html"}
	for _, path := range files {
		t.Run(path, func(t *testing.T) {
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(content)
			if !strings.Contains(text, `<div class="card" id="quotaDashboard" style="display:none">`) {
				t.Fatal("quota diagnostics card must remain hidden by default")
			}
			for _, forbidden := range []string{
				"renderQuotaDashboard();",
				")+accountQuotaDetails(x)",
				`data-account-action="probe"`,
			} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("public UI still activates unreliable quota element %q", forbidden)
				}
			}
		})
	}
}
