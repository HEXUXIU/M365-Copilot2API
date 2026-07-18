package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLimitToolCalls(t *testing.T) {
	calls := []detectedToolCall{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	got := limitToolCalls(calls, 1)
	if len(got) != 1 || got[0].Name != "a" {
		t.Fatalf("got %#v", got)
	}
	if len(limitToolCalls(calls, 2)) != 2 {
		t.Fatal("expected two calls")
	}
	if len(limitToolCalls(calls, 99)) != 3 {
		t.Fatal("must preserve calls below limit")
	}
}
func TestSettingsPersistAndValidate(t *testing.T) {
	s := &settingsStore{path: filepath.Join(t.TempDir(), "settings.json"), v: defaultRuntimeSettings()}
	v := s.v
	v.MaxToolCallsPerTurn = 1
	v.MaxToolRounds = 32
	v.ChatTimeoutSeconds = 60
	v.ImageTimeoutSeconds = 90
	if err := s.save(v); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.path); err != nil {
		t.Fatal(err)
	}
	v.MaxToolCallsPerTurn = 0
	if err := s.save(v); err == nil {
		t.Fatal("expected validation error")
	}
}
