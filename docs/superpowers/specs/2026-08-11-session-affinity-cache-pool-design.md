# M365 Session Affinity and Context Reuse Pool

Status: implemented; rollout starts in observe mode

## Objective

Increase Microsoft 365 Copilot context reuse while keeping account selection stable
for a logical conversation. The gateway must report reused context only after an
existing Microsoft cloud conversation was actually continued successfully.

The target deployment has fewer than 50 concurrent requests and runs one gateway
instance. A single Redis instance is sufficient; Redis Cluster is out of scope.

## Current Behavior

- Requests without an account id call `auth.Store.Next()` and rotate through the
  account list.
- `sessionResolver` can restore an account and Microsoft conversation from an
  explicit session id, user field, IP fingerprint, or message-prefix match.
- Account health is process-local. A 429, 401, or 403 can cause the next request
  to use another account.
- The admin usage log counts previous request messages as `CacheTokens`, even
  when no successful cloud-conversation reuse was proven.
- OpenAI-compatible responses omit standard cached-token detail fields, so a
  downstream gateway has no cache-read signal to propagate.

## Non-Goals

- Cache completed model answers.
- Claim control over Microsoft's internal token-cache implementation or billing.
- Merge conversations solely because their text is similar.
- Preserve a warm conversation after its Microsoft account becomes unusable.
- Add Redis Cluster or multi-region replication for the current traffic level.

## Domain Model

### Account affinity key

A tenant-scoped stable routing key derived from the strongest available signal,
in this order:

1. A known `previous_response_id`.
2. `X-M365-Session-Id`.
3. Explicit `session_id` or `conversation_id` headers.
4. `prompt_cache_key`.
5. A tenant-scoped content seed containing model, system/developer messages,
   tool definitions, and the first user message.

The affinity key chooses an account; it does not by itself prove that two
requests belong to the same Microsoft cloud conversation. The content seed is
an exact deterministic fallback, not a fuzzy match. The tenant/API-key identity
is part of every key so two callers cannot share state.

### Conversation binding key

A cloud conversation is resolved separately from account affinity. Explicit
session ids and known response ids resolve directly. Stateless requests resolve
only through the longest exact history-prefix digest. Similar histories and
identical first-turn requests do not automatically continue a cloud
conversation.

After a successful response, the stored cumulative history digest includes the
assistant output returned to the client. A later request must contain that
history as an exact prefix before it can continue the binding. This separates
branches that began with the same prompt but received or chose different
assistant turns without storing raw prompts or responses in Redis.

### Affinity binding

An affinity binding connects one conversation binding key to:

- one Microsoft account;
- one Microsoft `conversation_id`;
- one Microsoft `session_id`;
- the cumulative normalized history digest, message count, and token count;
- a monotonically increasing generation;
- creation, last-use, and expiry timestamps;
- the latest confirmed reuse status.

### Warm reuse

A request is a warm reuse only when all conditions are true:

1. An existing conversation binding was resolved.
2. Its stored history is an exact prefix of the incoming history.
3. The request continued the stored Microsoft conversation on the bound account.
4. Microsoft returned a successful response.

Merely receiving historical messages does not count as a cache hit.

## Architecture

### Session key resolver

Create a focused resolver that returns a tenant-scoped `affinity_hash`, an
optional exact conversation binding, and the reasons used to derive them.
Replace IP/UA similarity as a routing decision. IP/UA may remain diagnostic
metadata only.

The account-affinity fallback follows Sub2API's stable-session approach. It uses
exact normalized fields and SHA-256. Tool-call ids and other
transport-generated ids are excluded, while roles, text, tool names, schemas,
and the requested model are included.

For stateless conversation lookup, compute normalized prefix digests from the
longest eligible prefix to the shortest and issue one Redis `MGET`. Continue
only the longest exact match. Cap lookup at the latest 64 messages; explicit
session and response ids have no such cap.

### Affinity store

Add an `AffinityStore` interface with a Redis implementation. Production uses
Redis; the existing file-backed resolver remains a degraded single-instance
fallback when Redis is unavailable.

Redis keys use an isolated namespace:

