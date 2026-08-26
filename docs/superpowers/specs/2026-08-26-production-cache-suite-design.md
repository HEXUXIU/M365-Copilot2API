# Production Cache Suite Design

## Goal

Replace PRs #35, #36, and #65 with one reviewable PR based on upstream v0.6.0. The PR preserves the production-tested affinity and cache behavior, groups related changes into coherent commits, and exposes the simple cache controls in the administration console.

## Commit Groups

1. **Affinity and Redis**
   - Account and conversation affinity with tenant isolation.
   - Optional Redis shared state with in-memory fallback.
   - Conservative cache-hit accounting and concurrency-safe binding updates.

2. **Production cache strategy**
   - `balanced` and `sticky` routing strategies.
   - Sticky full-context sending and stable account/conversation reuse.
   - Legacy persisted-session migration and production request-path binding application.

3. **Failure and tool stability**
   - Retry known empty/fallback completions without exposing fallback text.
   - Preserve established conversations while failing over eligible new requests.
   - Isolate tool-router WebSockets and keep fallback responses from poisoning account health.

4. **Administration UI**
   - Add a `Balanced` / `Sticky` cache strategy selector to Settings.
   - Show a `Send full context` switch only for Sticky mode.
   - Read and write the existing `cacheStrategy` and `stickyFullContext` runtime settings.
   - Apply saved values to subsequent requests without a process restart.

## UI Behavior

The controls follow the existing Settings page styles and localization system. Balanced remains the conservative default. Sticky is the production-tested high-cache mode. The full-context switch is enabled by default when Sticky is selected. Redis URL, TTL, affinity mode, and concurrency tuning remain environment-level advanced settings.

## Migration

The new branch starts at upstream v0.6.0. Only final effective changes are reconstructed; superseded implementations and the instruction-wrapper add/revert pair are omitted. Existing persisted settings continue to load because field names and JSON representation remain unchanged.

## Verification

- `go test ./...`
- `go test -race ./internal/web/...`
- `go vet ./...`
- `go build ./...`
- `git diff --check`
- Settings API round-trip tests for both cache fields.
- Browser verification of selector visibility, conditional full-context control, persistence, and responsive layout.
- Production smoke tests for normal chat, streaming, tools, image input, sticky cache reuse, and concurrent long-context conversations.

## Pull Request Presentation

The replacement PR uses one branch and one PR, but retains the four commit groups above. Its description lists production cache measurements separately from unit-test results and identifies rollback points by commit.
