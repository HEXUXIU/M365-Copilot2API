# Caller Shell Contract Preservation Design

## Goal

Preserve the caller-declared shell environment when the model-driven router
compacts a Codex `exec` custom tool. A Windows caller must receive PowerShell,
a Linux or macOS caller must receive its declared POSIX shell syntax, and an
unspecified caller must not be assigned an environment by the gateway.

The change must retain the production cache-first design, tool-call identity,
cross-protocol instruction preservation, response history, affinity, account
scheduling, and public response formats.

## Production Evidence

The current production router was given an `exec` tool whose nested
`shell_command` contract explicitly required Linux/Bash commands such as
`pwd` and `ls -la` and explicitly prohibited PowerShell. The returned custom
tool call nevertheless contained:

```javascript
const result = await tools.shell_command({command: "Get-Location; Get-ChildItem"});
text(result);
```

The behavior is deterministic. `compactExecRouterDescription` currently sees
the nested tool name `shell_command` and unconditionally emits a PowerShell
instruction. It discards the authoritative nested tool section that describes
the caller's actual environment. When the generated command fails on Linux,
the model may incorrectly explain the failure as an execution-environment
change.

This is independent from the already-fixed loss of Responses `instructions`,
Chat Completions system/developer messages, and Anthropic `system` content.
Both fixes are required: request instructions describe the task, while the
tool contract describes where and how the caller will execute it.

## Chosen Design

Treat the nested `shell_command` section in the caller-provided `exec`
description as authoritative. Parse Markdown level-three tool headings with
the existing heading syntax, extract only the selected section up to the next
level-three tool heading, normalize whitespace, and compact it to a bounded
size.

The compact router description will continue to list all available nested tool
names. When `shell_command` is available, it will append a clearly labeled,
bounded copy of that tool's caller-provided contract. It will instruct the
router to preserve that contract and never substitute PowerShell, Bash, POSIX,
Windows, Linux, macOS, paths, or command separators that the contract did not
declare.

If the description lists `shell_command` but provides no usable section body,
the router will state that the environment is unspecified. It must use only a
probe explicitly allowed by the caller's contract or tool metadata and must not
guess an operating system from a path, an earlier command, or an execution
error.

The caller-local workspace policy will also state that a command error does not
prove an environment switch and that the model must not claim execution moved
to a remote container without a matching caller tool result.

## Data Flow

```text
Codex caller tool declaration
  -> custom exec description
  -> level-three nested-tool section parser
  -> bounded authoritative shell_command contract
  -> compact model-router tool definition
  -> structured exec custom_tool_call
  -> caller host executes the nested shell_command
  -> real tool result returns through the original call_id
```

The gateway never executes the command and never derives the host OS itself.
Execution remains exclusively on the caller-provided bridge.

## Boundaries

- Preserve the nested `shell_command` section, not the entire large `exec`
  description.
- Bound the extracted shell contract independently so a large tool manual
  cannot crowd out the task or materially degrade cache locality.
- Stop extraction at the next level-three nested-tool heading so unrelated tool
  contracts do not leak into the shell summary.
- Keep nested tool order, public tool names, schemas, call IDs, validation, and
  repair behavior unchanged.
- Do not infer a shell from request paths, working-directory strings, prior
  assistant prose, or failed command output.
- Do not translate commands in the gateway. The model emits commands according
  to the caller contract and the caller executes them.
- Do not change Redis keys, affinity digests, account scheduling, database
  schema, settings UI, response headers, or Sub2API routing configuration.
- Anthropic endpoint permission remains a separate production configuration
  correction and is not hidden inside this source change.

## Error Handling

An absent or empty nested section produces an explicit unspecified-environment
summary rather than a default PowerShell summary. Oversized sections use the
existing deterministic head-and-tail compaction helper. Invalid model output
continues through the existing schema validation and repair path.

After a real tool error, the continuation retains the same request-level
instructions, tool declarations, response identity, and caller-local policy.
The model may correct syntax based on authoritative evidence but must not claim
that execution changed hosts without such evidence.

## Verification

Automated tests must prove:

1. A Windows/PowerShell nested contract survives compaction with its commands.
2. A Linux/Bash nested contract survives compaction and no PowerShell default is
   introduced.
3. A macOS/POSIX contract survives without Windows syntax.
4. An unspecified contract remains unspecified and does not gain an OS.
5. Extraction stops at the next nested-tool heading.
6. Long contracts stay within the independent byte limit while preserving head
   and tail markers.
7. Existing nested-tool discovery, unavailable-tool rejection, router
   validation, Responses, Chat Completions, Anthropic, affinity, and cache tests
   remain green.

Production acceptance must use different meaningful tasks for Windows and
Linux contracts. Each first tool turn must contain correct shell syntax, real
host execution must succeed, the result must return through the original call
ID, the final answer must reflect the real result, and public responses must
contain no `X-M365-*` headers. The same tool contract must then be repeated to
record cache usage, first-token latency, and total latency.

## Rollback

The code change is isolated to router-only tool-description compaction and the
caller-local policy text. Rollback restores the previous gateway image and
Compose file. No database, Redis, account, or Sub2API state migration is
required.
