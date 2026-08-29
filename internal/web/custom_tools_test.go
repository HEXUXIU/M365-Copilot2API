package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func containsJSON(v []byte, key string) bool { return strings.Contains(string(v), `"`+key+`"`) }

func customCallSource() map[string]any {
	return customCallSourceFor("exec", "uname -s")
}

func customCallSourceFor(name, input string) map[string]any {
	return map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{
				"tool_calls": []any{map[string]any{
					"id":   "call_" + name,
					"type": "custom",
					"function": map[string]any{
						"name":      name,
						"arguments": mustJSON(map[string]any{"input": input}),
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
	call := output[0].(map[string]any)
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

func TestResponsesStreamWritesCustomApplyPatchEvents(t *testing.T) {
	input := "*** Begin Patch\n*** Add File: 1.txt\n+123214324\n*** End Patch"
	rr := httptest.NewRecorder()
	writeResponsesResult(rr, "m", true, customCallSourceFor("apply_patch", input))
	body := rr.Body.String()
	for _, want := range []string{
		"event: response.output_item.added",
		`"type":"custom_tool_call"`,
		`"name":"apply_patch"`,
		"event: response.custom_tool_call_input.delta",
		"event: response.custom_tool_call_input.done",
		"event: response.output_item.done",
		"event: response.completed",
		`"input":"*** Begin Patch`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in stream: %s", want, body)
		}
	}
	added := strings.Index(body, "event: response.output_item.added")
	delta := strings.Index(body, "event: response.custom_tool_call_input.delta")
	done := strings.Index(body, "event: response.custom_tool_call_input.done")
	itemDone := strings.Index(body, "event: response.output_item.done")
	completed := strings.Index(body, "event: response.completed")
	if !(added < delta && delta < done && done < itemDone && itemDone < completed) {
		t.Fatalf("custom apply_patch events are out of order: %s", body)
	}
}
