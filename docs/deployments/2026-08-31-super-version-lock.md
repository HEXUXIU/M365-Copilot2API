# Production Super Version Lock

This record pins the M365 gateway build that was selected from the afternoon
acceptance run. It is metadata only; credentials, API keys, and account token
material are intentionally excluded.

## Source and image

- Source commit: `c728e0d4db2fb5228ba3703ef6a67a3bee7557a3`
- Production image: `m365-copilot2api:c728e0d-hardgate-20260830-fixed`
- Production image digest: `sha256:e0d69d2a0423d0afbb67c1843266763418d93cc83af4f2e40516d34b9576f80d`
- VPS: `15.204.122.247`
- Public path: `clove.asia -> Sub2API -> m365-ingress-normalizer:4141 -> m365-copilot2api:4142`

## Locked performance profile

- `M365_CACHE_STRATEGY=sticky`
- `M365_AFFINITY_MODE=enforce`
- `M365_AFFINITY_TTL_MINUTES=360`
- `M365_AFFINITY_MAX_SESSIONS=30000`
- `M365_CONTEXT_WINDOW=1179648`
- `M365_MAX_OUTPUT_TOKENS=131072`
- `M365_ACCOUNT_DEFAULT_CONCURRENCY=20`
- `M365_ACCOUNT_CONCURRENCY_LIMIT=20`
- `M365_WS_POOL_SIZE=16`
- `M365_WS_POOL_TTL_SECONDS=300`
- `M365_TOOL_PROTOCOL_MODE=pi_compat`

Redis remains on the cache-first profile: 4 GiB maxmemory, `volatile-lfu`, 16
samples, `hz=30`, active expiry effort 8, lazy expiry/eviction, and AOF
`everysec`.

## Acceptance evidence

- `/health`: HTTP 200.
- Chat Completions JSON: HTTP 200 with correct final content.
- Chat Completions SSE: HTTP 200, six data events, terminal `[DONE]`.
- Responses native tool: HTTP 200 with a non-empty structured `call_id`.
- Tool-result continuation: HTTP 200 with one final message and no duplicate tool call.
- Long-context Responses continuation: `573 / 590` cached input tokens (97.1%).
- Public `clove.asia`: `/v1/models`, Chat JSON, and Chat SSE all HTTP 200.
- Redis during the run: `evicted_keys=0`, `rejected_connections=0`.
- Application, ingress, and Redis restart counts: `0`; `OOMKilled=false`.
- Temporary audit API keys were deleted after testing.

## Rollback references

The deployment compose snapshots are retained on the VPS. The previously
constructed cache-controls image is retained as a separate local image tag,
and the original `toolpolicy-shellaware-final-5103744` image is retained under
an `-original-20260831` tag. Rollback does not flush Redis or alter data
volumes.
