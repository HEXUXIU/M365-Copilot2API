# Real Tool Production Acceptance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Exercise every real tool exposed to this Codex desktop thread through the production M365 Responses gateway, execute each emitted call on the actual host, return its real result, and distinguish protocol failures from unavailable fixtures or intentionally invalid state.

**Architecture:** A temporary credential-free Python probe sends explicit Responses tool contracts to the production Sub2API endpoint and records response IDs, call IDs, arguments, usage, and timing. The current Codex host executes emitted calls through the corresponding real tool API, then the probe submits `function_call_output` or `custom_tool_call_output` through `previous_response_id`. Read-only tools run in parallel batches; stateful tools use disposable goals, files, browser state, automations, and Codex tasks with cleanup.

**Tech Stack:** Python 3 standard library, OpenAI Responses-compatible HTTP/SSE, Codex desktop tool APIs, PowerShell 7, Go 1.24, Redis 7, Docker Compose.

---

### Task 1: Freeze The Tool Inventory And Safety Classes

**Files:**
- Create: `work/real-tool-probe.py` outside the Git repository as a temporary test driver
- Create: `docs/deployments/2026-08-31-real-tool-production-acceptance.md`

- [ ] **Step 1: Record the 30 runtime tool names**

Query `ALL_TOOLS`, sort the names, and record each tool's argument declaration. Add collaboration tools to a separate list because they are not part of `ALL_TOOLS` and require an explicit delegation request.

- [ ] **Step 2: Assign every tool one outcome class**

Use exactly these classes: `e2e_pass`, `negative_path_pass`, `fixture_unavailable`, `not_executed_by_constraint`, or `failed`. Never count an empty MCP resource list as a tool failure, and never count an unexecuted state-changing action as a pass.

- [ ] **Step 3: Define disposable targets**

Use `tmp/real-tool-acceptance/` for file operations, the current task ID for no-op navigation/read operations, a disabled disposable automation for automation CRUD, and a disposable projectless Codex task for title/pin/archive/send/wait tests. Delete or restore every disposable target after its final assertion.

### Task 2: Build The Credential-Free Responses Probe

**Files:**
- Create: `work/real-tool-probe.py`

- [ ] **Step 1: Implement `begin`**

Accept a base64-encoded JSON specification containing `model`, `instructions`, `input`, `tools`, `tool_choice`, and `stream`. Read the API key only from `M365_TEST_API_KEY`, POST to `/v1/responses`, and print one JSON object with `response_id`, normalized calls, usage, public M365 response headers, response-header latency, first-event latency, and total latency.

- [ ] **Step 2: Implement `finish`**

Accept the same specification, a response ID, and base64-encoded real tool results. Resend the original instructions and tools with `tool_choice=auto`, submit each result with its original `call_id`, and assert a completed response without an unintended repeated tool call.

- [ ] **Step 3: Validate the probe locally**

Run:

```powershell
python -m py_compile work/real-tool-probe.py
python work/real-tool-probe.py self-test
```

Expected: `PY_COMPILE_OK` followed by JSON showing base64 round-trip, function-call normalization, custom-call normalization, SSE event parsing, and unique call-ID checks all passed.

### Task 3: Exercise Core Read-Only Function Tools

**Files:**
- Use: `work/real-tool-probe.py`
- Update: `docs/deployments/2026-08-31-real-tool-production-acceptance.md`

- [ ] **Step 1: Request one parallel batch**

Ask M365 to call each tool exactly once: `get_goal`, `codex_app__load_workspace_dependencies`, `codex_app__read_thread_terminal`, `list_mcp_resources`, `list_mcp_resource_templates`, `codex_app__list_projects`, and `codex_app__list_threads`. Assert seven unique non-empty call IDs and valid JSON arguments.

- [ ] **Step 2: Execute all seven real host tools**

Invoke the emitted calls through their real APIs and retain bounded structured outputs. Do not replace host results with synthetic strings.

- [ ] **Step 3: Return all seven outputs together**

Submit a seven-item `function_call_output` array through `previous_response_id`. Assert HTTP 200, completed status, no repeated call, one stable account/binding, and nonzero cached tokens on a stable-prefix continuation.

### Task 4: Exercise Shell, Patch, And Image Tools

**Files:**
- Create and delete: `tmp/real-tool-acceptance/patch-proof.txt`
- Use image: `work/m365-super-lock/docs/screenshots/02-dashboard.png`

- [ ] **Step 1: Test PowerShell selection and execution**

Expose `shell_command` as a custom tool with the Windows/PowerShell contract. Require a command that prints the current directory and `M365_REAL_TOOL_SHELL_OK`; execute it through the real shell tool and return the real output.

- [ ] **Step 2: Test apply-patch execution**

