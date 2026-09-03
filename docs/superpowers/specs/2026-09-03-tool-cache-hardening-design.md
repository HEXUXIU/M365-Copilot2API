# Tool and Cache Hardening Design

**Goal:** Preserve legitimate parallel tool calls, eliminate duplicate stream/reasoning output, and make the current cache-first Responses path measurable and bounded before production rollout.

**Baseline:** `fix/duplicate-text-0e3e46c` at `7ddf077dc11b0617e23b21316bf6c4e68d7fe4f7`.

## Scope

This change stays on the current baseline. It does not merge open upstream PRs or replace the current account, Redis, proxy, or deployment configuration wholesale.

The work has four bounded parts:

1. Tool event identity: use upstream call identity when present; use normalized name/arguments only as a fallback for missing identity. Duplicate envelope representations of one call remain suppressed, while distinct parallel calls remain visible.
2. Stream reconciliation: keep cumulative ChatHub text snapshots, `writeAtCursor` fragments, tool events, and reasoning snapshots on separate reconciliation paths. Gateway-authored progress remains disabled unless the model emits an explicit status.
3. Cache/runtime acceptance: verify tenant/session/account isolation, 30-minute response-state retention, bounded Redis memory, and migration behavior without clearing Redis data.
4. Production artifact: commit the source, build an immutable image, compare source/image/container identifiers, and run Responses and Chat Completions smoke/replay tests before any traffic switch.

## Data flow and invariants

- A tool event is identified by `call_id`/message identity when available. A content fingerprint is a fallback only when the upstream event has no identity.
- The same call appearing in nested update arguments and `messages[]` is emitted once.
- Two calls with different upstream identities are emitted independently even when name and JSON arguments are identical.
- Reasoning snapshots emit only novel content. Rewritten or shorter snapshots do not replay old content.
- Router control text is excluded from user-visible history and cache accounting.
- Cache hits are reported only for verified incremental reuse. A cross-account migration records a cold replay before a new binding is established.
- Redis response state is retained for 30 minutes and remains subject to per-entry and global memory bounds.

## Verification matrix

- Unit: duplicate envelope, distinct parallel calls, missing identity fallback, reasoning snapshot growth/rewrite, failed-tool completion guard.
- Protocol: Chat JSON/SSE, Responses JSON/SSE, Responses continuation, duplicate continuation replay, tool result success/failure, complete `call_id` and `name`.
- Runtime: two tenant/session keys, same account/different sessions, account migration, Redis TTL and memory observations, 20/50/100/200 concurrent requests.
- Latency: record account selection, upstream connection, first valid event, first client chunk, tool event, and completion separately. Track P50/P95 TTFB and total duration.
- Artifact: source commit, image label, image digest, running container digest, and rollback snapshot must match the recorded release manifest.

## Rollback

The previous production image and compose snapshot remain untouched. A failed smoke test, identity mismatch, cache isolation violation, or memory regression blocks traffic switching and restores the previous artifact without flushing Redis.
