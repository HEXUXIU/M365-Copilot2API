# Cross-Protocol Router Instructions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve request-level instructions during tool planning for native Responses, routed Chat Completions, and routed Anthropic Messages without changing public protocol shapes, affinity, or persistent state.

**Architecture:** All three adapters already normalize request instructions into `oaiReq.Messages` with `system` or `developer` roles. A single router helper will extract those normalized messages in order, compact them to 16 KiB, and prepend them to the active-turn planning input while leaving old user/assistant history excluded.

**Tech Stack:** Go 1.24, OpenAI Responses and Chat Completions adapters, Anthropic Messages adapter, M365 model router, Docker Compose, Redis 7.

---

### Task 1: Add Failing Cross-Protocol Router Tests

**Files:**
- Modify: `internal/web/router_conversation_test.go`

- [ ] **Step 1: Replace the system-dropping expectation with protocol instruction preservation**

Add a table test that constructs normalized inputs through each public adapter and passes the resulting messages to `routerPlanningInput`:

```go
func TestRouterPlanningInputKeepsCrossProtocolInstructions(t *testing.T) {
	responses, err := (responsesRequest{
		Instructions: "RESPONSES_MARKER exact-response-path",
		Input:        "run the requested tool",
	}).openAI()
	if err != nil {
		t.Fatal(err)
	}
	anthropic, err := (anthropicRequest{
		System: []any{{"type": "text", "text": "ANTHROPIC_MARKER exact-anthropic-path"}},
		Messages: []anthropicMessage{{Role: "user", Content: "run the requested tool"}},
	}).openAI()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		messages []oaiMsg
		want     []string
	}{
		{"responses", responses.Messages, []string{"[request instructions]", "RESPONSES_MARKER", "run the requested tool"}},
		{"chat", []oaiMsg{
			{Role: "system", Content: "CHAT_SYSTEM_MARKER"},
			{Role: "developer", Content: "CHAT_DEVELOPER_MARKER"},
			{Role: "user", Content: "old request"},
			{Role: "assistant", Content: "old answer"},
			{Role: "user", Content: "run the current tool"},
		}, []string{"CHAT_SYSTEM_MARKER", "CHAT_DEVELOPER_MARKER", "run the current tool"}},
		{"anthropic", anthropic.Messages, []string{"ANTHROPIC_MARKER", "run the requested tool"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			full, attachments := flattenPromptMessages(tc.messages, nil)
			prompt, _ := routerPlanningInput(full, attachments, nil, tc.messages, nil, false)
			for _, want := range tc.want {
				if !strings.Contains(prompt, want) {
					t.Fatalf("router prompt missing %q: %q", want, prompt)
				}
			}
			if strings.Contains(prompt, "old request") || strings.Contains(prompt, "old answer") {
				t.Fatalf("router prompt retained old conversation: %q", prompt)
			}
		})
	}
}
```

- [ ] **Step 2: Add order, whitespace, and size tests**

```go
func TestRouterPlanningInstructionsPreserveOrderAndBounds(t *testing.T) {
	messages := []oaiMsg{
		{Role: "system", Content: "SYSTEM_FIRST"},
		{Role: "developer", Content: "DEVELOPER_SECOND"},
		{Role: "system", Content: "   "},
	}
	got := routerPlanningInstructions(messages)
	if strings.Index(got, "SYSTEM_FIRST") >= strings.Index(got, "DEVELOPER_SECOND") {
		t.Fatalf("instruction order changed: %q", got)
	}
	if strings.Count(got, "[system]") != 1 || strings.Count(got, "[developer]") != 1 {
		t.Fatalf("empty instruction was not omitted: %q", got)
	}

	long := "HEAD_MARKER" + strings.Repeat("x", maxRouterInstructionsBytes*2) + "TAIL_MARKER"
	bounded := routerPlanningInstructions([]oaiMsg{{Role: "system", Content: long}})
	if len(bounded) > maxRouterInstructionsBytes || !strings.Contains(bounded, "HEAD_MARKER") || !strings.Contains(bounded, "TAIL_MARKER") {
		t.Fatalf("bounded instructions invalid: len=%d", len(bounded))
	}
}
```

- [ ] **Step 3: Run the focused tests and verify RED**

Run:

```powershell
go test ./internal/web -run 'TestRouterPlanning(InputKeepsCrossProtocolInstructions|InstructionsPreserveOrderAndBounds)' -count=1
```

Expected: build failure because `routerPlanningInstructions` and `maxRouterInstructionsBytes` do not exist, plus the existing router input still omits system/developer markers.

### Task 2: Implement The Shared Bounded Instruction Prefix

**Files:**
- Modify: `internal/web/router_conversation.go`
- Test: `internal/web/router_conversation_test.go`

- [ ] **Step 1: Add the 16 KiB extractor**

Add:

```go
const maxRouterInstructionsBytes = 16 << 10

func routerPlanningInstructions(messages []oaiMsg) string {
	var b strings.Builder
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "system" && role != "developer" {
			continue
		}
		text, _ := parseContent(message.Content)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "[%s]\n%s\n", role, text)
	}
	return compactToolResult(b.String(), maxRouterInstructionsBytes)
}

func prependRouterPlanningInstructions(prompt string, messages []oaiMsg) string {
	instructions := routerPlanningInstructions(messages)
	if instructions == "" {
		return prompt
	}
	if strings.TrimSpace(prompt) == "" {
		return "[request instructions]\n" + instructions
	}
	return "[request instructions]\n" + instructions + "\n\n" + prompt
}
```

