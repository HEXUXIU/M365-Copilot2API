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
			e, ok := calls[m.ToolCallID]
			if !ok {
				continue
			}
			e.Result = compactToolResult(contentToString(m.Content), 4000)
			e.Failed = failureSignal.MatchString(e.Result)
			calls[m.ToolCallID] = e
		}
	}
	l := agentLedger{}
	seenFailure := map[string]int{}
	seenCall := map[string]int{}
	for _, id := range order {
		e := calls[id]
		l.ToolRounds++
		callSig := e.Name + "\x00" + e.Arguments
		seenCall[callSig]++
		if seenCall[callSig] >= 3 {
			l.RepeatedCall = true
		}
		if e.Result == "" {
			l.Pending = append(l.Pending, e)
		} else {
			l.Completed = append(l.Completed, e)
			if e.Failed {
				sig := e.Name + "\x00" + e.Arguments + "\x00" + normalizeFailure(e.Result)
				seenFailure[sig]++
				if seenFailure[sig] >= 2 {
					l.RepeatedFailure = true
					l.RepetitionSignature = sig
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
	b, _ := json.Marshal(l)
	hint := "Use this evidence ledger. Never treat a pending call as completed."
	if l.RepeatedFailure {
		hint += " The same call produced the same failure repeatedly; change strategy or request missing information instead of retrying unchanged."
	}
	if l.RepeatedCall {
		hint += " The same call has been issued repeatedly; do not repeat it unchanged unless new evidence explicitly justifies polling. Prefer a different diagnostic action or an honest incomplete result."
	}
	return hint + "\nEVIDENCE_LEDGER: " + string(b)
}
func maxToolRounds() int {
	if raw, exists := os.LookupEnv("M365_MAX_TOOL_ROUNDS"); exists {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && n > 0 && n <= 512 {
			return n
		}
		return 32
	}
	if n := currentSettings().MaxToolRounds; n > 0 && n <= 512 {
		return n
	}
	return 32
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

func completionEvidenceAllows(answer string, l agentLedger) bool {
	if len(l.Pending) > 0 {
		return false
	}
	if len(l.Completed) > 0 {
		return true
	}
	if !unsupportedSuccess.MatchString(answer) {
		return true
	}
	low := strings.ToLower(answer)
	for _, h := range []string{"cannot confirm", "not confirmed", "unable to confirm", "no tool result", "not completed", "failed"} {
		if strings.Contains(low, h) {
			return true
		}
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
