# Responses Affinity Continuity Production Acceptance

Date: 2026-08-31

## Decision

The production rollout is accepted. A Responses continuation that supplies
`previous_response_id` without `X-M365-Session-Id` now keeps the verified
original affinity binding, account, upstream conversation, and session while
that account remains healthy. Tool execution, SSE event identity, cache reuse,
tenant isolation, custom UI controls, persisted state, and rollback material
were all verified after deployment.

No rollback signal was found.

## Production Artifact

- Branch: `production-super-20260831`
- Source head at deployment: `d9a45a5c3e2a79bfb635c1e0a536076396a6d7aa`
- Core implementation commit: `fe25ff1`
- Image: `m365-copilot2api:production-super-d9a45a5`
- Image ID: `sha256:4bd78d270206843936b18b0ac708e55e1fed1603fca3db8c73339d38cdeb2333`
- Gateway container: `acbac914903b`
- Gateway restart count: `0`
- Gateway started at: `2026-08-31T07:20:54.037880044Z`
- Measured gateway-only hot switch: approximately `1.51 s`

Redis, Sub2API, PostgreSQL, the subscription proxy, the ingress normalizer,
their volumes, accounts, and sessions were not recreated. Their pre-deployment
container identities and start times remained unchanged.

## Tool And Cache Results

All continuation tests deliberately omitted `X-M365-Session-Id` and used
`previous_response_id` as the continuation identity.

| Workload | Tool/result verification | Continuation input | Cached tokens | Cache rate |
|---|---|---:|---:|---:|
| Windows PowerShell custom tool | Model emitted `Get-Location`; command executed in the Windows workspace; final answer contained the real path | 15,013 | 14,911 | 99.32% |
| Linux POSIX custom tool with 37,363 characters of real deployed source context | Model emitted `pwd; grep '^PRETTY_NAME=' /etc/os-release`; command executed on the VPS; output reported `/tmp` and Ubuntu 26.04.1 LTS | 26,003 | 25,930 | 99.72% |
| Non-stream function tool | Valid JSON arguments requested `/etc/os-release`, `PRETTY_NAME`, and `VERSION_ID`; real values were returned and used in the final answer | 14,568 | 14,487 | 99.44% |
| SSE function tool | Stable non-empty `call_id` across all events; real `uname -srmo` output returned; `response.created` and `response.completed` present | 14,496 | 14,420 | 99.48% |
| Long-context paper continuation | 61,519 extracted characters from the published BERT paper; two different substantive questions answered | 56,730 | 56,605 | 99.78% |

The Windows and final Linux custom-tool chains each logged the same account and
the same binding on both turns with an empty migration reason. The final Linux
chain used binding prefix `9c00ea76de21`; its second turn logged
`cache_hit=true cached_tokens=25930`.

## Streaming Latency

The SSE tool chain produced the following timings from the VPS through the full
Sub2API-to-M365 route:

| Turn | Response headers / first delta | Completion |
|---|---:|---:|
| Cold tool call | 6.599 s | 6.610 s |
| Tool-result continuation | 1.881 s | 7.601 s |

Time to first delta improved by 71.5%. Completion time is not directly
comparable because the two turns emitted different output types and lengths.

The BERT paper chain measured 2.153 s to first delta and 9.120 s total on the
cold turn, then 2.419 s to first delta and 8.709 s total on the 99.78% cached
continuation. The cache result is strong; this particular warm TTFT sample was
0.266 s slower, so the report does not claim a paper-chain TTFT improvement.

## Diagnostic Control

A deliberately minimal custom-tool probe initially returned zero cached tokens.
Its first request contained only 758 input tokens and its continuation omitted
the original tool schema. A second diagnostic with long user content but
`tools=0` on continuation also returned zero cache. Both still stayed on the
same account and binding.

The final representative Codex probe resent the same real instructions and
custom tool definition (`tools=1`, `tool_choice=auto`) on continuation. It
returned 99.72% cache reuse. This distinction is material: cache continuity is
preserved by the gateway, but clients must keep a stable reusable prefix. The
actual Codex client request shape does so.

## Production Health

From the new gateway start through the final audit:

- Recorded requests: `96`
- HTTP 200: `96`
- HTTP 500: `0`
- HTTP 502: `0`
- Chat Completions: `83` requests, `57` cache hits, `3,084,487` cached tokens
- Responses: `13` requests, `3` cache hits, `55,328` cached tokens
- Total confirmed cache hits: `60`
- Total confirmed cached tokens: `3,139,815`
- Panic/fatal log matches: `0`
- Tool identity error matches: `0`
- Response alias error matches: `0`
- `response.failed` matches: `0`

The Responses aggregate includes intentional cold requests, negative controls,
and malformed-environment diagnostics. Per-chain continuation rates above are
the relevant measurements for affinity cache quality.

## Accounts And Redis

- Accounts: `31` total, `31` enabled, `31` online
- Disabled/offline/auth-failed/rate-limited accounts: `0`
- Redis: healthy, `loading=0`
- RDB last background save: `ok`
- AOF enabled; last rewrite and last write: `ok`
- Redis evictions: `0`
- Redis rejected connections: `0`
- Redis database size: `435` keys
- Redis memory: `58.37 MiB` used of `4.00 GiB`
- Redis policy: `volatile-lfu`

## Public And Custom UI Surface

- `https://clove.asia/v1/models`: HTTP 200 with the production API key
- Public `X-M365-*` response headers: `0`
- Admin page: HTTP 200
- Custom cache controls remain present: `缓存策略`, `cacheStrategy`, and `粘性`
- The legacy `实时额度` template remains dormant with `display:none`
- Tests prohibit calls that activate `renderQuotaDashboard()` and prohibit
  injecting account quota details into visible account rows

## Rollback

Rollback material is stored at:

`/opt/m365-copilot2api/backups/d9a45a5-predeploy`

All entries in `SHA256SUMS` and `SHA256SUMS.postdeploy` verified successfully.
The prior image `m365-copilot2api:production-super-91fa9b6` remains available.

## Design Conformance

The implementation matches the approved cache-first design:

- Affinity reference priority is `previous_response_id`, explicit session, then
  the generated public response ID.
- Response-state namespace identity remains separate from affinity identity.
- Every successful child response is bound through the tenant-verified alias
  path.
- Authentication failure, quota cooldown, and hard account failure still allow
  the scheduler to migrate; healthy continuations remain sticky.
- Tenant isolation, cooldown gates, Redis persistence, and response-state CAS
  behavior were not weakened.