Import `fmt` alongside the existing `strings` import.

- [ ] **Step 2: Apply the prefix at every router input return**

In `routerPlanningInput`, wrap the incremental, active-turn, and full-prompt fallback strings with:

```go
prependRouterPlanningInstructions(compactToolResult(prompt, maxRouterPromptBytes), messages)
```

Keep attachment selection and the existing 128 KiB active-turn limit unchanged.

- [ ] **Step 3: Run focused tests and verify GREEN**

Run:

```powershell
go test ./internal/web -run 'TestRouterPlanning' -count=1
```

Expected: PASS.

- [ ] **Step 4: Run adapter and tool protocol regressions**

Run:

```powershell
go test ./internal/web -run 'Test(Responses|Anthropic|Claude|Router|Tool)' -count=1
```

Expected: PASS with no tool identity, adapter, or router regression.

### Task 3: Verify The Repository And Commit The Fix

**Files:**
- Modify: `docs/superpowers/specs/2026-08-31-cross-protocol-router-instructions-design.md`
- Modify: `docs/superpowers/plans/2026-08-31-cross-protocol-router-instructions.md`
- Modify: `internal/web/router_conversation.go`
- Modify: `internal/web/router_conversation_test.go`

- [ ] **Step 1: Run the full local gates**

```powershell
go test ./... -count=1
go test -race ./internal/web -count=1
go vet ./...
go build ./...
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 2: Scan the staged diff for credentials**

Stage only the four listed files, inspect `git diff --cached`, and reject the commit if it contains API keys, passwords, tokens, account emails, or private host credentials.

- [ ] **Step 3: Commit**

```powershell
git commit -m "fix(router): preserve cross-protocol instructions"
```

Expected: one conventional commit containing the implementation, tests, updated spec, and implementation plan.

### Task 4: Deploy With A Low-Interruption Gateway Swap

**Files:**
- Use: `/opt/m365-copilot2api/docker-compose.yml`
- Create: timestamped backup under `/opt/m365-copilot2api/backups/`

- [ ] **Step 1: Record the current production identity and health**

Record the gateway image ID, container ID, start time, restart count, 31 account states, Redis persistence status, and the current compose SHA-256.

- [ ] **Step 2: Build a commit-addressed image**

Build `m365-copilot2api:production-super-<commit>` from the committed source on the VPS. Do not recreate Redis, Sub2API, PostgreSQL, the subscription proxy, or their volumes.

- [ ] **Step 3: Swap only the gateway container**

Update the gateway image tag and run a gateway-only Compose replacement. Measure the unavailable interval and require it to stay below five seconds. Restore the previous compose file and image immediately on failed health checks.

### Task 5: Run Three-Protocol Production Acceptance

**Files:**
- Create: `docs/deployments/2026-08-31-cross-protocol-tool-acceptance.md`

- [ ] **Step 1: Test native Responses**

Put an exact path, title, and marker only in `instructions`; keep the user input generic. Require a structured function call and a custom PowerShell call, execute both on the real host, return results through the original `call_id`, and continue through `previous_response_id`. Run non-stream and SSE variants.

- [ ] **Step 2: Test routed Chat Completions**

Put different exact values only in system and developer messages. Verify `assistant.tool_calls`, unique IDs, real host execution, `role=tool` result return, final answer, non-stream and SSE finish reasons, and cached-token reporting.

- [ ] **Step 3: Test routed Anthropic Messages**

Put different exact values only in string and text-block `system` content. Verify `tool_use`, unique `tool_use_id`, real host execution, `tool_result` continuation, `stop_reason`, non-stream and SSE content-block ordering, and cache usage fields.

- [ ] **Step 4: Repeat ambiguous cases with different meaningful inputs**

For every mismatch, inspect root cause and run two new probes with different real documents, paths, or questions. Do not repeat malformed arguments unchanged.

### Task 6: Audit Production And Push The Locked Branch

**Files:**
- Update: `docs/deployments/2026-08-31-cross-protocol-tool-acceptance.md`

- [ ] **Step 1: Audit health and persistence**

Verify 31/31 accounts enabled and online, Redis AOF/RDB healthy, zero gateway restarts, zero new HTTP 500/502, panic/fatal, tool identity, alias, or response failure errors, and zero public `X-M365-*` headers.

- [ ] **Step 2: Record protocol and cache evidence**

Record exact first-turn argument fidelity, call IDs, real result summaries, TTFT, total latency, input/cached tokens, binding/account continuity, and rollback artifacts for all three formats.

- [ ] **Step 3: Commit the acceptance report and push**

```powershell
git add docs/deployments/2026-08-31-cross-protocol-tool-acceptance.md
git commit -m "test(router): record cross-protocol acceptance"
git push origin refs/heads/production-super-20260831:refs/heads/production-super-20260831
```

Expected: the private production branch advances without force-push and contains no credentials.