```text
m365:account-affinity:v1:{tenant_hash}:{affinity_hash} -> account_id, EX 2h
m365:conversation:v1:{tenant_hash}:{binding_id}        -> JSON binding, EX 2h
m365:history:v1:{tenant_hash}:{history_digest}         -> binding_id, EX 2h
m365:response:v1:{tenant_hash}:{response_id}           -> binding_id, EX 2h
m365:lock:v1:{tenant_hash}:{lock_key}                   -> owner token, EX 180s
m365:affinity-lru:v1                                   -> sorted set(last_used, key)
m365:account-health:v1:{account_id}                    -> health JSON with TTL
```

Bindings use a sliding two-hour TTL, matching the current cloud-conversation
cleanup window. The LRU index enforces a default maximum of 10,000 active
bindings. Each binding is expected to consume roughly 1-2 KB including Redis
overhead, comfortably below the target capacity.

The store exposes atomic operations for lookup, initial claim, refresh,
compare-and-swap migration, response binding, and deletion. Redis payloads do
not contain access or refresh tokens.

### Account scheduler

Existing warm bindings have first priority while their account is healthy and
compatible. New sessions use bounded-load rendezvous hashing over healthy
accounts instead of mutable round-robin. This provides:

- deterministic placement for a session;
- minimal remapping when accounts are added or removed;
- load distribution based on configured account capacity and current inflight
  requests;
- stable placement after a process restart even before a binding is recreated.

Each account has a configurable concurrency limit. At the target load, local
atomic inflight counters are sufficient for request slots; Redis health state
keeps cooldown and auth-failure decisions consistent across restarts.

### Session execution lock

Requests for different conversation bindings execute concurrently. Requests for
the same binding acquire a tenant-scoped lease before reading and extending
conversation history. A cold request locks its account-affinity key only for
placement and binding creation. This preserves turn order and prevents duplicate
continuations without treating two identical first-turn requests as one cloud
conversation.

The lock value is a random owner token. Release uses compare-and-delete. The
180-second lease exceeds the current 120-second chat timeout, so a crashed
request eventually releases itself. Lock wait respects the caller's context and
returns a retryable busy response on timeout.

## Request Flow

1. Authenticate the caller and derive `tenant_hash`.
2. Resolve `affinity_hash` plus an optional exact conversation binding and
   acquire the corresponding execution lease.
3. Read the account affinity and conversation binding.
4. If a healthy conversation binding exists, validate an exact history prefix
   and construct an incremental prompt from only the new messages.
5. If no conversation binding exists, reuse a healthy account-affinity choice or
   select one with bounded-load rendezvous hashing, then send a full prompt to a
   new cloud conversation.
6. Execute the Microsoft request.
7. On success, atomically create or refresh the binding with the returned cloud
   conversation/session ids plus the request-and-assistant history digest,
   message count, and token count.
8. Bind the resulting history digest and any public response id to the binding.
9. Emit usage and cache metadata, then release the lease.

## Failure and Migration

### Short rate limit

When the bound account returns 429/503 with `Retry-After` of five seconds or
less, retain the binding and retry the same account once. This protects warm
context from transient throttling.

### Sustained rate limit or transient upstream failure

After the same-account retry is exhausted, mark the account cooling down and
select a different healthy account. Send the full prompt to create a cold cloud
conversation. Update the binding with compare-and-swap only after the new
request succeeds.

### Authentication failure

Mark 401/403 accounts unavailable until re-authenticated. Migrate the current
session using the same success-before-swap process.

### Migration safety

- The old binding remains authoritative until a replacement succeeds.
- A generation check prevents two retries from overwriting each other.
- A failed replacement leaves the old binding intact and records a cold-start
  reason without claiming cache reuse.
- A successful migration resets cached-token accounting for that request to
  zero because the new account did not reuse the old cloud conversation.

## Cache Accounting and Protocol Output

Count only the exact historical prefix omitted from the incremental Microsoft
request. Tokenize the full logical input and the fresh incremental input with the
existing tokenizer. Standard total prompt/input tokens equal fresh plus reused
tokens; cached tokens are the reused subset. Report the source as
`conversation_reuse`, making clear that this is gateway context reuse rather
than Microsoft billing telemetry.

Protocol mappings:

- Chat Completions: `usage.prompt_tokens_details.cached_tokens`.
- Responses: `usage.input_tokens_details.cached_tokens`.
- Anthropic Messages: `usage.cache_read_input_tokens`.

