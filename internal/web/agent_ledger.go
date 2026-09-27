package web

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type toolEvidence struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
	Failed    bool   `json:"failed"`
}

type meteringSnapshot struct {
	MeterError         string         `json:"meterError,omitempty"`
	HasAccess          bool           `json:"hasAccess"`
	RemainingAllowance map[string]int `json:"remainingAllowance,omitempty"`
	Timestamp          time.Time      `json:"timestamp"`
}

type agentLedger struct {
	Completed           []toolEvidence     `json:"completed"`
	Pending             []toolEvidence     `json:"pending"`
	ToolRounds          int                `json:"tool_rounds"`
	RepeatedCall        bool               `json:"repeated_call"`
	RepeatedFailure     bool               `json:"repeated_failure"`
	RepetitionSignature string             `json:"repetition_signature,omitempty"`
	StuckLoop           bool               `json:"stuck_loop"`
	Metering            []meteringSnapshot `json:"metering,omitempty"`
}

var failureSignal = regexp.MustCompile(`(?im)(exit\s*(code|status)?\s*[:=]?\s*[1-9]\d*|^\s*(error|failed|failure)(?:\s|:|$)|\bexplicit\s+error\b|exception|traceback|timed?\s*out|permission denied|not found|refused)`)
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

// scopedCallID returns a globally unique tool call id. The scope parameters
// are kept for signature compatibility with callers that pass per-turn
// context; the id itself must not depend on call content or scope text,
// otherwise repeating the same tool+arguments across turns collides
// (duplicate tool call id errors from clients).
func scopedCallID(name, args string, index int, scope string) string {
	return "call_" + uuid.NewString()
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
				e.Failed = !strings.HasPrefix(e.Result, "Success. Updated the following files:") && failureSignal.MatchString(e.Result)
				calls[m.ToolCallID] = e
			}
		}
	}
	l := agentLedger{}
	seenCall := map[string]int{}
	var consecutiveFailureSignature string
	consecutiveFailures := 0
	for _, id := range order {
		e := calls[id]
		l.ToolRounds++
		sig := e.Name + "\x00" + e.Arguments
		seenCall[sig]++
		if seenCall[sig] >= 3 {
			l.RepeatedCall = true
			l.RepetitionSignature = sig
		}
		if seenCall[sig] >= 5 {
			l.StuckLoop = true
		}
		if e.Result == "" {
			l.Pending = append(l.Pending, e)
		} else {
			l.Completed = append(l.Completed, e)
			if e.Failed {
				fs := e.Name + "\x00" + canonicalToolArguments(e.Arguments)
				if fs == consecutiveFailureSignature {
					consecutiveFailures++
				} else {
					consecutiveFailureSignature = fs
					consecutiveFailures = 1
				}
				if consecutiveFailures >= 3 {
					l.RepeatedFailure = true
					l.RepetitionSignature = fs
				}
				if consecutiveFailures >= 5 {
					l.StuckLoop = true
				}
			} else {
				consecutiveFailureSignature = ""
				consecutiveFailures = 0
			}
		}
	}
	return l
}
func normalizeFailure(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`\d+`).ReplaceAllString(s, "#")
	if len(s) > 500 {
		cut := 500
		for cut > 0 && cut < len(s) && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
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
func recordMetering(l *agentLedger, meterError string, hasAccess bool, remaining map[string]int) {
	if l == nil {
		return
	}
	snap := meteringSnapshot{
		MeterError:         meterError,
		HasAccess:          hasAccess,
		RemainingAllowance: remaining,
		Timestamp:          time.Now(),
	}
	l.Metering = append(l.Metering, snap)
}

func (l agentLedger) CanContinue(maxRounds int) error {
	if maxRounds <= 0 {
		maxRounds = 32
	}
	if l.ToolRounds >= maxRounds {
		return fmt.Errorf("tool round limit reached: %d", maxRounds)
	}
	if l.StuckLoop {
		return fmt.Errorf("stuck tool loop detected: same call repeated 3+ times")
	}
	if l.RepeatedFailure {
		return fmt.Errorf("repeated tool failure detected: %s", l.RepetitionSignature)
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
	if len(l.Pending) > 0 {
		return false
	}
	if len(l.Completed) == 0 && len(l.Pending) == 0 {
		return !unsupportedSuccess.MatchString(answer)
	}
	low := strings.ToLower(answer)
	failureKeywords := []string{"cannot confirm", "not confirmed", "unable to confirm", "no tool result", "no matching tool results were returned", "no external action has been verified"}
	hasFailure := false
	for _, h := range failureKeywords {
		if strings.Contains(low, h) {
			hasFailure = true
			break
		}
	}
	if len(l.Completed) > 0 {
		return !hasFailure
	}
	if unsupportedSuccess.MatchString(answer) {
		return false
	}
	return true
}
func completedCallIDs(l agentLedger) []string {
	o := make([]string, 0, len(l.Completed))
	for _, e := range l.Completed {
		o = append(o, e.ID)
	}
	sort.Strings(o)
	return o
}
