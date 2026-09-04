# M365 Image Generation Event Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore real `gpt-image-2` generation from the exact production baseline while preserving the existing API, tool, cache, and routing behavior.

**Architecture:** Add a redacted structural event summary at the M365 image boundary, use one production diagnostic request to identify the observed ChatHub contract, and then apply the smallest test-driven parser, lifecycle, request, or typed-error repair. Build and deploy only the M365 application service; retain Sub2API's existing public-origin image proxy.

**Tech Stack:** Go, Gorilla WebSocket/SignalR JSON, `net/http`, Docker Compose, Redis, OpenAI Images-compatible HTTP API.

---

### Task 1: Redacted ChatHub Image Event Summary

**Files:**
- Create: `internal/chathub/image_event_summary.go`
- Create: `internal/chathub/image_event_summary_test.go`

- [ ] **Step 1: Write the failing redaction and structure tests**

Pass an update frame containing protocol enums, a prompt, an email address, a bearer-like token, `pollUrl`, `fileToken`, and a full image URL to the desired API:

~~~go
got := SummarizeImageEvents([]json.RawMessage{raw})
encoded, _ := json.Marshal(got)
summary := string(encoded)
for _, forbidden := range []string{
    "private prompt", "person@example.com", "Bearer ",
    "poll-secret", "file-secret", "https://designerapp.officeapps.live.com/",
} {
    if strings.Contains(summary, forbidden) {
        t.Fatalf("summary leaked %q: %s", forbidden, summary)
    }
}
if !strings.Contains(summary, "\"event_type\":1") ||
    !strings.Contains(summary, "\"target\":\"update\"") ||
    !strings.Contains(summary, "\"message_type\":\"Progress\"") ||
    !strings.Contains(summary, "\"content_type\":\"image\"") {
    t.Fatalf("missing protocol shape: %s", summary)
}
~~~

Add a second test for invalid JSON and a type-2 completion frame. Invalid JSON reports only byte length and parse failure. Completion records result field names and classifies its value as `success`, `empty`, or `other` without copying the value.

- [ ] **Step 2: Run the focused tests and verify RED**

~~~powershell
go test ./internal/chathub -run 'TestSummarizeImageEvents' -count=1
~~~

Expected: compilation fails because `SummarizeImageEvents` is not defined.

- [ ] **Step 3: Implement the structural summarizer**

Use fixed JSON structs named `ImageEventSummary`, `ImageArgumentSummary`, `ImageMessageSummary`, and `ImageProgressSummary`. Record event index/bytes/type/target, sorted key names, message protocol enums, progress status/key names, URL candidate counts, result class, and metering access booleans.

Enum values are accepted only from protocol-specific keys and only after rejecting whitespace, URL punctuation, email markers, UUID-shaped values, and strings longer than 64 bytes. Unknown values become `other`. URL candidates are counted with `isImageURL` but never copied. Recursive walking is bounded to 32 levels and 1,024 nodes per event.

- [ ] **Step 4: Run focused and package tests and verify GREEN**

~~~powershell
go test ./internal/chathub -run 'TestSummarizeImageEvents' -count=1
go test ./internal/chathub -count=1
~~~

Expected: both pass and test output contains no fixture secrets.

- [ ] **Step 5: Commit the diagnostic primitive**

~~~powershell
git add internal/chathub/image_event_summary.go internal/chathub/image_event_summary_test.go
git commit -m "feat(images): add redacted event diagnostics"
~~~

### Task 2: Emit Diagnostics Only For Empty Image Results

**Files:**
- Modify: `internal/web/images.go`
- Create: `internal/web/images_diagnostics_test.go`

- [ ] **Step 1: Write a failing boundary test**

Define the desired helper `imageEventDiagnostic(res chathub.Result) string`. Assert that an empty image result includes event count, reasoning length, terminal reason, and structured summaries. Assert that an image-bearing result returns an empty string and raw event JSON/text never appears.

- [ ] **Step 2: Run the focused test and verify RED**

~~~powershell
go test ./internal/web -run 'TestImageEventDiagnostic' -count=1
~~~

Expected: compilation fails because the helper is not defined.

- [ ] **Step 3: Add the narrow empty-result log**

Marshal a fixed object containing `events`, `reasoning_bytes`, `terminal_reason`, and `chathub.SummarizeImageEvents(res.Events)`. Replace the existing text/raw preview debug log with `[image-gen-events]`. Keep the public 502 body unchanged.

- [ ] **Step 4: Verify GREEN**

~~~powershell
go test ./internal/web -run 'TestImageEventDiagnostic|TestImage' -count=1
go test ./internal/chathub ./internal/web -count=1
git diff --check
~~~

- [ ] **Step 5: Commit the integration**

