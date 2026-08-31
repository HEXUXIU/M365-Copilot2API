# Cross-Protocol Router Instructions Design

## Goal

Preserve request-level instructions during model-driven tool planning for all
three supported upstream formats: native OpenAI Responses, routed OpenAI Chat
Completions, and routed Anthropic Messages. The selected tool must receive exact
paths, markers, titles, prompts, and environment constraints even when those
details are not repeated in the latest user input.

The change must retain the production cache-first design: Chat Completions
router isolation, response history, affinity identity, Redis state, public
response shape, tool-result continuation, and account scheduling remain
unchanged.

## Observed Failure

Each adapter normalizes its request-level policy into system or developer
messages. The router then calls `activeMessages`, which intentionally starts at
the latest user message and discards those preceding instructions. As a result,
the router sees the tool schema, current user input, and internal evidence
ledger, but not request-level constraints that may define exact tool arguments.

Production probes showed the consequence:

- A Responses structured `create_thread` call replaced the requested title and
  child prompt with generic values derived from the user input and evidence
  ledger.
- PowerShell, `apply_patch`, and JavaScript calls selected the correct tool but
  omitted exact markers or paths supplied only through `instructions`.
- Repeating the same exact values in the user input made structured function
  arguments correct on the first tool turn. Chat Completions system/developer
  messages and Anthropic `system` use the same normalized message path and are
  therefore subject to the same loss.

## Chosen Design

Add one protocol-agnostic helper that extracts nonempty system and developer
messages from the normalized OpenAI request in their original order. Preserve
role labels, join the values into a distinct `[request instructions]` section,
and limit the section to 16 KiB with the existing head-and-tail compaction
helper.

Before building a router prompt, prepend that bounded section to the active
planning input. The latest user/tool turn remains independently bounded by its
existing limit. Responses `instructions`, Chat Completions system/developer
messages, and Anthropic `system` blocks therefore converge on the same behavior
without adding protocol-specific request fields.

This gives the router the current application contract without replaying old
user or assistant turns. The additional prefix is stable within a conversation
and remains small enough to preserve cache and latency characteristics.

## Data Flow

```text
Responses instructions / Chat system+developer / Anthropic system
  -> protocol adapter
  -> normalized oaiReq.Messages
  -> routerPlanningInstructions
  -> routerPlanningInput
  -> bounded [request instructions] section + active user/tool evidence
  -> modelToolRouterPrompt
  -> validated structured tool call
```

The canonical Messages slice is not mutated. Response history, cache digest,
prompt accounting, continuation reconstruction, and the final-answer request
therefore retain their existing behavior.

## Boundaries

- Maximum router-only instructions payload: 16 KiB.
- Empty or whitespace-only instructions add no router section.
- System and developer messages retain their original relative order and role
  labels inside the bounded section.
- Old user and assistant history remains absent from one-shot router planning.
- Tool schemas, `tool_choice`, call-ID generation, validation, ledger filtering,
  and retry behavior are unchanged.
- No settings, database schema, Redis keys, affinity bindings, public headers,
  or administration UI change.

## Error Handling

Compaction is deterministic and cannot reject the request. Invalid tool
arguments continue through the existing schema validation and repair path.
Account failover and router timeouts retain their current behavior.

## Verification

Automated tests must prove:

1. Responses `instructions` survive normalization and appear in router planning.
2. Chat Completions system and developer messages appear in original order.
3. Anthropic string and text-block `system` values appear in router planning.
4. Whitespace-only instructions are omitted.
5. Long instructions are bounded to 16 KiB while retaining head and tail.
6. Existing isolation tests still prove that old user and assistant history is
   absent from the active router turn.
7. The full Go test, race, vet, build, and diff checks pass.

Production acceptance must exercise all three public formats with different
meaningful prompts. It must cover structured function tools, PowerShell custom
tools, `apply_patch`, JavaScript, non-stream and SSE streams, Responses
`previous_response_id`, Chat tool messages, and Anthropic `tool_use` /
`tool_result`. It must record first-turn argument fidelity, unique call IDs,
real host execution, cached tokens, latency, public header visibility,
account/binding continuity, and post-deploy service health.

## Rollback

The implementation is isolated to request normalization and router input
construction. Rollback restores the prior production image and compose file;
no data migration or Redis cleanup is needed.