Expose `apply_patch` as a custom tool. Require a valid patch that adds `tmp/real-tool-acceptance/patch-proof.txt` containing `M365_REAL_TOOL_PATCH_OK`; execute it through the real patch tool and verify the file content with `shell_command`.

- [ ] **Step 3: Test image inspection**

Expose `view_image` as a function tool with the exact existing dashboard screenshot path and `detail=high`. Execute the real image tool, verify that a nonempty image data URL is returned, and send a bounded factual result to M365.

- [ ] **Step 4: Clean up the patch fixture**

Use `apply_patch` to delete only `tmp/real-tool-acceptance/patch-proof.txt`, confirm the file is absent, and leave unrelated files untouched.

### Task 5: Exercise Goal, Plan, MCP, And Browser Runtime Tools

**Files:**
- Update: `docs/deployments/2026-08-31-real-tool-production-acceptance.md`

- [ ] **Step 1: Test goal and plan state**

Call `get_goal`, update the active plan with the current statuses, and read the goal again. Test `create_goal` only after the current goal is complete or record the expected active-goal rejection as a negative-path pass. Test `update_goal` only at the terminal acceptance step.

- [ ] **Step 2: Test MCP discovery and read behavior**

Call both MCP listing tools. If a resource exists, call `read_mcp_resource` with its exact server and URI. If none exists, call it once with a deliberately invalid local fixture reference and require a structured error; classify the read path as `fixture_unavailable`, not `e2e_pass`.

- [ ] **Step 3: Test the JavaScript runtime in dependency order**

Call `mcp__node_repl__js_add_node_module_dir` with the bundled Node module path, call `mcp__node_repl__js` to produce `{marker:"M365_NODE_REPL_OK",value:42}`, reset through `mcp__node_repl__js_reset`, then call JavaScript again and verify the prior binding is absent while the runtime remains usable.

### Task 6: Exercise Codex App Task Management Tools

**Files:**
- Update: `docs/deployments/2026-08-31-real-tool-production-acceptance.md`

- [ ] **Step 1: Create one disposable projectless task**

Use `codex_app__create_thread` with a prompt that returns `M365_DISPOSABLE_THREAD_READY`. Capture its real thread and host IDs, then use `codex_app__wait_threads` until it completes.

- [ ] **Step 2: Test read, send, title, pin, and archive round trips**

Read the disposable task, send `Return exactly M365_FOLLOWUP_OK`, wait for completion, rename it to `M365 real-tool disposable`, pin then unpin it, archive then unarchive it, and verify each visible state through the corresponding read/list tool.

- [ ] **Step 3: Test fork and handoff paths**

Fork the disposable task into the same directory and read the fork. Attempt handoff only if the task has a supported project checkout; otherwise require the documented structured unsupported-state result and classify it as a negative-path pass. Query `get_handoff_status` only when a real operation ID is returned.

- [ ] **Step 4: Test open and navigate without changing user data**

Open the acceptance report in a Codex file panel and navigate to the current task ID. Both operations must return success without renaming, archiving, or moving the current task.

- [ ] **Step 5: Archive disposable tasks**

Archive every disposable task and fork. Keep the current task unchanged.

### Task 7: Exercise Automation CRUD

**Files:**
- Update: `docs/deployments/2026-08-31-real-tool-production-acceptance.md`

- [ ] **Step 1: Create a disabled disposable automation**

Create a local heartbeat named `M365 real-tool disposable automation`, with disabled status and failed-run-only notifications, targeting the current task. Because it is disabled, it must never execute.

- [ ] **Step 2: View and delete the automation**

Read it by returned ID, assert name/status/target, delete it, and verify a later view returns not found. Do not leave a scheduled job behind.

### Task 8: Repeat Cache, Protocol, And Production Audits

**Files:**
- Create: `docs/deployments/2026-08-31-real-tool-production-acceptance.md`

- [ ] **Step 1: Repeat failed or ambiguous cases with different inputs**

Every functional failure gets one root-cause inspection and at least two new probes with different meaningful inputs. Do not retry malformed arguments unchanged.

- [ ] **Step 2: Audit protocol evidence**

Record unique call IDs, output acceptance, response events, account/binding continuity, cached tokens, TTFT, total duration, and public header visibility for each batch.

- [ ] **Step 3: Audit production health**

Verify all 31 accounts, Redis AOF/RDB, gateway restart count, HTTP status distribution, and zero new panic/fatal/500/502/tool-identity/alias errors from the test window.

- [ ] **Step 4: Commit and push the report**

Run:

```powershell
go test ./... -count=1
git diff --check
git add docs/superpowers/plans/2026-08-31-real-tool-production-acceptance.md docs/deployments/2026-08-31-real-tool-production-acceptance.md
git commit -m "test(tools): record production real-tool acceptance"
git push origin refs/heads/production-super-20260831:refs/heads/production-super-20260831
```

Expected: tests pass, no secrets are staged, and the remote production branch advances without force-push.
