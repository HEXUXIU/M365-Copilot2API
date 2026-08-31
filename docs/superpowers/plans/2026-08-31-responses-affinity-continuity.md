# Responses Affinity Continuity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep a Responses tool-call chain on its healthy original account and upstream conversation when the client uses `previous_response_id` without an explicit `X-M365-Session-Id`.

**Architecture:** Preserve the existing response-state namespace and explicit-session behavior. Derive a separate affinity reference in priority order `previous_response_id`, explicit session id, generated public response id; pass that reference through the internal OpenAI adapter; bind every completed public response id to the verified affinity binding. The scheduler remains authoritative and may still migrate on authentication failure, quota cooldown, or another hard account failure.

**Tech Stack:** Go 1.24, `net/http`, in-memory/Redis affinity stores, OpenAI Responses compatibility adapter, Docker Compose, Redis 7.

**Approved design:** `outputs/2026-08-30-cache-first-routing-design.md`, especially sections 4, 6, 7, 8, and 14.

---

### Task 1: Reproduce a no-session response chain

**Files:**
- Modify: `internal/web/affinity_manager_test.go`
- Modify: `internal/web/responses_stream_adapter_test.go`

- [x] **Step 1: Write the failing non-stream affinity-chain test**

Add a test that creates two healthy accounts, prepares a first Responses request with no explicit session, completes it on one account, binds `resp-first`, continues through `resp-second`, and asserts all three requests resolve the same binding, account, conversation id, and session id.

```go
func TestResponsesPreviousResponseChainKeepsBindingWithoutExplicitSession(t *testing.T) {
	manager := openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second})
	defer manager.close()
	ctx := context.Background()
	accounts := []auth.AccountToken{{ID: "a"}, {ID: "b"}}
	available := func(string) bool { return true }

	firstRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	firstReference := prepareResponsesAffinity(firstRequest, "", "", "resp-first")
	firstBody := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "inspect the workspace"}}}
	first, err := manager.begin(ctx, "tenant", firstBody, firstRequest, accounts, available)
	if err != nil {
		t.Fatal(err)
	}
	first.apply(firstBody)
	first.complete(ctx, firstBody, first.accountID, "conv-a", "sess-a", oaiMsg{Role: "assistant", Content: "done"}, 100, 4)
	firstAccount := first.accountID
	firstBinding := first.key.BindingID
	first.close()
	manager.bindResponse(ctx, "tenant", "resp-first", firstReference)

	secondRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	secondReference := prepareResponsesAffinity(secondRequest, "resp-first", "", "resp-second")
	secondBody := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "inspect the workspace"}, {Role: "assistant", Content: "done"}, {Role: "user", Content: "continue"}}}
	second, err := manager.begin(ctx, "tenant", secondBody, secondRequest, accounts, available)
	if err != nil {
		t.Fatal(err)
	}
	second.apply(secondBody)
	if !second.incremental || second.accountID != firstAccount || second.binding.ID != firstBinding || secondBody.ConversationID != "conv-a" || secondBody.SessionID != "sess-a" {
		t.Fatalf("first continuation lost affinity: state=%s body=%+v", second, secondBody)
	}
	second.complete(ctx, secondBody, second.accountID, "conv-a", "sess-a", oaiMsg{Role: "assistant", Content: "continued"}, 120, 4)
	second.close()
	manager.bindResponse(ctx, "tenant", "resp-second", secondReference)

	thirdRequest := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	prepareResponsesAffinity(thirdRequest, "resp-second", "", "resp-third")
	thirdBody := &oaiReq{Messages: []oaiMsg{{Role: "user", Content: "next"}}}
	third, err := manager.begin(ctx, "tenant", thirdBody, thirdRequest, accounts, available)
	if err != nil {
		t.Fatal(err)
	}
	defer third.close()
	third.apply(thirdBody)
	if !third.incremental || third.accountID != firstAccount || third.binding.ID != firstBinding || thirdBody.ConversationID != "conv-a" || thirdBody.SessionID != "sess-a" {
		t.Fatalf("second continuation lost affinity: state=%s body=%+v", third, thirdBody)
	}
}
```

