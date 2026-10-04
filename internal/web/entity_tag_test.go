package web

import (
	"strings"
	"testing"
)

// The upstream model emits inline entity markup that a client should never see.
// The entity name must survive; only the markup is removed.
func TestStripEntityTagsKeepsContent(t *testing.T) {
	in := "没有发现可确认的 <Organization>NodeLoc</Organization> 备份，<Product>Discourse</Product> 官方导出也没有。"
	got := stripEntityTags(in)
	if strings.Contains(got, "<Organization>") || strings.Contains(got, "<Product>") || strings.Contains(got, "</") {
		t.Fatalf("entity markup leaked: %q", got)
	}
	for _, want := range []string{"NodeLoc", "Discourse"} {
		if !strings.Contains(got, want) {
			t.Fatalf("entity content %q was dropped: %q", want, got)
		}
	}
}

func TestStripEntityTagsLeavesOrdinaryTextAlone(t *testing.T) {
	in := "Use a < b and x > y, and keep 1 < 2 comparisons."
	if got := stripEntityTags(in); got != in {
		t.Fatalf("ordinary text was altered: %q", got)
	}
}