~~~powershell
git add internal/web/images.go internal/web/images_diagnostics_test.go
git commit -m "fix(images): expose redacted empty-event structure"
~~~

### Task 3: Run One Diagnostic Candidate

**Files:**
- Modify on server: `/opt/m365-copilot2api/docker-compose.yml`
- Back up on server: `/opt/m365-copilot2api/backups/image-events-$candidateCommit/docker-compose.yml`

- [ ] **Step 1: Run pre-build verification**

~~~powershell
go test ./internal/chathub ./internal/web -count=1
go test -race ./internal/chathub ./internal/web -count=1
go vet ./...
go build ./...
git diff --check
git status --short
~~~

- [ ] **Step 2: Build an immutable candidate**

Set `$candidateCommit = git rev-parse --short=12 HEAD`, transfer only committed source, verify the Git commit on the server, build `m365-copilot2api:image-events-$candidateCommit`, and require the container to report the same commit.

- [ ] **Step 3: Back up and switch only M365**

Copy the active Compose file into the commit-named backup directory. Change only the M365 image tag and run:

~~~bash
docker compose up -d --no-deps m365-copilot2api
~~~

Verify that Sub2API, normalizer, Redis, PostgreSQL, and subscription proxy container IDs/start times did not change.

- [ ] **Step 4: Send exactly one diagnostic request**

Use a unique test ID and meaningful scene prompt. Start it once on the server and poll its response file so SSH/TUN reconnects never duplicate generation. Collect only `[image-gen]` and `[image-gen-events]` for the request window.

- [ ] **Step 5: Select the evidence branch**

| Evidence | Repair |
| --- | --- |
| Image candidate count above zero | Extend extraction for the observed nesting or encoded JSON boundary. |
| Image progress pending without terminal image | Treat image progress as activity and wait for terminal image state. |
| No GraphicArt or image progress | Add the missing image-specific HAR request contract. |
| Result or metering denial | Return a typed image-only error and fail over only that account. |

Before editing behavior, add the exact redacted observed fixture and selected assertion to this plan.

### Task 4: Test-Drive The Selected Repair

**Files:**
- Modify one or more of: `internal/chathub/images.go`, `internal/chathub/client.go`, `internal/web/images.go`
- Modify the matching focused test file in `internal/chathub` or `internal/web`

- [ ] **Step 1: Add the redacted observed fixture as a failing test**

Keep protocol structure and dummy image URLs; replace every prompt, identity, token, and correlation value. Assert one external behavior: extracted image, continued image wait, required payload field, or typed image-only error.

- [ ] **Step 2: Verify RED**

Run the exact test name with `go test ... -count=1` and record its expected assertion failure.

- [ ] **Step 3: Implement only the selected repair**

Keep changes inside the image-specific path. Preserve general chat/tool timeouts, payloads, parsing, and retry behavior.

- [ ] **Step 4: Verify GREEN and regressions**

~~~powershell
go test ./internal/chathub ./internal/web -count=1
go test -race ./internal/chathub ./internal/web -count=1
go vet ./...
go build ./...
git diff --check
~~~

- [ ] **Step 5: Commit the functional repair**

Stage only its implementation and tests, then commit with a specific `fix(images): ...` message naming the observed contract.

### Task 5: Production Acceptance And Versioning

**Files:**
- Create: `docs/deployments/2026-09-04-image-generation-recovery.md`

- [ ] **Step 1: Build and inspect the final immutable image**

Build from the clean functional commit. Require the image/container commit to match Git and retain both the diagnostic candidate and `tool-cache-hardening-358a4de`.

- [ ] **Step 2: Run direct M365 acceptance**

Send one direct M365 URL-format generation. Require image output and a successful download before involving Sub2API.

- [ ] **Step 3: Run two public generations**

Use two different meaningful prompts. Require an origin URL under `https://clove.asia/v1/images/files/`, HTTP 200 download, `image/*`, valid PNG/JPEG/WebP magic bytes, non-empty data, and no private address, port 4141, or M365 response header.

- [ ] **Step 4: Run format and edit acceptance**

Generate once with `b64_json`, decode it, and verify magic bytes. Submit a small PNG edit and verify a valid returned image.

- [ ] **Step 5: Run core regressions**

Exercise Responses streaming, Chat Completions, implicit tool selection, tool-result continuation, cache reuse, account switching, and health endpoints. Record latency, status, cache counters, selected accounts, Redis memory, M365 RSS, and all restart counts.

- [ ] **Step 6: Record, commit, and push**

Write exact baselines, commits, tags, request IDs, timings, response checks, container states, and rollback commands to the deployment document. Commit as `docs(images): record production recovery` and push `fix/image-generation-events-20260904` to the private remote.
