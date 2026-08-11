# Cache Usage Propagation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Propagate actual M365 conversation-prefix reuse through standard OpenAI cache usage fields so Sub2API records cache reads.

**Architecture:** Store the resolver's actually reused prefix length in a request-scoped capture. Use the existing Chat heuristic and Responses tokenizer to serialize protocol-native cache details, and use the same captured value for internal usage accounting.

**Tech Stack:** Go, `net/http`, request contexts, `sync/atomic`, OpenAI Chat Completions and Responses JSON/SSE, Docker Compose.

---

### Task 1: Request-scoped cache capture and estimators

**Files:**
- Create: `internal/web/request_usage.go`
- Test: `internal/web/request_usage_test.go`
- Modify: `internal/web/codex_usage.go`
- Test: `internal/web/codex_responses_compat_test.go`

- [ ] Write tests proving zero cache for no reuse, positive bounded cache for a reused prefix, and concurrent-safe capture reads/writes.
- [ ] Run `go test ./internal/web -run 'Test(RequestUsage|ResponsesUsage.*Cache)' -count=1` and verify it fails because the capture and cache-aware estimator do not exist.
- [ ] Add a request-context capture containing an atomic reused-message count.
- [ ] Add cache-aware Chat and Responses estimators. Clamp cached tokens to total input tokens and omit details when the value is zero.
- [ ] Re-run the targeted tests and verify they pass.

### Task 2: Resolver wiring and protocol serialization

**Files:**
- Modify: `internal/web/server.go`
- Modify: `internal/web/protocol_handlers.go`
- Modify: `internal/web/tool_response.go`
- Test: `internal/web/codex_responses_compat_test.go`
- Test: `internal/web/request_usage_test.go`

- [ ] Add failing tests for Chat and Responses usage maps in stream and non-stream shapes.
- [ ] Run the targeted tests and verify the cache fields are absent before implementation.
- [ ] Ensure every `openaiChat` request has a capture and record reuse only when `0 < HistoryLen < len(Messages)` and the suffix is sent.
- [ ] Include Chat usage in ordinary and tool-call terminal responses.
- [ ] Use cache-aware Responses estimates in streaming and non-streaming adapters.
- [ ] Change internal usage logs to store uncached input and cached input separately.
- [ ] Re-run targeted tests and verify all protocol shapes pass.

### Task 3: Regression verification

**Files:**
- Modify only files from Tasks 1 and 2 if a regression is found.

- [ ] Run `gofmt` on changed Go files.
- [ ] Run `go test ./internal/web -count=1`.
- [ ] Run `go test -race ./internal/web -count=1`.
- [ ] Run `go test ./... -count=1`.
- [ ] Inspect `git diff --check` and scan the diff for secrets.

### Task 4: Versioned deployment and live validation

**Files:**
- Modify on server: `/opt/m365-copilot2api/docker-compose.yml`
- Create on server: `/opt/m365-copilot2api/releases/cacheusage.1/`

- [ ] Cross-compile the exact source revision plus patch for Linux/amd64.
- [ ] Build a new image from `m365-copilot2api:ce773ee` by replacing only `/app/m365-copilot2api`.
- [ ] Start a temporary smoke container and verify `/api/health`.
- [ ] Back up Compose, switch only the M365 image tag, and verify container health/logs.
- [ ] Send a new turn followed by a strict-prefix continuation directly to M365 and verify the standard cached-token field.
- [ ] Send the same pattern through Sub2API and verify account 919 receives a new row with `cache_read_tokens > 0`.
- [ ] Keep the original image and Compose backup for immediate rollback.
