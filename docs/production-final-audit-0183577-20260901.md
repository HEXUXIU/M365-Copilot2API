# Production final audit — `production-super-0183577`

Date: 2026-09-01 (Asia/Shanghai)

## Runtime

- Image: `m365-copilot2api:production-super-0183577`
- Container: running; restart count `0`; OOM `false`
- Sub2API, Redis, PostgreSQL, subscription proxy, and header-hiding ingress: running
- Recent application window: `113` HTTP 200, `2` HTTP 400, `1` HTTP 401; no application 502/503/504
- Redis: `0` evicted keys, `0` rejected connections; counters remained healthy

The two 400s and one 401 are request-level validation/auth records from concurrent client traffic. No panic, fatal, cooldown loop, invalid-grant storm, or upstream-unavailable message was present in the audit window.

## Protocol and tool acceptance

- Responses SSE: passed with a real Windows PowerShell command; exact tool arguments and result continuation verified.
- Chat Completions SSE: passed on isolated rerun with a real Ubuntu Bash command; exact arguments, result continuation, and no diagnostic headers verified.
- Anthropic Messages SSE: passed; complete event sequence, stable `call_id`, no repeated tool call, and no diagnostic headers.
- Existing environment matrix remains 12/12 passed across Responses, Chat Completions, and Anthropic, covering Windows PowerShell, Ubuntu Bash, and undeclared environments.

The first combined SSE run saw one Cloudflare 502 while several long-running streams were active. A single-protocol rerun passed; the gateway and Sub2API logs showed no corresponding application 502 or process fault.

## Cache and failover

- Real long-context continuation: `95,622 / 95,748` cached input tokens (`99.87%`).
- Tool-result continuations: approximately `99.44%–99.49%` cache reads.
- Forced unavailable-affinity migration: cold first migrated request, then `27,247 / 27,286` cached (`99.86%`); migration and account change observed in logs.
- Accounts: `31/31` online and scheduled; disabled/cooldown: `0`.
- Subscription proxy: 30 configured exits, url-test selection, 60-second checks, 50 ms tolerance; health check around 0.9 s.

## Claude CLI probe

- Claude Code package `2.1.252` installed in an isolated temporary directory.
- Local CLI version probe passed: `2.1.252 (Claude Code)`.
- Native CLI one-shot invocation did not finish within 120 seconds against the compatibility gateway. No production state changed. The supported Anthropic Messages API path used by Claude Code-style clients passed independently, so this is recorded as a client bootstrap/protocol compatibility finding rather than a production gateway regression.

## Local verification

All completed successfully on the `production-super-20260831` source checkout:

```text
go test ./... -count=1
go test -race ./internal/web ./internal/chathub -count=1
go vet ./...
go build ./...
git diff --check
```

No credentials, tokens, or temporary runtime files are included in this report.

