# Responses Router Instructions Design

## Goal

Preserve request-level OpenAI Responses `instructions` during model-driven tool
planning so the selected tool receives exact paths, markers, titles, prompts,
and environment constraints even when those details are not repeated in the
latest user input.

The change must retain the production cache-first design: Chat Completions
router isolation, response history, affinity identity, Redis state, public
response shape, tool-result continuation, and account scheduling remain
unchanged.

## Observed Failure

The Responses adapter converts `instructions` to a system message. The router
then calls `activeMessages`, which intentionally starts at the latest user
message and discards preceding system and developer messages. As a result, the
router sees the tool schema, current user input, and internal evidence ledger,
but not request-level instructions that may define exact tool arguments.

Production probes showed the consequence:

- A structured `create_thread` call replaced the requested title and child
  prompt with generic values derived from the user input and evidence ledger.
- PowerShell, `apply_patch`, and JavaScript calls selected the correct tool but
  omitted exact markers or paths supplied only through `instructions`.
- Repeating the same exact values in the user input made structured function
  arguments correct on the first tool turn.

## Chosen Design

Add an internal, non-serialized `PlanningInstructions` field to the normalized
OpenAI request. The Responses adapter sets it from the original request's
trimmed `instructions`; Chat Completions and other adapters leave it empty.

Before building a router prompt, prepend a distinct `[request instructions]`
section to the active planning input when `PlanningInstructions` is nonempty.
Limit this section to 16 KiB with the existing head-and-tail compaction helper.
The current active-turn router input remains independently bounded by its
existing limit.

This gives the router the current Responses contract without replaying every
system or developer message. It is narrower than forwarding all system
history, so existing Chat Completions isolation and latency behavior remain
intact.

## Data Flow

```text
Responses instructions
  -> responsesRequest.openAI
  -> oaiReq.PlanningInstructions (internal only)
  -> routerPlanningInput
  -> bounded [request instructions] section + active user/tool evidence
  -> modelToolRouterPrompt
  -> validated structured tool call
```

The field is excluded from JSON serialization. The canonical Messages slice
still contains the original system message, so response history, cache digest,
prompt accounting, continuation reconstruction, and the final-answer request
retain their existing behavior.

## Boundaries

- Maximum router-only instructions payload: 16 KiB.
- Empty or whitespace-only instructions add no router section.
- Chat Completions continue to drop old system/developer history from router
  planning.
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

1. Responses `instructions` populate the internal planning field.
2. Router planning includes short request instructions and exact argument text.
3. Whitespace-only instructions are omitted.
4. Long instructions are bounded to 16 KiB while retaining head and tail.
5. Existing Chat Completions tests still prove that old system/developer history
   is absent from the active router turn.
6. The full Go test, race, vet, build, and diff checks pass.

Production acceptance must repeat different meaningful probes for structured
function tools, PowerShell custom tools, `apply_patch`, JavaScript, SSE, and
`previous_response_id` continuation. It must record first-turn argument
fidelity, unique call IDs, real host execution, cached tokens, latency, public
header visibility, account/binding continuity, and post-deploy service health.

## Rollback

The implementation is isolated to request normalization and router input
construction. Rollback restores the prior production image and compose file;
no data migration or Redis cleanup is needed.
