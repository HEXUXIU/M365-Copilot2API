# M365 Image Progress Stream Design

## Baseline

This change starts from the production image repair source at M365 commit
`675ea194ca30448748ea45e16adfde6486724ad4` and Sub2API commit
`cb47b336e83400abe96e489dfcaff8b14ab4272f`. The existing non-streaming
image response, public-origin image proxy, image-specific timeout, structured
client errors, account routing, cache behavior, and tool behavior remain the
baseline.

## Problem

Sub2API already accepts `stream:true`, relays an upstream SSE response, and
sends an SSE comment keepalive while an image stream is quiet. The M365 Images
handler currently ignores `stream:true` and always returns one final JSON
document. Consequently, the API-key route from Sub2API to M365 receives no SSE
events to relay and clients see neither generation progress nor an early first
byte.

M365 ChatHub already exposes the real `GraphicArt` and `ImageGeneration`
lifecycle while the request is running. That lifecycle should drive the public
stream. M365 does not expose a trustworthy percentage, so the gateway must not
invent one.

## Goals

1. Make `/v1/images/generations` and `/v1/images/edits` honor `stream:true`.
2. Send the first SSE event before waiting for ChatHub image completion.
3. Expose genuine queued, generating, downloading, and completed phases.
4. Send SSE comment keepalives every 10 seconds while no phase changes.
5. Preserve standard final `image_generation.completed` and
   `image_edit.completed` events.
6. Preserve the current non-streaming JSON contract and 55-second whitespace
   keepalive.
7. Keep public image URLs on the request origin and keep `X-M365-*` headers
   absent from public responses.
8. Keep Responses, Chat Completions, Anthropic Messages, tool calling, cache,
   account scheduling, Redis, PostgreSQL, and all unrelated containers
   unchanged.

## Non-Goals

- Synthesizing progress percentages.
- Generating fake partial image bytes.
- Adding an asynchronous image-task API or a polling database.
- Changing image cooldown, account failover, or cache policy.
- Rebuilding Sub2API streaming around a new protocol.
- Persisting generated-image files in this change.

## Public Protocol

Only requests with `stream:true` use SSE. The response headers are:

```text
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

M365 emits a visible progress event when the phase changes:

```text
event: image_generation.progress
data: {"type":"image_generation.progress","stage":"queued","elapsed_seconds":0}

event: image_generation.progress
data: {"type":"image_generation.progress","stage":"generating","elapsed_seconds":12}

event: image_generation.progress
data: {"type":"image_generation.progress","stage":"downloading","elapsed_seconds":67}
```

Edits use the `image_edit.progress` prefix. A progress event contains only
`type`, `stage`, and non-negative `elapsed_seconds`. It contains no prompt,
account identity, upstream URL, access token, file token, poll URL, or internal
host.

If the phase has not changed for 10 seconds, M365 emits an SSE comment:

```text
: image-generation keepalive

```

SSE comments keep Cloudflare and raw HTTP clients alive while remaining
invisible to standard SSE event consumers. The existing Sub2API keepalive is
retained as a second independent guard.

On success, M365 emits exactly one final event per produced image:

```text
event: image_generation.completed
data: {"type":"image_generation.completed","created_at":0,"url":"https://clove.asia/v1/images/files/IMAGE_ID"}
```

For `response_format=b64_json`, the final payload carries `b64_json` instead of
`url`. The final event may also contain existing image metadata when known. It
must not emit a partial-image event unless actual partial image bytes exist.

## Internal Data Flow

The M365 Images handler parses `stream` for JSON generation requests and
multipart edit requests. In stream mode it commits SSE headers, emits `queued`,
and calls `chatWithAccountEvents` instead of the non-event ChatHub entry point.

The event callback identifies image generation from the structured ChatHub
fields `messageType`, `contentType`, and `contentOrigin`. The first genuine
pending `GraphicArt` or `ImageGeneration` event advances the phase to
`generating`. Duplicate upstream snapshots do not repeat the phase event.

When ChatHub returns a completed Designer image URL, the handler advances to
`downloading`, obtains the scoped Designer token, downloads and validates the
image, and stores or encodes it through the existing path. It then emits the
standard final event.

The progress writer serializes all writes and heartbeats. A failed client write
stops downstream progress output but does not cancel already-started upstream
image work, preserving the existing billing and completion behavior.

Sub2API continues using its API-key image path. It relays M365 SSE lines,
counts the final image event for billing, preserves its stream timeout and
keepalive policy, filters response headers, and rewrites any completed public
URL to the original request scheme and host when necessary.

## Error Semantics

Validation and scheduling errors raised before SSE starts preserve their real
HTTP status. Once the queued event is flushed, HTTP status is committed as 200;
later errors are emitted once as an SSE `error` event with an OpenAI-compatible
error object. No completion event follows an error.

An account may be switched only before any visible image-generation phase from
that account proves work was accepted. After a genuine generating phase, the
non-idempotent request is not replayed. Existing image-specific cooldown and
rate-limit behavior remains authoritative.

## Test Strategy

Implementation follows red-green-refactor:

1. M365 handler tests first prove that `stream:true` currently returns JSON and
   does not emit queued, generating, downloading, and completed events.
2. ChatHub event tests prove that real pending and completed GraphicArt frames
   advance phases without exposing sensitive fields or duplicating events.
3. Heartbeat tests use short injected intervals and prove valid SSE framing,
   serialized writes, prompt cancellation, and no invented percentages.
4. Edit tests prove multipart `stream=true` reaches the same stream path and
   uses `image_edit.*` event names.
5. Sub2API tests prove progress pass-through, final-event accounting,
   public-origin URL rewriting, header filtering, and unchanged non-stream JSON.

Production acceptance uses different meaningful prompts and a real PNG edit.
It records first-byte time, each phase timestamp, total time, response event
order, final URL origin, immediate download status, content type, content
length, file magic, and absence of `X-M365-*` headers. A distinct `b64_json`
request validates decoding and image magic.

The image checks are followed by isolated Responses, Chat Completions,
Anthropic Messages, real implicit tool calls, tool-result continuations,
long-context cache reuse, controlled account switching, Redis memory, process
RSS, restart counts, OOM flags, and recent fatal/panic logs.

## Deployment And Rollback

Each source change receives its own Git commit. Candidate images include the
commit prefix in their tag. Before deployment, save the active Compose file,
container inspection, and image inspection in a timestamped rollback
directory.

Recreate only the service whose image changes. Do not recreate PostgreSQL,
Redis, the ingress normalizer, or the subscription proxy. Preserve the current
production images as immediate rollback targets. If image streaming or any
core protocol, tool, cache, routing, memory, or health regression appears,
restore the recorded image and recreate only the affected application service.

## Success Criteria

The work is complete when stream generation and stream edit both show real
progress without a Cloudflare 524, non-stream URL and `b64_json` still pass,
fresh URLs download completely, core tool and cache tests remain green,
resource health remains within the existing VPS limits, and both canonical
branches plus a credential-free deployment record are pushed.
