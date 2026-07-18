package web

import (
	"encoding/json"
	"m365-native/internal/chathub"
	"strings"
	"testing"
)

func TestCompatMetadataHidesEventsByDefault(t *testing.T) {
	t.Setenv("M365_INCLUDE_UPSTREAM_EVENTS", "")
	res := chathub.Result{ConversationID: "conv", SessionID: "sess", RequestID: "req", Events: []json.RawMessage{json.RawMessage(`{"secret":"internal"}`)}}
	m := compatM365Metadata(res)
	if _, ok := m["events"]; ok {
		t.Fatal("upstream events exposed by default")
	}
	if m["conversationId"] != "conv" || m["usage_source"] != "unavailable_from_chathub" {
		t.Fatalf("metadata=%#v", m)
	}
}

func TestCompatMetadataEventsAreExplicitOptIn(t *testing.T) {
	t.Setenv("M365_INCLUDE_UPSTREAM_EVENTS", "true")
	res := chathub.Result{Events: []json.RawMessage{json.RawMessage(`{"type":1}`)}}
	if _, ok := compatM365Metadata(res)["events"]; !ok {
		t.Fatal("opt-in events missing")
	}
}

func TestNamedToolChoiceModeIsStable(t *testing.T) {
	choice := map[string]any{"name": "weather"}
	if got := normalizedToolChoiceMode(choice); got != "named:weather" {
		t.Fatalf("mode=%q", got)
	}
	p := modelToolRouterPrompt("request", testTools(), choice)
	if !strings.Contains(p, "MODE: named:weather") || strings.Contains(p, "map[name:") {
		t.Fatalf("prompt=%s", p)
	}
}
