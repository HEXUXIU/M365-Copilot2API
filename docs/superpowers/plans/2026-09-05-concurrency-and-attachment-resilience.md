# Concurrency And Attachment Resilience Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the `bd74d32` production behavior intact while making attachment transport resilient and increasing safe request concurrency through measured, reversible tuning.

**Architecture:** Finish the bounded retry path in `internal/chathub/client.go` using fresh request bodies and transient-only retry classification. Keep concurrency controls in the existing request gate and per-account AIMD limiter, expose only environment-level changes for production, and raise limits one measured tier at a time. Redis and response-state data remain untouched; every production change is preceded by a snapshot.

**Tech Stack:** Go, `net/http`, Docker Compose, Redis, PowerShell-based remote checks, `go test`, `go test -race`, `go vet`, `go build`.

---

### Task 1: Complete attachment retry implementation

**Files:**
- Modify: `internal/chathub/client.go:20-40,1450-1760`
- Test: `internal/chathub/attachment_upload_test.go:76-123`

- [ ] **Step 1: Run the focused regression tests and confirm the current failure is in production code.**

Run:
```powershell
go test ./internal/chathub -run 'TestUploadAttachmentsRetriesTransient' -count=1
```
Expected: compile failure or a failing retry assertion caused by the incomplete implementation, not by test setup.

- [ ] **Step 2: Remove the obsolete request construction before the upload retry loop.**

Delete the pre-loop `http.NewRequestWithContext` call and its header setup. The only upload request construction must be inside the retry loop so every attempt receives a fresh `strings.NewReader(form.Encode())` body.

- [ ] **Step 3: Use the final retry-loop response values for status handling.**

Replace the stale `resp.StatusCode` references after the loop with `status`, and keep the existing HTTP 400 inline-image fallback. Use `responseHeaders` only for retry metadata and use `data` for the bounded error body.

- [ ] **Step 4: Add the missing `net` import and run the focused tests until green.**

Run:
```powershell
go test ./internal/chathub -run 'TestUploadAttachmentsRetriesTransient' -count=1
```
Expected: both transient upload and transient download tests pass.

- [ ] **Step 5: Commit the isolated attachment fix.**

Run:
```powershell
git add internal/chathub/client.go internal/chathub/attachment_upload_test.go
git commit -m "fix: retry transient attachment transport failures"
```

### Task 2: Run repository verification before concurrency changes

**Files:**
- Read: all Go packages

- [ ] **Step 1: Run the full deterministic checks.**

Run:
```powershell
go test ./...
go test -race ./internal/web ./internal/chathub -count=1
go vet ./...
go build ./...
git diff --check
```
Expected: all commands exit 0 with no race, vet, build, or whitespace errors.

- [ ] **Step 2: Record the commit and image base used for deployment.**

Run:
```powershell
git rev-parse HEAD
git show -s --format='%h %s' HEAD
```
Expected: the new commit is a descendant of `bd74d32`; no build may use `work/m365-source`.

### Task 3: Snapshot and apply the first concurrency tier

**Files:**
- Production: `/opt/m365-copilot2api/docker-compose.yml`
- Production: `/opt/sub2api/.env`
- Local record: `docs/deployments/2026-09-05-concurrency-tier-1.md`

- [ ] **Step 1: Capture a rollback snapshot before changing production.**

Save the compose files, container inspect output, proxy settings, and SHA256 values for account/token/session data under a timestamped `/opt/m365-copilot2api/backups/pre-concurrency-*` directory.

- [ ] **Step 2: Apply the conservative first tier without clearing data.**

Set the M365 service environment to:
```text
M365_ADAPTIVE_ACCOUNT_CONCURRENCY=true
M365_ACCOUNT_CONCURRENCY_LIMIT=8
M365_ACCOUNT_DEFAULT_CONCURRENCY=8
M365_GLOBAL_REQUEST_LIMIT=48
M365_GLOBAL_REQUEST_QUEUE_LIMIT=192
M365_REQUEST_WEIGHT_BYTES=65536
M365_WS_POOL_SIZE=16
M365_WS_POOL_TTL_SECONDS=300
```

Keep Redis URLs, cache strategy, affinity TTL, account files, and response-state data unchanged. Keep Sub2API first-output timeouts at the already deployed values.

- [ ] **Step 3: Recreate only the M365 service and verify health.**

Confirm the container reports the expected environment, has no restart increase, and its health endpoint returns `status=ok` before load testing.

### Task 4: Measure concurrency tiers with real request shapes

**Files:**
- Create: `docs/reports/2026-09-05-concurrency-benchmark.md`

- [ ] **Step 1: Run 8, 16, 32, and 48 concurrent requests at each tier.**

Cover Responses JSON, Responses streaming, Chat Completions, one tool-call round trip, and one image/attachment request. Use distinct prompts per request and reuse a small stable prefix so cache behavior is measurable without synthetic filler.

- [ ] **Step 2: Record success and latency metrics.**

For each shape record success rate, first-byte/first-event latency, total latency P50/P95/P99, 429/502/504 counts, request queue depth, M365 RSS, Redis memory, and container restart/OOM state.

- [ ] **Step 3: Choose the highest tier that remains stable.**

Accept a tier only when there are no OOM/restarts, no sustained queue growth, no new 502/504 cluster, and 429 rate stays at or below the pre-change baseline. If a tier fails, restore the previous tier from the snapshot and keep the last stable values.

### Task 5: Final verification and deployment record

**Files:**
- Create: `docs/deployments/2026-09-05-concurrency-final.md`

- [ ] **Step 1: Repeat health and functional checks after the selected tier is active.**

Run low-load Responses/Chat/tool/image requests, then repeat a smaller mixed concurrency sample to confirm the chosen tier survived the full workload.

- [ ] **Step 2: Verify data integrity and cache continuity.**

Compare account/token/session SHA256 values with the pre-change snapshot, inspect Redis key counts and TTLs, and confirm no cache or session volume was removed.

- [ ] **Step 3: Commit the deployment record and report the exact live values.**

Run:
```powershell
git add docs/deployments/2026-09-05-concurrency-final.md docs/reports/2026-09-05-concurrency-benchmark.md
git commit -m "docs: record measured concurrency rollout"
```

