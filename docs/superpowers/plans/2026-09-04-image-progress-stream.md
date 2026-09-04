# M365 Image Progress Stream Implementation Plan

> For agentic workers: use task-by-task execution with failing tests first. Steps use checkbox syntax.

Goal: Make M365 Images generation and edit requests provide real SSE progress while preserving non-streaming, tool, cache, routing, and image-download behavior.

Architecture: Add a small M365 web-layer progress writer that emits deduplicated stage events and 10-second SSE comments. Extend the current image request path with a stream branch using chatWithAccountEvents; retain the existing blocking branch for stream:false. Sub2API remains the relay and accounting boundary because it already handles image SSE, keepalive, header filtering, and public-origin URL rewriting.

Tech Stack: Go, net/http, ChatHub events, existing M365 image handler, existing Sub2API image SSE relay, Go test/race/vet/build.

---

### Task 1: Define the progress writer with failing tests

Files:
- Create: internal/web/image_progress_stream_test.go
- Test: internal/web/image_progress_stream_test.go

- [ ] Step 1: Write failing tests

~~~go
func TestImageProgressStreamEmitsStageOnceAndNeverInventsPercent(t *testing.T) {
    gin.SetMode(gin.TestMode)
    recorder := httptest.NewRecorder()
    ctx, _ := gin.CreateTestContext(recorder)
    ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

    stream := newImageProgressStream(ctx, "image_generation", time.Hour)
    defer stream.Stop()
    require.NoError(t, stream.Start())
    require.NoError(t, stream.Stage("queued"))
    require.NoError(t, stream.Stage("queued"))
    require.NoError(t, stream.Stage("generating"))

    body := recorder.Body.String()
    require.Equal(t, 1, strings.Count(body, "\"stage\":\"queued\""))
    require.Equal(t, 1, strings.Count(body, "\"stage\":\"generating\""))
    require.NotContains(t, body, "\"percent\"")
    require.Contains(t, body, "event: image_generation.progress\\n")
}

func TestImageProgressStreamHeartbeatIsSSEComment(t *testing.T) {
    gin.SetMode(gin.TestMode)
    recorder := httptest.NewRecorder()
    ctx, _ := gin.CreateTestContext(recorder)
    ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

    stream := newImageProgressStream(ctx, "image_generation", 5*time.Millisecond)
    require.NoError(t, stream.Start())
    defer stream.Stop()
    require.Eventually(t, func() bool {
        return strings.Contains(recorder.Body.String(), ": image-generation keepalive")
    }, 200*time.Millisecond, time.Millisecond)
    require.NotContains(t, recorder.Body.String(), "\"stage\":\"heartbeat\"")
}
~~~

- [ ] Step 2: Verify RED

Run:

~~~powershell
go test ./internal/web -run 'TestImageProgressStream' -count=1
~~~

Expected: compilation failure because newImageProgressStream and its methods do not exist.

- [ ] Step 3: Commit the red tests

~~~powershell
git add internal/web/image_progress_stream_test.go
git commit -m "test(images): define live progress stream contract"
~~~

### Task 2: Implement the progress writer

Files:
- Create: internal/web/image_progress_stream.go
- Modify: internal/web/image_progress_stream_test.go

- [ ] Step 1: Implement the minimal writer

Define imageProgressStream with a mutex, standard http.ResponseWriter, request context, event prefix, start time, heartbeat interval, last stage, stop/done channels, and stopped state. Define newImageProgressStream(w http.ResponseWriter, ctx context.Context, prefix string, interval time.Duration), Start, Stage, and Stop. Start sets text/event-stream, Cache-Control: no-cache, Connection: keep-alive, and X-Accel-Buffering: no, then starts a ticker goroutine. Stage deduplicates by stage, marshals only type, stage, and elapsed_seconds, writes one SSE event, and flushes. The ticker writes the SSE comment ": image-generation keepalive\\n\\n" and flushes. All writes are mutex-protected; a write error stops the stream.

- [ ] Step 2: Verify GREEN and package compatibility

~~~powershell
go test ./internal/web -run 'TestImageProgressStream' -count=1
go test ./internal/web -count=1
~~~

- [ ] Step 3: Commit

~~~powershell
git add internal/web/image_progress_stream.go internal/web/image_progress_stream_test.go
git commit -m "feat(images): add deduplicated SSE progress writer"
~~~

