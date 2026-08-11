# Cache Usage Accuracy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Report cached tokens as the tokenizer-counted difference between the complete flattened prompt and the incremental prompt actually sent to M365.

**Architecture:** Replace the request-scoped reused-message counter with an immutable full/sent prompt snapshot. Chat and Responses serializers derive cached tokens from that snapshot with the same model tokenizer, while resolver wiring records the snapshot only after a non-empty incremental suffix becomes the actual upstream answer prompt.

**Tech Stack:** Go, `net/http` request contexts, `sync/atomic`, `tiktoken` `o200k_base`, OpenAI Chat Completions and Responses JSON/SSE, Docker Compose.

---

### Task 1: Prompt-delta capture

**Files:**
- Modify: `internal/web/request_usage_test.go`
- Modify: `internal/web/request_usage.go`

- [ ] **Step 1: Write failing capture tests**

Replace reused-message assertions with prompt-delta cases that cover strict
prefix reuse, new sessions, similarity fallback, full-prompt resend, and an
empty incremental prompt. The positive case must assert:

```go
count, _ := tokenEstimator("gpt-5.6-sol")
want := count(fullPrompt) - count(sentPrompt)
if got := capture.CachedTokens("gpt-5.6-sol"); got != want {
	 t.Fatalf("cached tokens=%d want=%d", got, want)
}
```

- [ ] **Step 2: Run the capture tests and verify RED**

Run:

```bash
go test ./internal/web -run 'TestRequestUsageCapture|TestRecordResolvedCacheUsage' -count=1
```

Expected: compilation fails because `CachedTokens` and prompt-delta recording
do not exist yet.

- [ ] **Step 3: Implement the immutable prompt snapshot**

Use an atomically published immutable value:

```go
type cachePromptDelta struct {
	full string
	sent string
}

type requestUsageCapture struct {
	promptDelta atomic.Pointer[cachePromptDelta]
}
```

Add `RecordPromptDelta(full, sent string)` and `CachedTokens(model string) int`.
Reject empty/equal prompts and return `max(count(full)-count(sent), 0)`.
Change `recordResolvedCacheUsage` to accept `fullPrompt` and `sentPrompt` and
record only when `0 < HistoryLen < totalMessages`.

- [ ] **Step 4: Run the capture tests and verify GREEN**

Run the command from Step 2. Expected: PASS.

- [ ] **Step 5: Commit the capture change**

```bash
git add internal/web/request_usage.go internal/web/request_usage_test.go
git commit -m "fix(api): measure cached prompt delta"
```

### Task 2: Shared tokenizer usage

**Files:**
- Modify: `internal/web/request_usage_test.go`
- Modify: `internal/web/request_usage.go`
- Modify: `internal/web/codex_usage.go`

- [ ] **Step 1: Write failing estimator tests**

Add mixed Chinese/ASCII prompts and assert that both protocols expose the same
captured delta:

```go
want := count(fullPrompt) - count(sentPrompt)
chat := requestChatCompletionUsage(r, "gpt-5.6-sol", fullPrompt, "完成")
responses := requestResponsesUsage(r, "gpt-5.6-sol", messages, tools, "auto", "完成")
```

Also assert `prompt_tokens == count(fullPrompt)` for Chat and cached tokens are
clamped to each protocol's total input.

- [ ] **Step 2: Run estimator tests and verify RED**

```bash
go test ./internal/web -run 'Test(ChatCompletionUsage|RequestResponsesUsage).*PromptDelta' -count=1
```

Expected: compilation or assertion failure because Chat still uses
`EstimateTokens` and both protocols still consume reused-message counts.

- [ ] **Step 3: Use one model tokenizer for totals and cache**

Change Chat estimation to accept `model` and use `tokenEstimator(model)` for
the complete prompt and completion. Pass `capture.CachedTokens(model)` into
Chat and Responses estimators, clamp it to total input, and remove the
reused-message summation path.

- [ ] **Step 4: Run estimator tests and verify GREEN**

Run the command from Step 2. Expected: PASS.

- [ ] **Step 5: Commit the estimator change**

```bash
git add internal/web/request_usage.go internal/web/request_usage_test.go internal/web/codex_usage.go
git commit -m "fix(api): unify cache token estimation"
```

### Task 3: Record only the prompt actually saved

**Files:**
- Modify: `internal/web/server.go`
- Modify: `internal/web/request_usage_test.go`

- [ ] **Step 1: Add the empty-suffix regression test**

Exercise `recordResolvedCacheUsage` with a strict prefix but an empty or equal
sent prompt and assert `CachedTokens` remains zero. Keep the positive case with
a non-empty, different incremental prompt.

- [ ] **Step 2: Run the regression test and verify RED**

```bash
go test ./internal/web -run TestRecordResolvedCacheUsage -count=1
```

Expected: FAIL until prompt validity is part of the recording contract.

- [ ] **Step 3: Move recording after incremental selection**

In `openaiChat`, remove the early resolver-only recording call. After
`incPrompt` is trimmed and assigned to `answerPrompt`, call:

```go
recordResolvedCacheUsage(r, resolved, len(body.Messages), prompt, answerPrompt)
```

Update every `requestChatCompletionUsage` call to pass the selected model and
complete prompt. Preserve existing stream, non-stream, text, and tool exits.

- [ ] **Step 4: Run focused protocol tests**

```bash
go test ./internal/web -run 'Test(RecordResolvedCacheUsage|WriteStreamFinishIncludesCacheUsage|WriteToolResponseIncludesCacheUsage|RequestResponsesUsage)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the resolver wiring**

```bash
git add internal/web/server.go internal/web/request_usage_test.go
git commit -m "fix(api): record only sent prompt reuse"
```

### Task 4: Regression and live verification

**Files:**
- Modify only Task 1-3 files if verification reveals a defect.
- Create on server: `/opt/m365-copilot2api/releases/ce773ee-cacheusage.2/`
- Modify on server: `/opt/m365-copilot2api/docker-compose.yml`

- [ ] **Step 1: Format and run all local checks**

```bash
gofmt -w internal/web/request_usage.go internal/web/request_usage_test.go internal/web/codex_usage.go internal/web/server.go
go test ./internal/web -count=1
go test -race ./internal/web -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

Expected: every command exits zero.

- [ ] **Step 2: Build and smoke-test a versioned image**

Cross-compile Linux/amd64 from the exact commit, build
`m365-copilot2api:ce773ee-cacheusage.2`, and start a temporary container on a
loopback-only port. Expected: HTTP 200 and binary/image SHA verification passes.

- [ ] **Step 3: Deploy with rollback**

Back up Compose, switch only the M365 image, run `docker compose config -q`,
recreate the service, and verify running state, HTTP 200, restart count zero,
and no recent panic/fatal/runtime errors.

- [ ] **Step 4: Verify deterministic two-turn deltas**

Send a mixed Chinese/ASCII first turn and a strict-prefix continuation. Compute
the expected delta independently with the repository tokenizer fixture. Verify
the direct M365 response field equals it, then repeat through Sub2API and verify
the newest `usage_logs.cache_read_tokens` row equals the same value.

- [ ] **Step 5: Push the completed commits**

Push to `2164312714-svg/M365-Copilot2API:main`, update upstream PR #15, and
verify the PR head SHA matches local `HEAD`.
