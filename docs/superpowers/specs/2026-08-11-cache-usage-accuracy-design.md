# Cache Usage Accuracy Design

## Problem

The gateway now exposes cache reuse through the standard OpenAI usage fields,
but the cached-token value is still derived from a reused message count. That
estimate can drift from the amount actually saved because it counts messages
individually while M365 receives a flattened prompt containing role labels,
separators, and tokenizer boundary effects. Chat and Responses also use
different estimators today.

There is an additional false-positive edge case: reuse is recorded before the
incremental suffix is flattened. If that suffix produces no text, the gateway
falls back to the full prompt but can still report cached tokens.

M365 does not return authoritative billing usage. The most accurate value the
gateway can provide is therefore the token difference between the full prompt
it would have sent without conversation reuse and the incremental prompt it
actually sent.

## Selected Semantics

For a strict-prefix session reuse that really sends an incremental suffix:

```text
cached_tokens = max(
  tokenize(full_flattened_prompt) - tokenize(sent_incremental_prompt),
  0,
)
```

Both operands use the same `o200k_base` tokenizer for GPT models. The result is
clamped to the response's total input-token estimate. This is an estimate of
payload tokens saved by M365 conversation reuse, not an OpenAI billing claim.

The value is not rounded to OpenAI's prompt-cache block size. M365 conversation
reuse is a different mechanism, and block rounding would make the number look
more OpenAI-like while making it less representative of the actual payload.

## Request Flow

1. Flatten the complete request messages into `fullPrompt` as today.
2. Resolve the session and identify a strict historical prefix.
3. Flatten only the suffix into `incrementalPrompt`.
4. Record a cache delta only after `incrementalPrompt` is non-empty and becomes
   the actual answer prompt.
5. At response serialization, tokenize `fullPrompt` and the recorded sent
   prompt with the request model's tokenizer and emit their bounded difference.

New sessions, similarity-only matches, full-prompt resends, empty suffixes, and
requests without a strict prefix report zero cached tokens.

## Protocol Mapping

- Chat Completions emits the result at
  `usage.prompt_tokens_details.cached_tokens`.
- Responses emits the same result at
  `usage.input_tokens_details.cached_tokens`.
- Streaming, non-streaming, text, and tool-call exits consume the same
  request-scoped cache delta.
- Internal usage records continue storing uncached input and cached input
  separately so aggregate totals do not double count.

The existing Responses `m365.usage_values_are_estimates` metadata remains the
source-of-truth disclosure. No database or Sub2API changes are required.

## Alternatives Rejected

### Reused-message token sum

This is the current implementation. It misses the exact flattened role and
separator representation and is sensitive to BPE boundaries between messages.

### OpenAI 1,024-token minimum and 128-token rounding

This matches OpenAI prompt-cache presentation but not M365 cloud-conversation
reuse. It can deliberately under-report real payload savings and is therefore
not used.

## Verification

Automated tests cover:

1. Exact equality with `tokens(fullPrompt) - tokens(incrementalPrompt)`.
2. Mixed Chinese and ASCII content, including role separators.
3. A strict prefix with a non-empty suffix.
4. An empty suffix that resends the full prompt and reports zero cache.
5. New-session and similarity-fallback requests reporting zero cache.
6. Bounded cache values for Chat and Responses.
7. Streaming, non-streaming, text, and tool-call response shapes.
8. Race detection for request-scoped capture access.

Live verification sends a deterministic two-turn fixture directly to M365 and
through Sub2API. The second response must equal the independently calculated
`o200k_base` full-versus-incremental delta, and the corresponding Sub2API row
must store that value in `cache_read_tokens` while preserving total input.

## Deployment And Rollback

Build a new versioned image without replacing
`m365-copilot2api:ce773ee-cacheusage.1`. Back up Compose, smoke-test the new
image, switch only the M365 service image, and verify HTTP health plus recent
logs. Rollback restores the Compose backup and recreates the previous image.
