# Responses Tool Call Identity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement the plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure every public Responses tool call has a usable `call_id` and tool name so client tool-result continuations cannot fail with an identity validation error.

**Architecture:** Normalize tool-call identity at the Responses projection boundary. Preserve upstream IDs when present, generate a unique gateway ID only when an upstream call omitted one, and reject calls that still lack a name. Validate tool-result input before it enters response state. Keep cache, affinity, account scheduling, and existing custom-tool event ordering unchanged.

**Tech Stack:** Go, `net/http`, existing Responses SSE adapter, Go unit tests.

---

### Task 1: Reproduce the malformed identity

**Files:**
- Modify: `internal/web/custom_tools_test.go`
- Modify: `internal/web/responses_stream_adapter_test.go`

- [x] **Step 1: Write failing tests** for non-stream output with an empty upstream ID and a stream whose first tool delta has a name but no ID.
- [x] **Step 2: Run the focused tests** and confirm the assertions fail because `call_id` is empty or the adapter emits `response.failed`.

### Task 2: Normalize public tool-call identities

**Files:**
- Modify: `internal/web/codex_responses.go`
- Modify: `internal/web/protocol_handlers.go`

- [x] **Step 1: Add one helper** that trims/validates tool names, preserves non-empty IDs, and creates a UUID-backed `call_` ID for an omitted ID.
- [x] **Step 2: Apply the helper** before non-stream Responses output is serialized and when the stream adapter has learned a tool name but no ID.
- [x] **Step 3: Keep malformed calls with no tool name as a structured `invalid_tool_call` failure and never emit an item with empty identity fields.

### Task 3: Verify continuation and production behavior

**Files:**
- Modify: `internal/web/protocol_compat_test.go`
- Modify: `internal/web/responses_state_test.go`

- [x] **Step 1: Add regression coverage** for `function_call_output` and `custom_tool_call_output` continuation IDs.
- [x] **Step 2: Run focused tests, race tests, `go vet`, and `go build`.
- [ ] **Step 3: Build a versioned image, back up compose/container metadata, restart only the gateway, and run public Responses JSON/SSE plus tool-result continuation checks. Blocked until SSH access to the production VPS is restored.
- [ ] **Step 4: Roll back to the saved image immediately if any health, cache, or tool check regresses.