### Task 3: Classify real ChatHub image events with failing tests

Files:
- Create: internal/web/image_progress_stage_test.go
- Create: internal/web/image_progress_stage.go

- [ ] Step 1: Write failing tests

~~~go
func TestImageProgressStageFromEvent(t *testing.T) {
    cases := []struct {
        name  string
        event chathub.StreamEvent
        want  string
    }{
        {"pending GraphicArt", chathub.StreamEvent{Kind: "progress", MessageType: "Progress", ContentType: "GraphicArt", ContentOrigin: "ImageGeneration"}, "generating"},
        {"pending ImageGeneration", chathub.StreamEvent{Kind: "progress", MessageType: "Progress", ContentType: "ImageGeneration", ContentOrigin: "ImageGeneration"}, "generating"},
        {"ordinary text", chathub.StreamEvent{Kind: "text", Text: "hello"}, ""},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            require.Equal(t, tc.want, imageProgressStageFromEvent(tc.event))
        })
    }
}
~~~

- [ ] Step 2: Verify RED

~~~powershell
go test ./internal/web -run 'TestImageProgressStageFromEvent' -count=1
~~~

Expected: compilation failure because imageProgressStageFromEvent does not exist.

- [ ] Step 3: Implement and verify the classifier

~~~go
func imageProgressStageFromEvent(event chathub.StreamEvent) string {
    if event.Kind != "progress" && event.Kind != "reasoning" {
        return ""
    }
    if event.MessageType != "Progress" {
        return ""
    }
    if event.ContentType != "GraphicArt" && event.ContentType != "ImageGeneration" {
        return ""
    }
    if event.ContentOrigin != "ImageGeneration" && event.ContentOrigin != "GraphicArt" {
        return ""
    }
    return "generating"
}
~~~

Run the focused test, then commit:

~~~powershell
go test ./internal/web -run 'TestImageProgressStageFromEvent' -count=1
git add internal/web/image_progress_stage.go internal/web/image_progress_stage_test.go
git commit -m "feat(images): classify GraphicArt progress stages"
~~~

### Task 4: Add stream-aware M365 Images handling

Files:
- Modify: internal/web/images.go lines 38-258 and 279-369
- Modify: internal/web/images_test.go when present

- [ ] Step 1: Parse stream

Extend imageGenerationRequest with Stream bool json:"stream". The JSON generation path uses the existing decoder. The multipart edit bridge reads stream with strconv.ParseBool; malformed values return the existing structured 400 error.

- [ ] Step 2: Wire the event callback

Before the existing blocking chatWithAccount call, select the path:

~~~go
var progress *imageProgressStream
if b.Stream {
    prefix := "image_generation"
    if b.Operation == "edit" {
        prefix = "image_edit"
    }
    progress = newImageProgressStream(w, prefix, 10*time.Second)
    if err := progress.Start(); err != nil {
        return
    }
    defer progress.Stop()
    if err := progress.Stage("queued"); err != nil {
        return
    }
}

onEvent := func(event chathub.StreamEvent) error {
    if progress == nil {
        return nil
    }
    if stage := imageProgressStageFromEvent(event); stage != "" {
        return progress.Stage(stage)
    }
    return nil
}

if b.Stream {
    res, err = s.chatWithAccountEvents(ctx, acc.ID, account, request, onEvent)
} else {
    res, err = s.chatWithAccount(ctx, acc.ID, account, request)
}
~~~

Use the existing account loop and failover rules. Once a real generating stage has been emitted, do not replay the non-idempotent image request on another account. Before each actual Designer download call emit downloading.

- [ ] Step 3: Emit final/error SSE without changing non-stream JSON

On successful stream serialization, emit exactly one image_generation.completed or image_edit.completed event per returned image using the existing URL or b64_json shape. On errors after SSE starts, emit one structured error event and no completion event. Keep stream:false on the current jsonOut path and retain its 55-second invisible whitespace keepalive behavior where configured.

- [ ] Step 4: Add focused handler tests

Cover JSON generation with stream:true, JSON generation with stream:false, multipart edit with stream=true, duplicate ChatHub progress snapshots, and absence of percent, prompt, account, token, pollUrl, and private host in progress payloads.

