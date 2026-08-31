# Caller Shell Contract Preservation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve the caller-declared Windows, Linux, macOS, or unspecified shell contract when routing Codex `exec` custom tools, without changing public protocols, cache identity, affinity, or persistent state.

**Architecture:** Parse the existing level-three nested-tool headings in the caller-provided `exec` description, extract only the `shell_command` section, compact it independently, and append it as authoritative router context. Remove the unconditional PowerShell summary and strengthen the caller-local policy so a failed command is not described as an environment switch.

**Tech Stack:** Go 1.24, OpenAI Responses/Chat Completions/Anthropic adapters, M365 model tool router, Docker Compose, Redis 7.

---

### Task 1: Add Failing Caller-Shell Tests

**Files:**
- Modify: `internal/web/model_tool_router_test.go`
- Modify: `internal/web/protocol_compat_test.go`

- [ ] **Step 1: Add cross-environment contract preservation tests**

Add the following table test after `TestCompactRouterToolsKeepsExecNestedToolCatalog`:

```go
func TestCompactExecRouterDescriptionPreservesCallerShellContract(t *testing.T) {
	tests := []struct {
		name      string
		contract  string
		want      []string
		forbidden []string
	}{
		{
			name:     "windows powershell",
			contract: "Runs a PowerShell command on Windows. Use Get-Location; Get-ChildItem -Force.",
			want:     []string{"PowerShell", "Get-Location", "Get-ChildItem"},
			forbidden: []string{"pwd; ls -la"},
		},
		{
			name:     "linux bash",
			contract: "Runs a Bash command on Linux. Use pwd; ls -la; grep. Do not use PowerShell.",
			want:     []string{"Bash", "pwd; ls -la", "Do not use PowerShell"},
			forbidden: []string{"For PowerShell commands", "Get-Location"},
		},
		{
			name:     "macos posix",
			contract: "Runs a POSIX shell command on macOS. Use pwd; find . -maxdepth 2.",
			want:     []string{"POSIX", "macOS", "find . -maxdepth 2"},
			forbidden: []string{"For PowerShell commands", "Get-ChildItem"},
		},
		{
			name:     "unspecified caller shell",
			contract: "Runs commands in the caller-declared default shell. Follow this contract exactly.",
			want:     []string{"caller-declared default shell", "Follow this contract exactly"},
			forbidden: []string{"PowerShell", "Bash", "Windows", "Linux", "macOS"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			description := "Run JavaScript through the caller bridge.\n" +
				"### apply_patch\nPATCH_SECTION_SENTINEL\n" +
				"### shell_command\n" + tt.contract + "\n" +
				"### view_image\nVIEW_IMAGE_SECTION_SENTINEL"
			got := compactExecRouterDescription(description)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("router description missing %q: %q", want, got)
				}
			}
			for _, forbidden := range tt.forbidden {
				if strings.Contains(got, forbidden) {
					t.Fatalf("router description introduced %q: %q", forbidden, got)
				}
			}
			if strings.Contains(got, "VIEW_IMAGE_SECTION_SENTINEL") {
				t.Fatalf("shell section leaked into the next tool: %q", got)
			}
			if len(got) > maxExecRouterDescriptionBytes {
				t.Fatalf("exec description is unbounded: %d", len(got))
			}
		})
	}
}
```

- [ ] **Step 2: Add missing-section and bounded-section tests**

```go
func TestCompactExecRouterDescriptionHandlesMissingAndLongShellContracts(t *testing.T) {
	missing := compactExecRouterDescription("Run JavaScript.\n### shell_command\n   \n### apply_patch\nApply a patch.")
	if !strings.Contains(missing, "environment is unspecified") || strings.Contains(missing, "PowerShell") {
		t.Fatalf("missing shell contract gained an environment: %q", missing)
	}

	longContract := "SHELL_CONTRACT_HEAD " + strings.Repeat("x", maxExecRouterDescriptionBytes*2) + " SHELL_CONTRACT_TAIL"
	bounded := compactExecRouterDescription("Run JavaScript.\n### shell_command\n" + longContract)
	if len(bounded) > maxExecRouterDescriptionBytes || !strings.Contains(bounded, "SHELL_CONTRACT_HEAD") || !strings.Contains(bounded, "SHELL_CONTRACT_TAIL") {
		t.Fatalf("bounded shell contract invalid: len=%d value=%q", len(bounded), bounded)
	}
}
```

- [ ] **Step 3: Assert the caller-local policy rejects false environment-switch claims**

Extend `TestResponsesInstructionsAndCustomExecPolicyAreSystemMessages` with:

```go
policy := fmt.Sprint(o.Messages[0].Content)
for _, want := range []string{"caller-provided shell contract is authoritative", "does not mean", "remote container"} {
	if !strings.Contains(policy, want) {
		t.Fatalf("custom exec policy missing %q: %q", want, policy)
	}
}
```

- [ ] **Step 4: Run the focused tests and verify RED**

Run:

```powershell
go test ./internal/web -run 'TestCompactExecRouterDescription|TestResponsesInstructionsAndCustomExecPolicy' -count=1
```

