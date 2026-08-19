package web

import (
	"strings"
	"testing"
)

// Local extension: raw keys are persisted so the console can re-copy them.
// Regression test for the restart-wipe bug the earlier patch had — a reload
// from disk must keep Raw while still masking Hash in list output.
func TestKeyStorePersistsRawForConsoleCopy(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/api-keys.json"
	t.Setenv("M365_API_KEYS", path)

	s := openAPIKeys()
	rec, raw, err := s.create("console")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(raw, "m365_") {
		t.Fatalf("create returned malformed key %q", raw)
	}
	if rec.Raw != "" {
		t.Fatalf("create response should mask Raw, got %q", rec.Raw)
	}

	// Reload from disk: raw must survive for the console copy button.
	s2 := openAPIKeys()
	keys := s2.list()
	if len(keys) != 1 {
		t.Fatalf("reloaded store has %d keys, want 1", len(keys))
	}
	if keys[0].Raw != raw {
		t.Fatalf("raw key lost on reload: got %q want %q", keys[0].Raw, raw)
	}
	if keys[0].Hash != "" {
		t.Fatalf("Hash must stay masked in list output, got %q", keys[0].Hash)
	}
	if !s2.valid(raw) {
		t.Fatal("reloaded store should still accept the key")
	}
}
