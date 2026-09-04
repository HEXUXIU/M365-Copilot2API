# M365 Image Generation Event Recovery Design

## Problem

The production `gpt-image-2` path reaches M365 but currently returns no image.
Two live production requests showed the same failure shape: each selected M365
account remained connected for about 30 seconds, produced four or five ChatHub
events, and returned an empty `Images`, `Text`, and `RawResult`. Sub2API then
correctly returned an upstream 502 after M365 exhausted its bounded account
failover.

The deployed baseline is fixed for this work:

- M365 image: `m365-copilot2api:tool-cache-hardening-358a4de`
- M365 source: commit `358a4ded58d3d3c04e90b1917a4778daac7bd323`
- Sub2API image: `sub2api:image-public-origin-656844f1e`
- normalizer image: `m365-ingress-normalizer:headers-hidden-20260830`

The older `work/m365-source`, `tool-layer-merge-20260902`, and
`images-origin-cdd75c1` trees are not implementation sources for this repair.

## Goals

1. Recover real `gpt-image-2` generation through the existing OpenAI Images
   endpoint.
2. Identify the actual shape and outcome of the short ChatHub event sequence
   before changing request or parsing behavior.
3. Return an origin-aware public URL such as
   `https://clove.asia/v1/images/files/<uuid>` for URL responses.
4. Preserve `b64_json` and image edit compatibility.
5. Keep Responses, Chat Completions, tool calling, cache retention, account
   scheduling, Redis state, and the ingress normalizer unchanged.
6. Keep every diagnostic and implementation stage independently reversible.

## Non-Goals

- Replacing the current production baseline with another historical image.
- Reworking general ChatHub streaming or tool-call behavior.
- Changing cache keys, cache TTLs, account health policy, or normalizer headers.
- Persisting upstream image bytes in Sub2API.
- Logging prompts, response text, credentials, tokens, full URLs, `pollUrl`, or
  `fileToken` values.

## Selected Approach

Add a narrow, redacted event summarizer in the M365 ChatHub package and emit its
output only when an Images API request completes without an image. The summary
will describe structure rather than content:

- SignalR event `type` and `target`;
- top-level and argument-level field names;
- message `messageType`, `contentType`, and `contentOrigin`;
- content-generation progress status and field names;
- counts of URL-shaped image candidates;
- terminal result/error code names;
- metering capability names and access booleans.

The summary must never include string field values other than bounded protocol
enums from an explicit allowlist. Account IDs may use the existing internal ID
already present in image-generation logs; email addresses and identity tokens
remain excluded.

One production request will then distinguish the repair branch:

1. If an event contains a generated image URL, add a failing parser test using
   a redacted fixture with the observed shape, then minimally extend
   `imageURLs`.
2. If progress contains a pending image operation but no completed image, add a
   failing lifecycle test and keep the request open until the observed terminal
   image state or the existing image timeout. Polling is added only when the
   event proves that the upstream contract requires it.
3. If no image-generation message is emitted, compare the request payload with
   the repository's captured HAR contract. Add a failing payload test, then add
   only the missing image-specific option, allowed message type, or explicit
   request intent.
4. If the event carries an eligibility, quota, or capacity result, add a typed
   error test and route only that account through the existing image-specific
   health policy. Do not mark ordinary chat capability unhealthy.

## Test Strategy

Implementation follows red-green-refactor. Each production behavior change
starts with a focused failing unit test that reproduces the observed event or
payload shape. Focused tests run before the full package suite, race tests, vet,
build, and whitespace checks.

The candidate image must pass container-level checks before production traffic:

- self-reported commit matches the candidate commit;
- `/health` is HTTP 200;
- one direct M365 Images request returns an image without involving Sub2API;
- the existing Responses and Chat Completions smoke tests remain successful.

Production acceptance requires at least two distinct, meaningful image prompts.
For each successful URL response:

- response URL uses the request scheme and host;
- no private `172.*` address, internal port `4141`, or `X-M365-*` header leaks;
- unauthenticated public GET returns HTTP 200 and `image/*`;
- payload begins with a valid PNG, JPEG, or WebP magic number;
- response size is non-empty and within the existing 20 MiB limit.

Additional acceptance covers one `b64_json` generation, one image edit request,
Responses streaming, Chat Completions, an implicit real tool call, a tool-result
continuation, cache reuse, account switching, Redis memory, process memory, and
all container health states.

## Deployment And Rollback

Build the candidate from this branch with a unique image tag containing its Git
commit. Do not run the complete M365 Compose stack. Back up the active M365
Compose file, change only the `m365-copilot2api` image, and recreate only that
service. Sub2API, the normalizer, both Redis instances, PostgreSQL, and the
subscription proxy stay running.

If image acceptance fails, or any Responses, Chat Completions, tool, cache,
account, memory, or health regression appears, restore
`m365-copilot2api:tool-cache-hardening-358a4de` immediately and recreate only the
M365 application service. Keep diagnostic logs and test artifacts for analysis;
do not preserve sensitive event contents.

## Success Criteria

The repair is complete only when real production generation and download pass
twice with different prompts, `b64_json` and edit behavior pass, the core API and
tool/cache regressions pass, production reports the intended image and commit,
and the branch plus deployment record are pushed to the private repository.