Expected: FAIL because the current implementation omits every caller-provided
shell section and introduces the unconditional `For PowerShell commands` text
for Linux, macOS, and unspecified environments.

### Task 2: Preserve The Authoritative Nested Shell Section

**Files:**
- Modify: `internal/web/model_tool_router.go`
- Modify: `internal/web/protocol_compat.go`
- Test: `internal/web/model_tool_router_test.go`
- Test: `internal/web/protocol_compat_test.go`

- [ ] **Step 1: Add the independent shell-contract limit**

Add to the router constants:

```go
maxExecShellContractBytes = 1200
```

- [ ] **Step 2: Extract one nested tool section without leaking the next**

Add after `execNestedToolNames`:

```go
func execNestedToolSection(description, target string) string {
	matches := execNestedToolHeadingPattern.FindAllStringSubmatchIndex(description, -1)
	for i, match := range matches {
		if len(match) < 4 || !strings.EqualFold(description[match[2]:match[3]], target) {
			continue
		}
		end := len(description)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		return strings.TrimSpace(description[match[1]:end])
	}
	return ""
}
```

- [ ] **Step 3: Replace the PowerShell default with the caller contract**

Replace the `available["shell_command"]` block in
`compactExecRouterDescription` with:

```go
if available["shell_command"] {
	summary.WriteString(" The caller-provided shell_command contract is authoritative. Preserve its shell, operating-system, path, quoting, and command-separator rules exactly. A command error does not prove that the caller environment changed. ")
	contract := execNestedToolSection(description, "shell_command")
	if contract == "" {
		summary.WriteString("The caller shell environment is unspecified; do not assume one or invent shell-specific commands.")
	} else {
		summary.WriteString("Caller shell_command contract:\n")
		summary.WriteString(compactToolResult(contract, maxExecShellContractBytes))
	}
}
```

Keep the final `compactToolResult(summary.String(), maxExecRouterDescriptionBytes)` call unchanged.

- [ ] **Step 4: Strengthen the caller-local workspace policy**

Append these sentences to `customExecWorkspaceInstruction`:

```text
The caller-provided shell contract is authoritative. A command failure does not mean the workspace moved or the shell changed. Never claim execution switched to a remote container unless a matching caller tool result explicitly says so.
```

- [ ] **Step 5: Run focused tests and verify GREEN**

```powershell
go test ./internal/web -run 'TestCompactExecRouterDescription|TestCompactRouterToolsKeepsExecNestedToolCatalog|TestResponsesInstructionsAndCustomExecPolicy' -count=1
```

Expected: PASS.

### Task 3: Run Protocol, Tool, And Cache Regressions

**Files:**
- Test: `internal/web/*_test.go`

- [ ] **Step 1: Run router and tool regressions**

```powershell
go test ./internal/web -run 'Test(Router|Tool|Responses|Anthropic|Claude|CompactExec|CompactRouter)' -count=1
```

Expected: PASS with no call-ID, schema, continuation, or adapter regression.

- [ ] **Step 2: Run the full local gates**

```powershell
go test ./... -count=1
go test -race ./internal/web -count=1
go vet ./...
go build ./...
git diff --check
```

Expected: every command exits 0.

- [ ] **Step 3: Scan and commit the source fix**

Stage only the two implementation files and two test files. Reject the commit
if the staged diff contains API keys, account credentials, VPS credentials, or
private tokens. Commit with:

```powershell
git commit -m "fix(router): preserve caller shell contracts"
```

### Task 4: Verify The Fix Through Production Protocols

**Files:**
- Update: `docs/deployments/2026-08-31-cross-protocol-tool-acceptance.md`
- Update: `docs/deployments/2026-08-31-real-tool-production-acceptance.md`

- [ ] **Step 1: Re-run the exact Linux failure probe after deployment**

Declare a Codex `exec` tool whose `shell_command` section requires Linux/Bash,
contains `pwd; ls -la`, and prohibits PowerShell. Keep the user request generic.
Require the first custom tool call to contain POSIX syntax and no
`Get-Location` or `Get-ChildItem`.

- [ ] **Step 2: Execute two different real Linux commands**

Execute the first emitted command on the VPS and a second command that reads
`PRETTY_NAME` from `/etc/os-release`. Return each real result through its
original call ID and require a completed final response without an
environment-switch claim.

- [ ] **Step 3: Execute two different real Windows commands**

Declare a Windows/PowerShell contract, require `Get-Location` and
`Get-ChildItem`, execute both in the real Windows workspace, return the real
results, and require no POSIX syntax.

- [ ] **Step 4: Repeat through all public formats**

Run Responses non-stream/SSE, Chat Completions non-stream/SSE, and Anthropic
Messages non-stream/SSE with different meaningful paths and questions. Record
argument fidelity, call IDs, tool result acceptance, account/binding
continuity, input/cached tokens, cache percentage, first-token latency, total
latency, stop/finish reasons, and zero public `X-M365-*` headers.

- [ ] **Step 5: Audit health and rollback artifacts**

Verify the gateway image and commit, restart count, 31 account states, Redis
AOF/RDB and eviction/rejection counters, Sub2API Anthropic group permission,
zero new HTTP 500/502 or panic/fatal entries, and the pre-deploy Compose/image
rollback backup.
