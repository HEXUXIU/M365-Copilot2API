package web

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"m365-native/internal/chathub"
)

func TestConfiguredLimitSerializesOneToolCall(t *testing.T) {
	calls := []detectedToolCall{{ID: "call_1", Name: "first", Arguments: json.RawMessage(`{"x":1}`)}, {ID: "call_2", Name: "second", Arguments: json.RawMessage(`{"y":2}`)}}
	w := httptest.NewRecorder()
	limited := limitToolCalls(calls, 1)
	if err := writeToolResponse(w, "chatcmpl_test", "test", false, limited, chathub.Result{}); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	choices := out["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	got := msg["tool_calls"].([]any)
	if len(got) != 1 {
		t.Fatalf("serialized %d calls: %s", len(got), w.Body.String())
	}
	fn := got[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "first" {
		t.Fatalf("wrong call: %#v", fn)
	}
}
