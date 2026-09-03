# Tool and Cache Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the current `7ddf077` baseline for tool identity, bounded 30-minute response state, and production artifact verification.

**Architecture:** Keep ChatHub event extraction and protocol adapters unchanged at their public boundaries. Add identity-aware stream deduplication, pass all cache controls through Compose, and use a single bounded response-state TTL when affinity is enabled or disabled. Verify with focused tests, full Go checks, and immutable image metadata.

**Tech Stack:** Go, Go test, Docker Compose, Redis, GitHub CLI.

---

### Task 1: Preserve legitimate parallel tool calls

**Files:**
- Modify: `internal/chathub/stream_events.go`
- Modify: `internal/chathub/client.go`
- Test: `internal/chathub/stream_events_test.go`

- [x] Write and run a failing test for two same-name/same-argument calls with distinct `callId` values.
- [x] Add explicit call identity extraction and use identity-first deduplication; retain normalized content fallback for identity-less events.
- [x] Run the focused ChatHub tests and then the full package tests.

### Task 2: Enforce bounded response-state retention

**Files:**
- Modify: `internal/web/responses_state.go`
- Test: `internal/web/responses_state_test.go`

- [ ] Add a test that the response-state TTL uses the configured affinity TTL and defaults to 30 minutes when no affinity manager is active.
- [ ] Implement the single TTL resolution path with a bounded environment fallback.
- [ ] Run focused response-state tests.

### Task 3: Pass cache controls into production Compose

**Files:**
- Modify: `docker-compose.yml`
- Modify: `.env.example`

- [ ] Pass affinity TTL, session cap, lock wait, sticky retry, cache strategy, full-context, sticky concurrency, and anonymous scope variables through the service environment.
- [ ] Keep existing data volumes and service names unchanged.
- [ ] Validate the Compose file renders successfully with `docker compose config`.

### Task 4: Regression and artifact audit

**Files:**
- Create: `docs/deployments/2026-09-03-tool-cache-hardening-release.md`

- [ ] Run `go test ./... -count=1`, race tests for web/ChatHub, `go vet ./...`, `go build ./...`, and `git diff --check`.
- [ ] Build an immutable image tag containing the source commit and record its digest.
- [ ] Verify source commit, image label, container version, and running digest agree before switching traffic.
- [ ] Run Responses JSON/SSE and Chat JSON/SSE smoke tests, including one tool continuation and one duplicate continuation replay.
- [ ] Record rollback image and Compose snapshot without flushing Redis.
