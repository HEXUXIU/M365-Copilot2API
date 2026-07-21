package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type toolEvidence struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
	Failed    bool   `json:"failed"`
}
type agentLedger struct {
	Completed           []toolEvidence `json:"completed"`
	Pending             []toolEvidence `json:"pending"`
	ToolRounds          int            `json:"tool_rounds"`
	RepeatedCall        bool           `json:"repeated_call"`
	RepeatedFailure     bool           `json:"repeated_failure"`
	RepetitionSignature string         `json:"repetition_signature,omitempty"`
}

var failureSignal = regexp.MustCompile(`(?i)(exit\s*(code|status)?\s*[:=]?\s*[1-9]\d*|\berror\b|\bfailed\b|\bfailure\b|exception|traceback|timed?\s*out|permission denied|not found|refused)`)
var unsupportedSuccess = regexp.MustCompile(`(?i)\b(installed|created|written|executed|ran|started|deployed|deleted|verified|completed|succeeded|successful(?:ly)?)\b`)

func compactToolResult(s string, limit int) string {
	s = strings.TrimSpace(s)
	if limit < 200 {
		limit = 200
	}
	if len(s) <= limit {
		return s
	}
	head := limit / 3
	tail := limit - head - 80
	if tail < 80 {
		tail = 80
	}
	return s[:head] + fmt.Sprintf("\n... [truncated %d bytes] ...\n", len(s)-head-tail) + s[len(s)-tail:]
}
func scopedCallID(name, args string, index int, scope string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s:%s", scope, index, name, args)))
	return "call_" + hex.EncodeToString(h[:8])
}
func buildAgentLedger(messages []oaiMsg) agentLedger {
	calls := map[string]toolEvidence{}
	order := []string{}
	for _, m := range messages {
		if m.Role == "assistant" {
			for _, raw := range m.ToolCalls {
				id, _ := raw["id"].(string)
				fn, _ := raw["function"].(map[string]any)
				name, _ := fn["name"].(string)
				args := fmt.Sprint(fn["arguments"])
				if id != "" {
					calls[id] = toolEvidence{ID: id, Name: name, Arguments: args}
					order = append(order, id)
				}
			}
		}
		if m.Role == "tool" {
			if e, ok := calls[m.ToolCallID]; ok {
				e.Result = compactToolResult(contentToString(m.Content), 4000)
				e.Failed = failureSignal.MatchString(e.Result)
				calls[m.ToolCallID] = e
			}
		}
	}
	l := agentLedger{}
	seenCall := map[string]int{}
	seenFailure := map[string]int{}
	for _, id := range order {
		e := calls[id]
		l.ToolRounds++
		sig := e.Name + "\x00" + e.Arguments
		seenCall[sig]++
		if seenCall[sig] >= 2 {
			l.RepeatedCall = true
			l.RepetitionSignature = sig
		}
		if e.Result == "" {
			l.Pending = append(l.Pending, e)
		} else {
			l.Completed = append(l.Completed, e)
			if e.Failed {
				fs := e.Name + "\x00" + e.Arguments + "\x00" + normalizeFailure(e.Result)
				seenFailure[fs]++
				if seenFailure[fs] >= 2 {
					l.RepeatedFailure = true
					l.RepetitionSignature = fs
				}
			}
		}
	}
	return l
}
func normalizeFailure(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`\d+`).ReplaceAllString(s, "#")
	if len(s) > 500 {
		s = s[:500]
	}
	return s
}
func (l agentLedger) RouterContext() string {
	type compact struct {
		Completed    []toolEvidence `json:"completed"`
		Pending      []toolEvidence `json:"pending"`
		RepeatedCall bool           `json:"repeated_call"`
	}
	b, _ := json.Marshal(compact{l.Completed, l.Pending, l.RepeatedCall})
	hint := "Use only this compact evidence. A completed call is final evidence; do not issue the same name and arguments again."
	if l.RepeatedFailure {
		hint += " The same call failed repeatedly; change strategy instead of retrying unchanged."
	}
	return hint + "\nEVIDENCE_LEDGER: " + string(b)
}
func canonicalToolArguments(s string) string {
	s = strings.TrimSpace(s)
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		b, _ := json.Marshal(v)
		return string(b)
	}
	return s
}

func (l agentLedger) hasCompleted(name, args string) bool {
	want := canonicalToolArguments(args)
	for _, e := range l.Completed {
		if e.Name == name && canonicalToolArguments(e.Arguments) == want {
			return true
		}
	}
	return false
}
func filterCompletedCalls(calls []detectedToolCall, l agentLedger) []detectedToolCall {
	out := calls[:0]
	for _, c := range calls {
		if !l.hasCompleted(c.Name, string(c.Arguments)) {
			out = append(out, c)
		}
	}
	return out
}
func (l agentLedger) CanContinue(maxRounds int) error {
	if maxRounds <= 0 {
		maxRounds = 32
	}
	if l.ToolRounds >= maxRounds {
		return fmt.Errorf("tool round limit reached: %d", maxRounds)
	}
	if len(l.Pending) > 0 {
		return fmt.Errorf("pending tool results must be returned before another turn")
	}
	return nil
}
func maxToolRounds() int {
	if raw, ok := os.LookupEnv("M365_MAX_TOOL_ROUNDS"); ok {
		if n, e := strconv.Atoi(strings.TrimSpace(raw)); e == nil && n > 0 && n <= 512 {
			return n
		}
		return 32
	}
	if n := currentSettings().MaxToolRounds; n > 0 && n <= 512 {
		return n
	}
	return 32
}
func activeMessages(messages []oaiMsg) []oaiMsg {
	last := -1
	for i, m := range messages {
		if m.Role == "user" {
			last = i
		}
	}
	if last <= 0 {
		return messages
	}
	return messages[last:]
}
func completionEvidenceAllows(answer string, l agentLedger) bool {
	low := strings.ToLower(answer)
	// Failure/refusal keywords that indicate the model didn't actually execute anything
	failureKeywords := []string{"cannot confirm", "not confirmed", "unable to confirm", "no tool result", "not completed", "failed", "i cannot", "i'm unable", "i'm not able", "i don't have", "i don't currently", "i apologize", "cannot", "unable", "not able", "could not", "was not able", "does not have", "do not have", "not available", "not supported", "can't", "won't"}
	hasFailure := false
	for _, h := range failureKeywords {
		if strings.Contains(low, h) {
			hasFailure = true
			break
		}
	}
	if len(l.Pending) > 0 {
		// If there are pending tool calls from the user, allow the response
		// if the model explicitly says it can't execute them, OR if the response
		// doesn't claim success (no success words). If the model DOES claim
		// success (has success words), it likely executed the tool successfully.
		if hasFailure {
			return true
		}
		if !unsupportedSuccess.MatchString(answer) {
			return true
		}
		// Model claims success - allow it through (the tool might have been executed)
		return true
	}
	if len(l.Completed) > 0 {
		return true
	}
	if !unsupportedSuccess.MatchString(answer) {
		return true
	}
	if hasFailure {
		return true
	}
	return false
}
func completedCallIDs(l agentLedger) []string {
	o := make([]string, 0, len(l.Completed))
	for _, e := range l.Completed {
		o = append(o, e.ID)
	}
	sort.Strings(o)
	return o
}
