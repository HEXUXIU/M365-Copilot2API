package web

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"m365-copilot2api/internal/chathub"
)

func TestToolStreamSeparatesArgumentsFromFinishReason(t *testing.T) {
	rr := httptest.NewRecorder()
	calls := []detectedToolCall{{
		ID: "call_weather", Type: "function", Name: "get_weather",
		Arguments: json.RawMessage(`{"city":"Shanghai"}`),
	}}
	if err := writeToolResponse(rr, "chatcmpl_test", "gpt-test", true, true, calls, chathub.Result{}, nil); err != nil {
		t.Fatal(err)
	}

	argumentChunk := -1
	finishChunk := -1
	for index, line := range strings.Split(rr.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			t.Fatal(err)
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if strings.Contains(mustJSON(delta), `"arguments":"{\"city\":\"Shanghai\"}"`) {
			argumentChunk = index
			if choice["finish_reason"] != nil {
				t.Fatalf("arguments and finish_reason shared a chunk: %#v", choice)
			}
		}
		if choice["finish_reason"] == "tool_calls" {
			finishChunk = index
			if len(delta) != 0 {
				t.Fatalf("terminal tool chunk contains delta data: %#v", choice)
			}
		}
	}
	if argumentChunk < 0 || finishChunk <= argumentChunk {
		t.Fatalf("tool stream order is invalid: arguments=%d finish=%d body=%s", argumentChunk, finishChunk, rr.Body.String())
	}
}

func TestToolStreamEmitsStandardUsageOnlyChunk(t *testing.T) {
	rr := httptest.NewRecorder()
	calls := []detectedToolCall{{
		ID: "call_weather", Type: "function", Name: "get_weather",
		Arguments: json.RawMessage(`{"city":"Shanghai"}`),
	}}
	if err := writeToolResponse(rr, "chatcmpl_test", "gpt-test", true, true, calls, chathub.Result{}, nil); err != nil {
		t.Fatal(err)
	}

	finishChunk := -1
	usageChunk := -1
	for index, line := range strings.Split(rr.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			t.Fatal(err)
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) > 0 {
			choice, _ := choices[0].(map[string]any)
			if choice["finish_reason"] == "tool_calls" {
				finishChunk = index
			}
		}
		if chunk["usage"] != nil {
			usageChunk = index
			if len(choices) != 0 {
				t.Fatalf("usage-only chunk must have empty choices: %#v", chunk)
			}
		}
	}
	if finishChunk < 0 || usageChunk <= finishChunk {
		t.Fatalf("usage-only chunk order is invalid: finish=%d usage=%d body=%s", finishChunk, usageChunk, rr.Body.String())
	}
}

func TestToolResponseIncludesProgressWhenProvided(t *testing.T) {
	rr := httptest.NewRecorder()
	calls := []detectedToolCall{{ID: "call_exec", Type: "custom", Name: "exec", Arguments: json.RawMessage(`{"input":"Get-Location"}`)}}
	if err := writeToolResponseWithProgress(rr, "chatcmpl_test", "gpt-test", false, true, calls, chathub.Result{}, nil, "我先检查当前环境并执行这一步。"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rr.Body.String(), "我先检查当前环境并执行这一步") {
		t.Fatalf("progress was not included in tool response: %s", rr.Body.String())
	}
}

func TestToolResponseUsesOnlyModelAuthoredProgress(t *testing.T) {
	calls := []detectedToolCall{{ID: "call_exec", Type: "custom", Name: "exec", Arguments: json.RawMessage("{\"input\":\"Get-Location\"}")}}
	withStatus := httptest.NewRecorder()
	if err := writeToolResponse(withStatus, "chatcmpl_test", "gpt-test", false, true, calls, chathub.Result{Text: "STATUS: I will inspect the environment first.\nCALL_TOOL: exec({})"}, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withStatus.Body.String(), "I will inspect the environment first.") {
		t.Fatalf("model-authored status was omitted: %s", withStatus.Body.String())
	}

	withoutStatus := httptest.NewRecorder()
	if err := writeToolResponse(withoutStatus, "chatcmpl_test", "gpt-test", false, true, calls, chathub.Result{Text: "CALL_TOOL: exec({})"}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(withoutStatus.Body.String(), "step 1") || strings.Contains(withoutStatus.Body.String(), "第 1 步") {
		t.Fatalf("gateway progress was invented: %s", withoutStatus.Body.String())
	}
}
