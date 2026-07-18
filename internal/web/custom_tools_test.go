package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func containsJSON(v []byte, key string) bool { return strings.Contains(string(v), `"`+key+`"`) }

func customCallSource() map[string]any {
	return map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"tool_calls": []any{map[string]any{
					"id":   "call_exec",
					"type": "custom",
					"function": map[string]any{
						"name":      "exec",
						"arguments": `{"input":"uname -s"}`,
					},
				}},
			},
		}},
	}
}

func TestResponsesResultWritesCustomToolCall(t *testing.T) {
	rr := httptest.NewRecorder()
	writeResponsesResult(rr, "m", false, customCallSource())
	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	output := response["output"].([]any)
	call := output[1].(map[string]any)
	if call["type"] != "custom_tool_call" || call["name"] != "exec" || call["input"] != "uname -s" {
		t.Fatalf("custom output=%#v", call)
	}
}

func TestResponsesStreamWritesCustomToolEvents(t *testing.T) {
	rr := httptest.NewRecorder()
	writeResponsesResult(rr, "m", true, customCallSource())
	body := rr.Body.String()
	for _, want := range []string{"response.custom_tool_call_input.delta", "response.custom_tool_call_input.done", `"input":"uname -s"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
}
