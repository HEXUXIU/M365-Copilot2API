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

func TestResponsesResultRepairsMissingToolCallID(t *testing.T) {
	rr := httptest.NewRecorder()
	writeResponsesResult(rr, "m", false, map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{
			"tool_calls": []any{map[string]any{
				"id": "", "type": "function",
				"function": map[string]any{"name": "lookup", "arguments": "{}"},
			}},
		}}},
	})
	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	output, _ := response["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("output=%#v", output)
	}
	call, _ := output[0].(map[string]any)
	if strings.TrimSpace(call["call_id"].(string)) == "" || call["name"] != "lookup" {
		t.Fatalf("tool identity was not repaired: %#v", call)
	}
}

func TestResponsesResultMakesParallelToolCallIDsUnique(t *testing.T) {
	rr := httptest.NewRecorder()
	writeResponsesResult(rr, "m", false, map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{
			"tool_calls": []any{
				map[string]any{"id": "same", "type": "function", "function": map[string]any{"name": "first", "arguments": "{}"}},
				map[string]any{"id": "same", "type": "function", "function": map[string]any{"name": "second", "arguments": "{}"}},
			},
		}}},
	})
	var response map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	output, _ := response["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("output=%#v", output)
	}
	first, _ := output[0].(map[string]any)
	second, _ := output[1].(map[string]any)
	if first["call_id"] == second["call_id"] || strings.TrimSpace(second["call_id"].(string)) == "" {
		t.Fatalf("parallel tool IDs are not unique: first=%#v second=%#v", first, second)
	}
}

func TestNormalizeResponsesResultKeepsGeneratedIDForContinuationState(t *testing.T) {
	src := map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{
			"tool_calls": []any{map[string]any{
				"type": "function", "function": map[string]any{"name": "lookup", "arguments": "{}"},
			}},
		}}},
	}
	if err := normalizeResponsesResult(src); err != nil {
		t.Fatal(err)
	}
	msg, _ := openAIChoice(src)
	calls, _ := msg["tool_calls"].([]any)
	call, _ := calls[0].(map[string]any)
	id, _ := call["id"].(string)
	if strings.TrimSpace(id) == "" || buildRespToolCallsMap([]map[string]any{call})[id] == nil {
		t.Fatalf("generated call ID was not retained for continuation state: %#v", call)
	}
}