- [ ] Step 5: Verify and commit

~~~powershell
go test ./internal/web -run 'TestImageProgress|TestImageGeneration|TestImageEdit' -count=1
git add internal/web/images.go internal/web/images_test.go
git commit -m "feat(images): stream live M365 generation progress"
~~~

### Task 5: Verify Sub2API relay behavior

Files:
- Modify: backend/internal/service/openai_images_test.go
- Modify only if required: backend/internal/service/openai_images_responses.go

- [ ] Step 1: Add relay regression tests

Feed the existing API-key image forwarder a text/event-stream body containing image_generation.progress, an SSE comment heartbeat, and image_generation.completed. Assert progress and comments reach the client, image count is one, X-M365-* headers are absent, and final URLs remain on the request origin.

- [ ] Step 2: Run focused tests and commit any smallest relay fix separately

~~~powershell
go test ./backend/internal/service -run 'Test.*Images.*(Stream|Progress|Origin)' -count=1
git add backend/internal/service/openai_images_test.go backend/internal/service/openai_images_responses.go
git commit -m "test(images): preserve progress relay and origin filtering"
~~~

### Task 6: Full local and production acceptance

Files:
- Create: docs/deployments/2026-09-04-image-progress-stream-acceptance.md

- [ ] Step 1: Run local validation

~~~powershell
go test ./internal/chathub ./internal/web -count=1
go test -race ./internal/chathub ./internal/web -count=1
go vet ./...
go build ./...
git diff --check
~~~

Run equivalent focused, race, vet, build, and whitespace checks in the canonical Sub2API worktree.

- [ ] Step 2: Run real tests with distinct meaningful content

Use two different meaningful image prompts, one real PNG edit, one b64_json request, and both stream modes. Record timings, event names, statuses, byte counts, image magic, URL origin, and header-presence booleans only.

Required stream assertions:

~~~text
first SSE event <= 10 seconds when routing is healthy
stage order queued -> generating -> downloading -> completed
no duplicate progress stage
no percent field
no Cloudflare 524
fresh public URL starts with https://clove.asia/v1/images/files/
immediate download is HTTP 200 with image/* and complete Content-Length
X-M365-* is absent
~~~

- [ ] Step 3: Audit existing regression surfaces

Run Responses, Chat Completions, Anthropic Messages, one implicit real tool call, one tool-result continuation, long-context cache reuse, controlled account switching, Redis memory/key counts, M365/Sub2API RSS, restart counts, OOM flags, health, and recent panic/fatal logs. Confirm PostgreSQL, Redis, normalizer, and subscription-proxy container IDs did not change.

- [ ] Step 4: Commit the credential-free acceptance record

~~~powershell
git add docs/deployments/2026-09-04-image-progress-stream-acceptance.md
git commit -m "docs(images): record progress stream acceptance"
~~~

### Task 7: Immutable deployment with rollback

Files:
- Server: /opt/m365-copilot2api/docker-compose.yml
- Server rollback directory: /opt/m365-copilot2api/backups/image-progress-stream-<timestamp>

- [ ] Step 1: Build candidate

Tag M365 as m365-copilot2api:image-progress-stream-<commit>. Keep Sub2API at sub2api:image-client-errors-cb47b336e unless Task 5 requires a relay fix.

- [ ] Step 2: Save rollback artifacts

Save the active M365 Compose file, container inspection, and image inspection. Keep m365-copilot2api:image-graphicart-wait-675ea194ca30 available.

- [ ] Step 3: Recreate only M365

Change only the M365 image reference and recreate only that service. Do not restart Sub2API, Redis, PostgreSQL, normalizer, or subscription proxy.

- [ ] Step 4: Run post-deploy smoke tests

Check /health, then run both stream prompts, the real PNG edit, b64_json, Responses, Chat Completions, tool, and cache checks. Download every fresh URL before any later M365 restart because the current image mapping is process-local.

- [ ] Step 5: Roll back on any regression

Restore the saved Compose file and m365-copilot2api:image-graphicart-wait-675ea194ca30, recreate only M365, and rerun baseline health and smoke tests.

- [ ] Step 6: Push and complete

Push the M365 branch and any Sub2API branch changes to the private repository. Mark the active goal complete only after acceptance data and rollback artifacts exist.
