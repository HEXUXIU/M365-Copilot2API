package web

import "testing"

func TestFencedToolCalls(t *testing.T) {
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "get_current_time", "parameters": map[string]any{"type": "object"}}}}
	calls := fencedToolCalls("```get_current_time\n{}\n```", tools, "auto")
	if len(calls) != 1 || calls[0].Name != "get_current_time" || string(calls[0].Arguments) != "{}" {
		t.Fatalf("%v", calls)
	}
}

func TestFencedToolCallsRejectUnknown(t *testing.T) {
	tools := []map[string]any{{"type": "function", "function": map[string]any{"name": "get_current_time"}}}
	if got := fencedToolCalls("```shell\n{}\n```", tools, "auto"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := fencedToolCalls("```get_current_time\n{}\n```", tools, "none"); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
