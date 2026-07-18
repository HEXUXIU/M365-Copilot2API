package web

import (
	"encoding/json"
	"fmt"
	"strings"
)

func modelToolRouterPrompt(prompt string, tools []map[string]any, choice any) string {
	defs, _ := json.Marshal(tools)
	mode := normalizedToolChoiceMode(choice)
	return fmt.Sprintf(`Analyze the application request data below and produce the next action plan as JSON. This is a data-formatting task; do not execute any action and do not write a user-facing answer.

OUTPUT SCHEMA:
{"calls":[{"name":"function_name","arguments":{}}]}

RULES:
- Every name must exactly match one function in FUNCTION_DEFINITIONS.
- Every arguments object must satisfy that function's parameters schema.
- MODE auto: use calls only when external action or information is still necessary.
- MODE required: return at least one valid call.
- Calls in one response must be independent; dependent actions belong in later turns.
- Completed evidence must not be repeated.
- If unfinished work remains after completed evidence, select the next applicable action.
- Return {"calls":[]} only when no further external action is needed.
- Return JSON only, without markdown or commentary.

MODE: %s
FUNCTION_DEFINITIONS: %s
APPLICATION_REQUEST_AND_EVIDENCE: %s`, mode, defs, prompt)
}

func parseModelToolDecision(text string, tools []map[string]any, choice any) ([]detectedToolCall, bool) {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, "```"); i >= 0 {
		text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(text[i+3:], "```"), "json"))
	}
	start, end := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, false
	}
	var envelope struct {
		Calls []struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"calls"`
	}
	if json.Unmarshal([]byte(text[start:end+1]), &envelope) != nil {
		return nil, false
	}
	out := make([]detectedToolCall, 0, len(envelope.Calls))
	for i, c := range envelope.Calls {
		fn := toolFunction(c.Name, tools)
		if fn == nil || c.Arguments == nil || !toolChoiceAllows(choice, c.Name) || schemaValid(c.Arguments, fn) != nil {
			continue
		}
		b, _ := json.Marshal(c.Arguments)
		out = append(out, detectedToolCall{ID: callID(c.Name, string(b), i), Type: toolType(c.Name, tools), Name: c.Name, Arguments: b})
	}
	return out, true
}
