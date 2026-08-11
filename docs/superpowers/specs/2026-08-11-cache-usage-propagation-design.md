# Cache Usage Propagation Design

## Problem

M365-Copilot2API records reused conversation history as `cache_tokens` in its
private usage log, but its OpenAI-compatible responses omit the standard cache
breakdown. Downstream gateways therefore record every request with zero cache
reads even when the session resolver reused a conversation prefix.

## Scope

The fix belongs in M365-Copilot2API. Sub2API already accepts both standard
cache paths:

- Chat Completions: `usage.prompt_tokens_details.cached_tokens`
- Responses: `usage.input_tokens_details.cached_tokens`

No database or account configuration changes are required.

## Design

Each request owns a small usage capture stored in its context. When the session
resolver matches a strict message prefix and the gateway actually sends only
the suffix, `openaiChat` records the matched message count. Requests that send
the full prompt, including new sessions, similarity fallback, and exact-history
requests with no suffix, record zero cached messages.

Protocol serializers use that captured prefix:

- Chat Completions estimates cached tokens with the existing legacy estimator
  and emits `prompt_tokens_details.cached_tokens`.
- Responses estimates cached tokens with the same tokenizer used for
  `input_tokens` and emits `input_tokens_details.cached_tokens`.
- Streaming terminal events and non-streaming bodies carry the same usage.
- Tool-call responses use the same request usage rather than losing it on an
  early return.

`input_tokens`/`prompt_tokens` remain total input tokens, matching OpenAI usage
semantics. Internal usage logs store non-cached input separately from cached
input so aggregate totals do not double count.

## Safety And Rollback

The existing image `m365-copilot2api:ce773ee` remains untouched. Deployment
uses a new versioned image tag, a Compose backup, a pre-cutover smoke container,
and a health check. Rollback changes only the image tag and recreates the
service.

## Acceptance Criteria

1. A new conversation returns no positive cached-token field.
2. A request that reuses a strict history prefix returns positive cached tokens.
3. Chat and Responses, streaming and non-streaming, preserve the cache field.
4. Internal usage input plus cache equals total input without double counting.
5. A real request through Sub2API creates a `usage_logs` row with
   `cache_read_tokens > 0` for account 919.
6. Existing tests and `go test -race ./internal/web` pass.