- [x] **Step 2: Run the focused test and verify RED**

Run:

```powershell
go test ./internal/web -run TestResponsesPreviousResponseChainKeepsBindingWithoutExplicitSession -count=1
```

Expected: build failure because `prepareResponsesAffinity` does not exist on `91fa9b6`.

- [x] **Step 3: Write the failing SSE alias test**

Add a test that creates a verified root binding, sets `X-M365-Previous-Response-Id: resp-root`, runs the stream adapter with no explicit session, and asserts `resp-child` becomes a verified response alias.

```go
func TestStreamResponsesAdapterBindsAliasFromPreviousResponseHeader(t *testing.T) {
	s := newResponsesAdapterTestServer()
	s.affinity = openAffinityManager(affinityConfig{Mode: affinityEnforce, TTL: time.Hour, MaxSessions: 100, LockTTL: time.Minute, LockWait: time.Second})
	defer s.affinity.close()
	tenant := "tenant"
	tenantHash := normalizeAffinityTenantHash(tenant)
	rootHash := affinityExplicitHash(tenantHash, "previous_response", "resp-root")
	if err := s.affinity.fallback.PutBinding(context.Background(), affinityBinding{ID: rootHash, TenantHash: tenantHash, AccountID: "a", ConversationID: "conv-a", SessionID: "sess-a"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	r.Header.Set(previousResponseHeader, "resp-root")
	w := httptest.NewRecorder()
	if ok := s.streamResponsesAdapterWithRunner(w, r, oaiReq{}, "gpt-5.6-sol", "resp-child", "", responseNamespace(tenant, ""), responsesInnerStream(
		`{"choices":[{"delta":{"content":"done"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	)); !ok {
		t.Fatalf("stream failed: %s", w.Body.String())
	}
	if !s.affinity.hasResponseBinding(context.Background(), tenant, "resp-child") {
		t.Fatal("stream completion did not bind the child response alias")
	}
}
```

- [x] **Step 4: Run the SSE test and verify RED**

Run:

```powershell
go test ./internal/web -run TestStreamResponsesAdapterBindsAliasFromPreviousResponseHeader -count=1
```

Expected: FAIL because the current adapter passes the empty explicit session id to `bindResponse` and skips the alias.

### Task 2: Separate response namespace identity from affinity identity

**Files:**
- Modify: `internal/web/protocol_handlers.go:55`
- Modify: `internal/web/protocol_handlers.go:586`
- Modify: `internal/web/protocol_handlers.go:702`
- Modify: `internal/web/protocol_handlers.go:834`

- [x] **Step 1: Implement the request affinity reference**

Add the helper below. It leaves `X-M365-Session-Id` as the response-state namespace identity while using a distinct previous-response affinity key.

```go
func prepareResponsesAffinity(r *http.Request, previousResponseID, sessionID, publicResponseID string) string {
	referenceID := firstNonEmpty(previousResponseID, sessionID, publicResponseID)
	r.Header.Set(sessionHeaderName, sessionID)
	r.Header.Set(previousResponseHeader, referenceID)
	return referenceID
}
```

In `responses`, replace the two direct header assignments with:

```go
	affinityReferenceID := prepareResponsesAffinity(r, body.PreviousResponseID, affinitySessionID, publicID)
```

- [x] **Step 2: Bind SSE completion to the prepared reference**

Replace the stream completion alias source with the prepared request header while keeping the stored `RespNode.SessionID` unchanged:

```go
	if s.affinity != nil {
		s.affinity.bindResponse(r.Context(), s.affinityTenantIdentity(r), id, r.Header.Get(previousResponseHeader))
	}
```

- [x] **Step 3: Bind non-stream completion before publishing the result**

Immediately after the successful `RespNode` persistence, record the public response alias through the same verified manager path:

```go
	if s.affinity != nil {
		s.affinity.bindResponse(r.Context(), s.affinityTenantIdentity(r), publicID, affinityReferenceID)
	}
```

- [x] **Step 4: Run focused tests and verify GREEN**

Run:

```powershell
go test ./internal/web -run 'TestResponsesPreviousResponseChainKeepsBindingWithoutExplicitSession|TestStreamResponsesAdapterBindsAliasFromPreviousResponseHeader|TestPreviousResponseAliasResolvesCloudBinding|TestBindResponseSkipsAliasWhenBindingCannotBeVerified' -count=1
```

Expected: PASS.

- [x] **Step 5: Commit the implementation**

```powershell
git add internal/web/protocol_handlers.go internal/web/affinity_manager_test.go internal/web/responses_stream_adapter_test.go docs/superpowers/plans/2026-08-31-responses-affinity-continuity.md
git commit -m "fix(responses): preserve affinity across tool continuations"
```

### Task 3: Run the local quality gate

**Files:**
- Verify: all Go packages and the committed diff

- [x] **Step 1: Run package tests**

```powershell
go test ./... -count=1
```

Expected: PASS.

- [x] **Step 2: Run race tests on shared routing state**

```powershell
go test -race ./internal/web ./internal/chathub -count=1
```

Expected: PASS with no race report.

- [x] **Step 3: Run static and build checks**

```powershell
go vet ./...
go build ./...
git diff --check HEAD^
```

Expected: all commands exit 0 and `git diff --check` prints no diagnostics.

### Task 4: Deploy without touching stateful services

**Files:**
- Verify: `/opt/m365-copilot2api/docker-compose.yml`
- Create: `/opt/m365-copilot2api/backups/<commit>-predeploy/`

- [x] **Step 1: Push the locked production branch**

```powershell
git push origin production-super-20260831
```

Expected: the new commit becomes the branch head and the push does not rewrite history.

- [x] **Step 2: Capture rollback material on the VPS**

Save the compose file, gateway container inspect output, current image inspect output, account/Redis health summaries, and SHA-256 checksums under a commit-specific backup directory.

- [x] **Step 3: Build a commit-tagged gateway image**

Build `m365-copilot2api:production-super-<commit>` from the pushed source and verify its image id before editing Compose.

- [x] **Step 4: Recreate only the gateway container**

Update only the gateway image reference and run:

```bash
docker compose up -d --no-deps --force-recreate m365-copilot2api
```

Expected: Redis, Sub2API, PostgreSQL, subscription proxy, normalizer, volumes, accounts, and sessions keep their existing container ids and start times.

### Task 5: Repeat production acceptance with real workloads

**Files:**
- Create: `docs/deployments/2026-08-31-responses-affinity-acceptance.md`

- [x] **Step 1: Run a real PowerShell custom-tool chain**

Ask the model to inspect the working directory, execute the emitted PowerShell command locally, return the real tool output with `previous_response_id`, and assert HTTP 200, one stable tool call id, correct final answer, one stable account, and nonzero cached tokens.

- [x] **Step 2: Run a different Linux shell custom-tool chain from the VPS**

Ask the model to inspect a different directory with a Linux shell contract, execute the emitted command on the VPS, return its real output, and assert the same protocol and affinity properties.

- [x] **Step 3: Run function-tool JSON and SSE chains**

Use distinct real questions and function schemas for non-stream and SSE. Assert unique stable call ids, valid argument JSON, successful tool-result continuation, `response.created`, `response.completed`, and the original healthy account.

- [x] **Step 4: Run a real-paper long-context continuation**

Use a published paper not used by the immediate pre-deploy test, ask two substantive questions through `previous_response_id`, and record input tokens, cached tokens, cache percentage, response-header time, first delta time, and completion time.

- [x] **Step 5: Run the final production audit**

Verify `31/31` accounts enabled and online, zero cooldown/auth-failed/rate-limited accounts, Redis AOF/RDB healthy with zero evictions, no public `X-M365-*` response headers, custom cache controls still present, zero new 500/502/panic/fatal/tool-id errors, and a usable rollback image and backup directory.
