package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"m365-copilot2api/internal/auth"
	"m365-copilot2api/internal/chathub"
)

func TestRouterConversationReuseLifecycle(t *testing.T) {
	body := &oaiReq{ConversationID: "conv-old", SessionID: "sess-old"}
	state := newRouterConversation(true, body)
	req := chathub.Request{Text: "route"}
	state.apply(&req)
	if req.ConversationID != "conv-old" || req.SessionID != "sess-old" {
		t.Fatalf("router request lost existing conversation: %+v", req)
	}

	res := chathub.Result{ConversationID: "conv-new", SessionID: "sess-new"}
	if transient := state.accept(&res); transient != "" {
		t.Fatalf("reused router marked conversation transient: %q", transient)
	}
	state.adopt(body)
	if body.ConversationID != "conv-new" || body.SessionID != "sess-new" {
		t.Fatalf("router result was not adopted: %+v", body)
	}

	state.reset()
	req = chathub.Request{Text: "cold replay"}
	state.apply(&req)
	if req.ConversationID != "" || req.SessionID != "" {
		t.Fatalf("reset router retained stale cloud IDs: %+v", req)
	}
}

func TestRouterConversationWithoutReuseIsTransient(t *testing.T) {
	state := newRouterConversation(false, &oaiReq{ConversationID: "public-conv", SessionID: "public-sess"})
	req := chathub.Request{Text: "route"}
	state.apply(&req)
	if req.ConversationID != "" || req.SessionID != "" {
		t.Fatalf("one-shot router inherited public conversation: %+v", req)
	}
	res := chathub.Result{ConversationID: "transient-conv", SessionID: "transient-sess"}
	if transient := state.accept(&res); transient != "transient-conv" {
		t.Fatalf("transient conversation=%q", transient)
	}
}

func TestRouterPlanningInputUsesIncrementalSuffix(t *testing.T) {
	toolCall := map[string]any{
		"id": "call-1", "type": "function",
		"function": map[string]any{"name": "lookup", "arguments": `{"q":"cache"}`},
	}
	messages := []oaiMsg{
		{Role: "system", Content: "large stable system prompt"},
		{Role: "user", Content: "first request"},
		{Role: "assistant", ToolCalls: []map[string]any{toolCall}},
		{Role: "tool", ToolCallID: "call-1", Content: "fresh tool output"},
	}
	fullAttachments := []chathub.Attachment{{Type: "image", Name: "old.png"}}
	explicitAttachments := []chathub.Attachment{{Type: "file", Name: "current.txt"}}
	affinity := &affinityRequest{enforced: true, incremental: true, prefixCount: 3}

	prompt, attachments := routerPlanningInput("FULL PROMPT", fullAttachments, explicitAttachments, messages, affinity, true)
	if !strings.Contains(prompt, "fresh tool output") || strings.Contains(prompt, "large stable system prompt") || strings.Contains(prompt, "first request") {
		t.Fatalf("router prompt is not the incremental suffix: %q", prompt)
	}
	if len(attachments) != 1 || attachments[0].Name != "current.txt" {
		t.Fatalf("incremental attachments=%+v", attachments)
	}

	prompt, attachments = routerPlanningInput("FULL PROMPT", fullAttachments, explicitAttachments, messages, affinity, false)
	if prompt != "FULL PROMPT" || len(attachments) != 1 || attachments[0].Name != "old.png" {
		t.Fatalf("one-shot router did not preserve full input: prompt=%q attachments=%+v", prompt, attachments)
	}
}

func TestRouterPlanningInputBoundsLongHistoryToActiveTurn(t *testing.T) {
	messages := []oaiMsg{
		{Role: "system", Content: strings.Repeat("old-system ", 20000)},
		{Role: "user", Content: strings.Repeat("old-request ", 20000)},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "Create current.txt in the workspace."},
	}
	full, attachments := flattenPromptMessages(messages, nil)
	prompt, gotAttachments := routerPlanningInput(full, attachments, nil, messages, nil, false)
	if len(prompt) > maxRouterPromptBytes+200 || !strings.Contains(prompt, "current.txt") || strings.Contains(prompt, "old-system") {
		t.Fatalf("router prompt was not bounded to the active turn: len=%d prompt=%q", len(prompt), prompt)
	}
	if len(gotAttachments) != 0 {
		t.Fatalf("unexpected attachments: %+v", gotAttachments)
	}
}

func TestRouterToolConversationCachesAcrossTenTurns(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second})
	defer manager.close()
	ctx := context.Background()
	accounts := []auth.AccountToken{{ID: "account-a"}, {ID: "account-b"}}
	messages := []oaiMsg{{Role: "system", Content: strings.Repeat("stable context ", 500)}, {Role: "user", Content: "start the task"}}
	accountID := ""
	previousCached := int64(0)

	for turn := 0; turn < 10; turn++ {
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		req.Header.Set(sessionHeaderName, "router-ten-turns")
		body := &oaiReq{Model: "gpt-5.6-sol", Messages: cloneMessages(messages)}
		state, err := manager.begin(ctx, "tenant", body, req, accounts, func(string) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		state.apply(body)
		if turn == 0 {
			accountID = state.accountID
		} else {
			if state.accountID != accountID || body.ConversationID != "conv-router" || body.SessionID != "sess-router" {
				state.close()
				t.Fatalf("turn %d lost affinity: account=%q body=%+v", turn, state.accountID, body)
			}
			planning, _ := routerPlanningInput("FULL HISTORY", nil, nil, body.Messages, state, true)
			if strings.Contains(planning, "stable context") || !strings.Contains(planning, fmt.Sprintf("tool output %d", turn-1)) {
				state.close()
				t.Fatalf("turn %d did not send only the new suffix: %q", turn, planning)
			}
		}

		callID := fmt.Sprintf("call-%d", turn)
		call := map[string]any{
			"id": callID, "type": "function",
			"function": map[string]any{"name": "lookup", "arguments": fmt.Sprintf(`{"turn":%d}`, turn)},
		}
		prompt, _ := flattenPromptMessages(body.Messages, nil)
		usage := state.complete(ctx, body, state.accountID, "conv-router", "sess-router", oaiMsg{Role: "assistant", ToolCalls: []map[string]any{call}}, EstimateTokens(prompt), 8)
		state.close()
		if turn == 0 {
			if usage.Confirmed || usage.CachedTokens != 0 {
				t.Fatalf("cold turn claimed cache: %+v", usage)
			}
		} else {
			if !usage.Confirmed || usage.CachedTokens <= previousCached {
				t.Fatalf("turn %d cache did not grow: previous=%d usage=%+v", turn, previousCached, usage)
			}
			previousCached = usage.CachedTokens
		}
		messages = append(messages,
			oaiMsg{Role: "assistant", ToolCalls: []map[string]any{call}},
			oaiMsg{Role: "tool", ToolCallID: callID, Content: fmt.Sprintf("tool output %d", turn)},
		)
	}
}