For streaming responses, cached-token details appear only in the terminal usage
event after the Microsoft response succeeds. The admin usage log stores
`cache_hit`, `cache_source`, `affinity_hash_prefix`, `binding_id_prefix`,
`account_id`, `reused_input_tokens`, and `migration_reason`. It never logs raw
prompts.

## Configuration

```text
M365_AFFINITY_MODE=off|observe|enforce
M365_REDIS_URL=redis://redis:6379/0
M365_AFFINITY_TTL_MINUTES=120
M365_AFFINITY_MAX_SESSIONS=10000
M365_REDIS_POOL_SIZE=32
M365_REDIS_MAX_ACTIVE_CONNS=64
M365_SESSION_LOCK_TTL_SECONDS=180
M365_SESSION_LOCK_WAIT_SECONDS=120
M365_STICKY_RETRY_AFTER_SECONDS=5
M365_ACCOUNT_DEFAULT_CONCURRENCY=8
```

`observe` derives keys and records proposed decisions without changing routing.
`enforce` enables affinity routing and strict cache accounting. If Redis is
unavailable, the single-instance file-backed resolver handles requests and the
health endpoint reports degraded affinity state.

## Observability

Expose counters and structured logs for:

- affinity lookup hit/miss by reason;
- warm reuse success/failure;
- cached tokens by protocol;
- account selection and inflight slots;
- same-account retry;
- session migration and reason;
- lock wait duration and timeout;
- Redis errors and fallback activation;
- active bindings and LRU evictions.

## Testing

### Unit tests

- Account-affinity hashes are deterministic and tenant-isolated.
- Explicit ids take priority over content fallback.
- Identical first turns share account affinity but create independent cloud
  conversations unless an explicit id or exact assistant-bearing history proves
  continuation.
- Divergent branches resolve to different conversation bindings.
- Rendezvous assignment is stable and excludes unhealthy/full accounts.
- A binding refreshes its TTL and preserves its account.
- A strict message prefix produces the correct incremental prompt and reused
  token count.
- Similar but unequal histories do not reuse a conversation.
- Cache fields remain zero on cold requests, failed requests, and migrations.
- Chat, Responses, Anthropic, and streaming terminal usage use the correct
  cached-token field.

### Concurrency tests

- Fifty simultaneous continuations of one explicit session serialize onto one
  binding and one Microsoft conversation.
- Fifty identical stateless first turns share account affinity but create
  independent Microsoft conversations.
- Fifty different sessions distribute across healthy accounts without exceeding
  configured account concurrency.
- Concurrent migration attempts result in one compare-and-swap winner.
- `go test -race ./...` has no state races.

### Integration tests

Use fake Microsoft accounts and a fake affinity store to verify warm reuse,
short-429 retry, sustained-429 migration, auth-failure migration, process
restart recovery, Redis outage fallback, and terminal streaming usage.

## Rollout and Rollback

1. Deploy with `M365_AFFINITY_MODE=observe` and verify key stability, proposed
   account placement, Redis health, and zero cross-tenant matches.
2. Import valid `sessions.json` bindings into Redis once, preserving account and
   cloud conversation ids. Create history-digest indexes only for records that
   include assistant-bearing normalized history; older ambiguous records remain
   available only through explicit ids until refreshed.
3. Enable `enforce` for one API key, verify warm reuse and standard cached-token
   fields, then enable globally.
4. Keep the existing resolver available during the first release. Rollback is a
   configuration change to `off`; Redis keys expire automatically after two
   hours.

## Acceptance Criteria

- Repeated turns in one logical session use the same healthy Microsoft account.
- A process restart does not change a warm session's account.
- Concurrent cold requests for the same explicit session do not create duplicate
  cloud conversations; independent stateless first turns remain independent.
- Account changes occur only after an explicit health failure or capacity rule.
- Cache usage is nonzero only after confirmed successful cloud-conversation
  reuse and equals the tokenized strict historical prefix.
- Downstream Sub2API receives standard cached-token details in non-streaming and
  streaming terminal usage.
- The system handles 50 concurrent requests without exceeding account limits,
  corrupting conversation order, or creating cross-tenant bindings.
