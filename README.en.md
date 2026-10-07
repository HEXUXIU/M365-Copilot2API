<p align="center">
  <strong>English</strong> | <a href="./README.md">简体中文</a>
</p>

# M365 Copilot2API

<p align="center">
  <img src="https://img.shields.io/github/license/HEXUXIU/M365-Copilot2API" alt="License">
  <img src="https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go" alt="Go Version">
  <img src="https://img.shields.io/badge/API-OpenAI%20Compatible-412991?logo=openai" alt="OpenAI Compatible">
  <img src="https://img.shields.io/badge/API-Anthropic%20Compatible-FF6B6B?logo=anthropic" alt="Anthropic Compatible">
</p>

<p align="center">
  <strong>Microsoft 365 Copilot → OpenAI / Anthropic-compatible API gateway</strong>
</p>

M365 Copilot2API is a self-hosted gateway written in Go that translates the **ChatHub private protocol** (WebSocket) behind a Microsoft 365 Copilot commercial subscription into a standard **OpenAI / Anthropic-compatible API**. Claude Code, OpenCode, Cursor, and any OpenAI client can call M365 Copilot directly using the format they already know.

In short: **ChatHub private protocol ⇄ OpenAI / Anthropic-compatible API**. Connection handshakes, heartbeat keep-alives, event-stream parsing, and tool-call conversion are all encapsulated in the `internal/chathub` layer; only standard endpoints such as `/v1/chat/completions` and `/v1/messages` are exposed externally.

The project ships with a complete web admin console covering account authorization (OAuth/PKCE), API key management, a proxy pool, cloud conversation management, usage statistics, and model testing — suited for personal self-deployment and self-hosting.

> ⚠️ **Disclaimer (please read carefully)**
>
> - This project is **not an official Microsoft product** and has **no affiliation or partnership** with Microsoft, OpenAI, Anthropic, or their affiliates.
> - Accessing M365 services through third-party account pools, proxy relays, etc. **may violate the service providers' Terms of Service**; any consequences are the user's own responsibility.
> - Please comply with your local laws and regulations and the Terms of Service (ToS) of the target platform.
> - This project is **for personal learning and research only**; **commercial resale or large-scale operation is prohibited**.
> - The maintainers and contributors of this project **bear no responsibility** for account bans, data loss, or any other losses.

## Screenshots

<p align="center"><img src="docs/screenshots/02-dashboard.png" alt="Dashboard" style="max-width:860px;border-radius:12px;box-shadow:0 8px 32px rgba(0,0,0,.18)"></p>

<table>
  <tr>
    <td align="center" width="33%"><img src="docs/screenshots/01-login.png" alt="Login page" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>Login</b></sub></td>
    <td align="center" width="33%"><img src="docs/screenshots/03-usage.png" alt="Usage stats" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>Usage stats</b></sub></td>
    <td align="center" width="33%"><img src="docs/screenshots/04-accounts.png" alt="Account management" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>Account management</b></sub></td>
  </tr>
  <tr>
    <td align="center" width="33%"><img src="docs/screenshots/05-apikeys.png" alt="API Keys" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>API Keys</b></sub></td>
    <td align="center" width="33%"><img src="docs/screenshots/06-conversations.png" alt="Conversation management" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>Conversation management</b></sub></td>
    <td align="center" width="33%"><img src="docs/screenshots/07-proxies.png" alt="Proxy pool" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>Proxy pool</b></sub></td>
  </tr>
  <tr>
    <td align="center" width="33%"><img src="docs/screenshots/08-settings.png" alt="Settings" style="border-radius:10px;box-shadow:0 4px 16px rgba(0,0,0,.12)"><br><sub><b>Settings</b></sub></td>
    <td align="center" width="33%"><sub><i>More features waiting to be discovered</i></sub></td>
  </tr>
</table>

## Features

| Feature | Description |
|------|------|
| OpenAI-compatible `/v1/chat/completions` | Supports streaming output and function calling |
| OpenAI Responses `/v1/responses` | Compatible with the Responses protocol (Codex and other clients) |
| Anthropic-compatible `/v1/messages` | Direct connection for Claude Code / Cursor |
| SSE streaming | Real-time token-by-token output, `stream: true` |
| Tool-call conversion | OpenAI function calling ⇄ M365 tool protocol, with `router` / `native` planning modes |
| Context-key session reuse | Reuses cloud conversations keyed by conversation context; on a hit, only the incremental message is sent (similar to DeepSeek context caching) |
| Explicit session binding | The `X-M365-Session-Id` request header precisely specifies which session to continue |
| Auto cleanup | Reclaims cloud conversations by idle time (default 2h) or retention count |
| Multi-account management | PKCE authorization + account round-robin + automatic failover |
| API key management | Create / revoke / read back from the console |
| Proxy pool | HTTP / HTTPS / SOCKS5 proxy rotation, health checks, failure cooldown |
| Usage statistics | Aggregated by key / account / model / endpoint (`usage.jsonl`) |
| Cache-hit statistics | Hit-rate and token-savings dashboard |
| Multimodal input | Supports image attachments (base64 data URL / https URL), with automatic M365 upload and message-annotation injection |
| Image generation | `/v1/images/generations` |
| Web console | Single-screen management of accounts, keys, proxy pool, models, conversations, and logs |

## Architecture

```
┌──────────────┐   OpenAI / Anthropic    ┌─────────────────────────┐    ChatHub    ┌──────────────┐
│ Claude Code  │ ──────────────────────► │         Gateway          │ ────────────► │ M365 Copilot │
│ OpenCode     │  /v1/chat/completions    │ (Go, m365-copilot2api)  │   WebSocket   │(cloud convo) │
│ Any OpenAI   │  /v1/messages            │  internal/web           │  internal/    │              │
│ client       │  /v1/responses           │                         │  chathub      │              │
└──────────────┘                          └─────────────────────────┘               └──────────────┘
```

- **Protocol layer (`internal/chathub`)**: Wraps the WebSocket private protocol of M365 Copilot ChatHub — connection setup, heartbeat keep-alive, and event-stream parsing (streaming tokens, tool calls, multimodal input). Exposes a single unified event interface to the layers above.
- **Session resolution (`internal/web/session_resolver.go`)**: In multi-account scenarios, resolves each client request stably to a fixed account and cloud conversation, and implements context-key session reuse (see the mechanism below).
- **Account round-robin and failover**: Balances traffic across multiple accounts via round-robin; on account failure (auth expiry, disconnects, etc.) automatically switches to the next available account and retries.

## Quick Start

### Requirements

- Go 1.23+ (the minimum version declared in `go.mod`)
- Works on Windows / Linux; on Windows, using the bundled `manage.py` to manage the lifecycle is recommended

### Precompiled binaries (recommended)

Download the binary for your platform from [GitHub Releases](https://github.com/HEXUXIU/M365-Copilot2API/releases):

| Platform | Architecture | File |
|------|------|------|
| Linux | x86_64 / arm64 / i386 | `m365-copilot2api-linux-{amd64,arm64,386}` |
| Windows | x86_64 / arm64 / i386 | `m365-copilot2api-windows-{amd64,arm64,386}.exe` |
| macOS | x86_64 / arm64 | `m365-copilot2api-darwin-{amd64,arm64}` |

```bash
# Linux / macOS example
chmod +x m365-copilot2api-linux-amd64
./m365-copilot2api-linux-amd64
```

```powershell
# Windows example
.\m365-copilot2api-windows-amd64.exe
```

### Build from source

```powershell
git clone https://github.com/HEXUXIU/M365-Copilot2API.git
cd M365-Copilot2API

# Set the admin password (optional, defaults to admin123); always set a strong password in production
$env:M365_ADMIN_PASSWORD = "your_strong_password"

go build -o m365-copilot2api.exe ./cmd/server
```

```bash
# Linux / macOS
export M365_ADMIN_PASSWORD=your_strong_password
go build -o m365-copilot2api ./cmd/server
```

### Startup

On Windows, start with `manage.py` (runs in the background by default; logs are written to `server.log` / `server-error.log`):

```powershell
python manage.py start    # run in background, listens on 0.0.0.0:4141 by default
python manage.py status   # check running status
python manage.py logs     # view recent logs (optionally pass N for number of lines)
python manage.py err      # view error logs
python manage.py stop     # stop the service
```

> `manage.py` hardcodes an absolute repo path internally (e.g. `D:\M365-Copilot2API\m365-copilot2api.exe`); if you clone into a different directory, edit the path constants at the top of the script first, and make sure you've built the binary already.

Running the binary directly listens only on the internal network at `http://127.0.0.1:4141` by default; override it with the `M365_LISTEN` environment variable.

### Docker deployment

> No official Dockerfile is provided. For containerized deployment, build your own image from the precompiled binary or source, or discuss community approaches in Discussions.

### Initialization and first call

Open the console in your browser (default `http://127.0.0.1:4141`):

1. Log in with the admin password (changing the password is **mandatory on first login**; default password is `admin123`).
2. On the "Accounts" page, click **Start Authorization**:
   - A new browser window will pop up and redirect to the Microsoft sign-in page.
   - Complete the sign-in with your M365 account.
   - After signing in, the popup window will show a blank page or an error page — **this is expected**, because the callback endpoint isn't a real website and the authorization **is not yet complete**.
   - Copy the full URL (including the `code=...&state=...` parameters) from the popup window's **address bar**.
   - Back in the console, paste the URL into the "Callback URL" field and click "Confirm and add".
   - If your browser blocked the popup, allow popups for this site and try again.
3. Once authorized, **create your first API key** on the "API Key" page.
4. Verify the setup with the API examples below.

> If you have multiple M365 accounts, you can repeat the authorization for each; the gateway automatically schedules all of them using round-robin + failover.

## Configuration

Everything is configured via environment variables; you can use `.env.example` as a starting point. On startup, the app prioritizes explicitly-set environment variables.

### Core

| Variable | Default | Description |
|------|--------|------|
| `M365_LISTEN` | `127.0.0.1:4141` | Listen address (`manage.py` and Docker default to `0.0.0.0:4141`) |
| `M365_ADMIN_PASSWORD` | `admin123` | Admin password (must be changed on first login) |
| `M365_DATA_DIR` | `~/.config/m365-copilot2api` | Data directory (centralized storage for tokens, keys, usage, etc.; `manage.py` defaults to `data/`) |
| `M365_CONFIG` | `~/.config/m365-copilot2api/accounts.json` | Path to the account configuration file |
| `M365_SESSION_TTL_MINUTES` | `120` | Session-binding lifetime (minutes); cleared from `sessions.json` on expiry |
| `M365_CONTEXT_TTL_MINUTES` | `120` | Context-fingerprint reuse window (minutes) |
| `M365_CONTEXT_SIMILARITY` | `0.6` | Context-similarity reuse threshold (0-1, Jaccard similarity) |
| `M365_LOG_LEVEL` | `info` | Log level |
| `M365_ACCOUNT_DEFAULT_CONCURRENCY` | `8` | Max concurrent upstream calls per account; other accounts can still accept requests |
| `M365_PUBLIC_IDENTITY_POLICY` | `false` | Master switch for the public identity policy; only enables identity presets plus body/reasoning/citation/streaming sanitization when explicitly set to `true` on Microsoft reverse-proxy channels |

### Auto cleanup

Cloud conversations are treated as "cache entries": a session hit automatically refreshes its lifetime; conversations that are idle too long or exceed the count cap are reclaimed by a background loop.

| Variable | Default | Description |
|------|--------|------|
| `M365_AUTO_CLEANUP` | enabled | Auto-cleanup switch for cloud conversations (set to `0` / `false` / `no` / `off` to disable) |
| `M365_AUTO_CLEANUP_INTERVAL_MINUTES` | `30` | Scan interval (minutes) |
| `M365_AUTO_CLEANUP_MAX_AGE_HOURS` | `2` | Reclaimed once idle beyond this (hours) |
| `M365_AUTO_CLEANUP_KEEP_N` | `100` | Maximum number of cloud conversations to keep |

| Variable | Default | Description |
|------|--------|------|
| `M365_CLEANUP_MODE` | `after_response` | Local conversation-index cleanup mode (`after_response` / `keep_n` / `max_age`) |
| `M365_CLEANUP_KEEP_N` | `5` | Retention count for `keep_n` mode |
| `M365_CLEANUP_MAX_AGE_HOURS` | `24` | Time limit for `max_age` mode |

### Tools and reasoning

| Variable | Default | Description |
|------|--------|------|
| `M365_TOOL_PLANNING_MODE` | `router` | Tool planning mode: `router` (gateway-routed planning) / `native` (cloud-native planning) |
| `M365_MAX_TOOL_CALLS_PER_TURN` | `1` | Max parallel tool calls per turn (operations with side effects are automatically downgraded to serial) |
| `M365_MAX_TOOL_ROUNDS` | `16` | Max tool rounds per request |
| `M365_CONTEXT_WINDOW` | `128000` | Context window |
| `M365_MAX_OUTPUT_TOKENS` | `16384` | Max output tokens |
| `M365_CHAT_TIMEOUT_SECONDS` | `120` | Chat timeout (seconds) |
| `M365_IMAGE_TIMEOUT_SECONDS` | `150` | Image-processing timeout (seconds) |

### Proxy pool and authentication

| Variable | Default | Description |
|------|--------|------|
| `M365_PROXY_POOL` | empty | Proxy list (comma- or newline-separated; supports http / https / socks5) |
| `M365_PROXY_INSECURE_TLS` | — | Trust self-signed proxy certificates (`1` / `true`) |
| `M365_PROXY_HEALTH_URL` | default probe address | Target used for proxy health checks |
| `M365_BROWSER_CLIENT_ID` / `M365_BROWSER_AUTHORITY` / `M365_BROWSER_REDIRECT_URI` / `M365_BROWSER_SCOPE` | built-in | OAuth configuration for browser PKCE |
| `M365_DEVICE_CLIENT_ID` / `M365_DEVICE_AUTHORITY` / `M365_DEVICE_SCOPE` | built-in | OAuth configuration for Device Code |
| `M365_CLIENT_ID` / `M365_AUTHORITY` / `M365_REDIRECT_URI` / `M365_SCOPE` | built-in | Legacy-compatible config; used as a fallback when the flow-specific variables aren't set |

### Data files

| Variable | Description |
|------|------|
| `M365_TOKEN_CACHE` | Token cache file (falls back to the data directory if unset) |
| `M365_SESSION_CACHE` | Session-binding cache file (default `sessions.json`) |
| `M365_CONVERSATION_CACHE` | Local conversation index (default `conversations.json`) |
| `M365_API_KEYS` | API key storage file |
| `M365_USAGE_LOG` | Usage statistics log (default `{data_dir}/usage.jsonl`) |
| `M365_DEBUG_LOG` | Debug log file (request / response metadata) |

## Usage examples

### Basic chat (OpenAI format)

```bash
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.6-sol",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

### Streaming output

```bash
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.6-sol",
    "messages": [{"role": "user", "content": "1+1=?"}],
    "stream": true
  }'
```

### Explicit session (content-key reuse + incremental send)

Requests carrying the same `X-M365-Session-Id` are bound to the same cloud conversation; on a hit, the gateway only sends the newly-added history to the upstream:

```bash
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -H "X-M365-Session-Id: my-project-session" \
  -d '{"model":"gpt-5.6-sol","messages":[{"role":"user","content":"Continue our previous discussion"}]}'
```

### Multimodal image input (OpenAI format)

Clients can send images using the standard OpenAI `image_url` format; the gateway automatically uploads the image to M365's `UploadFile` endpoint and injects a file annotation into the ChatHub message (the client doesn't need to know any upstream details):

```bash
# base64 data URL
curl http://127.0.0.1:4141/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-5.6-sol",
    "messages": [{
      "role": "user",
      "content": [
        {"type": "text", "text": "What color is this image?"},
        {"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB..."}}
      ]
    }]
  }'
```

You can also pass an https image URL directly (public addresses only, with SSRF protection; use a data URL for local images). The Responses protocol's `input_image` / `input_file` are supported the same way.

### Anthropic format (Claude Code / Cursor)

```bash
curl http://127.0.0.1:4141/v1/messages \
  -H "x-api-key: YOUR_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.6-sol","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}'
```

Reasoning content returned upstream (ChainOfThought) is mapped to Anthropic `thinking` blocks, and displays and works normally in Claude Code.

## Connecting to Claude Code

Point the gateway to in the `env` section of `~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:4141",
    "ANTHROPIC_MODEL": "gpt-5.6-sol",
    "ANTHROPIC_API_KEY": "m365_your_key"
  }
}
```

The same applies to any other client that supports an OpenAI / Anthropic `base_url` setting (OpenCode, Cursor, Codex, etc.) — just point `BASE_URL` at the gateway.

> The author does not provide adaptation or troubleshooting support for compatibility with any third-party agent framework. Adapt it yourself if needed.

The "Use API Key" dialog on the console's "API Keys" page can directly generate a Claude Code `settings.json` config and terminal environment variables — just copy them.

> ⚠️ **Auth-conflict note**: If a leftover `ANTHROPIC_API_KEY` remains in your system environment variables, or `ANTHROPIC_AUTH_TOKEN` is also configured, Claude Code will warn that "authentication may not work." Pick one: let the `env` in `settings.json` override the system-level variable, or remove the system-level `ANTHROPIC_*` variables.

## Available models

The gateway ships with a default model mapping (you can add/remove entries and adjust the default reasoning level on the console's "Settings" page):

| Model | Default reasoning level | Notes |
|------|-------------|------|
| `gpt-5.6-reasoning` | `medium` | Reasoning model |
| `gpt-5.5` / `gpt-5.4` / `gpt-5.3` / `gpt-5.2` | `medium` | Fast models (`reasoning_effort` can switch levels) |
| `flux-3` / `flux-4` | - | Image-generation models (go through the real `/v1/images` image pipeline) |

- Model mapping translates public model names into the upstream tone; the console lets you add/remove mappings and adjust the default reasoning level.
- Reasoning intensity is adjusted via the OpenAI-compatible `reasoning_effort` parameter (`none/minimal/low/medium/high/xhigh`); fake model names are no longer used to distinguish levels.
- New model names that may go live under M365 subscriptions (e.g., `gpt-5.2`, `gpt-5.4`, the `codex` family) are subject to the actual catalog, and can be imported via console configuration.

## How context-key session reuse works

In multi-account scenarios, the gateway uses a "content key" (context key) to reuse requests against an existing cloud conversation, using a mechanism modeled on DeepSeek-style context caching: **a single conversation context maintains only one cloud session, and on a hit only the incremental new message is sent upstream** — this not only avoids the overhead of rebuilding context, but also better matches the experience of multi-turn tool use. The core implementation lives in `internal/web/session_resolver.go`.

When a client request arrives, `.Resolve()` decides which session to reuse based on the following priority:

1. **Explicit session (`X-M365-Session-Id`)**: A session ID explicitly given in the request header has the highest priority and doesn't participate in any identity determination — the caller actively decides which cloud conversation to connect to.
2. **Content-key prefix hit**: When the request's message sequence **exactly matches** the history of a recorded session (computed as a content fingerprint over the last 3 messages), that session and its cloud conversation are reused directly. The `HistoryLen` returned in this case represents "the number of messages the cloud conversation already contains," and the layer above uses it to send only the incremental `messages[HistoryLen:]`.
3. **Similarity fallback**: If the messages aren't a strict prefix, but the similarity to the last message of some recently active session (within the `M365_CONTEXT_TTL_MINUTES` window) exceeds a threshold (`M365_CONTEXT_SIMILARITY`, default 0.6), that session is still reused (in this case the incremental boundary is unknown, so the full history is sent).
4. **Fallback: create new**: If nothing matches, a new session is created bound to a suitable account using the `user` field / IP+UA fingerprint or round-robin logic.

A few properties follow from this:

- **Cross-IP / cross-account reuse**: The content fingerprint acts as a globally unique key and doesn't care who the caller is — switch machines, switch M365 accounts, and as long as the conversation context matches, you can pick up the same cloud conversation.
- **Only incremental sends**: On a strict-prefix hit, the layer above only sends the new messages — effectively treating the cloud conversation as a context cache.
- **Thread and cleanup coordination**: Session bindings are persisted in `sessions.json` (mode `0600`); expiry is controlled by `M365_SESSION_TTL_MINUTES`; sessions with no hits for a long time are reclaimed by auto-cleanup on the same window (default 2 hours).

## Automatic content cleanup

Cloud conversations are treated as "cache entries": **a session hit = refresh the lifetime; idle = expired**. A background loop reclaims them every 30 minutes by default:

- Cloud conversations idle beyond `M365_AUTO_CLEANUP_MAX_AGE_HOURS` (default 2 hours);
- or the oldest conversations exceeding the count cap `M365_AUTO_CLEANUP_KEEP_N` (default 100).

**The following conversations are never reclaimed**: whitelisted conversations, conversations currently referenced by an active session binding, and recently used user sessions. Deleting a cloud conversation cleans up the local index and session binding together, preventing "ghost" conversations and avoiding a subsequent request reusing a deleted conversation and causing cross-talk or errors. See `internal/web/auto_cleanup.go` for details.

## API endpoint reference

### Public-compatible endpoints (`/v1/*`)

| Endpoint | Method | Description |
|------|------|------|
| `/v1/models` | GET | Model catalog |
| `/v1/chat/completions` | POST | Chat completion (streaming / tool calls) |
| `/v1/responses` | POST | OpenAI Responses protocol |
| `/v1/messages` | POST | Anthropic Messages (requires `x-api-key` + `anthropic-version`) |
| `/v1/images/generations` | POST | Image generation |
| `/v1/sessions` | GET / POST | Query session bindings / query or create by `session_id` |
| `/v1/sessions/{id}` | DELETE | Unbind a session |

### Admin API (`/api/*`, requires an admin login session)

| Endpoint | Description |
|------|------|
| `/api/admin/login` · `/logout` · `/session` | Admin login session |
| `/api/admin/change-password` | Change admin password (mandatory on first login) |
| `/api/admin/keys` | API key management (create / revoke / read back) |
| `/api/admin/models` · `/models/test` | Model catalog / single-model connectivity test (no plaintext key needed) |
| `/api/admin/settings` | View and modify runtime settings |
| `/api/admin/proxy-pool` | Proxy pool management |
| `/api/accounts` · `/refresh` · `/delete` | Account management |
| `/api/auth/start` · `status` · `callback` | PKCE authorization flow |
| `/api/conversations` · `/api/m365/conversations` | Local / cloud conversation list, delete, cleanup, whitelist |
| `/api/stats` · `/stats/reset` | Cache-hit statistics |
| `/api/usage` · `/usage/logs` | Usage-statistics dashboard and details |
| `/api/chat` · `/chat/stream` | In-console instant chat |
| `/api/health` · `/api/version` | Health check / version |

## Tests

The repository ships with a full unit-test suite (session resolution, auto cleanup, tool routing, protocol compatibility, usage statistics, etc.). Run it with:

```bash
go test ./...
```

For example, it verifies: the default auto-cleanup idle window is 2 hours (`internal/web/auto_cleanup_test.go`), content-key prefix hits only send the increment (`session_resolver_test.go`), and the Responses / Anthropic protocol event sequences, among others.

## Directory structure

```
M365-Copilot2API/
├── cmd/server/            # Entry point, HTTP server startup
├── internal/
│   ├── web/               # HTTP routing, session resolver, auto cleanup, admin API, usage stats
│   │   ├── session_resolver.go   # Content-key session reuse (four-way fingerprint)
│   │   ├── auto_cleanup.go        # Automatic cloud-conversation cleanup
│   │   ├── usage.go               # usage.jsonl usage statistics
│   │   └── ...                    # Tool calls, protocol conversion, proxy pool, key management, etc.
│   ├── chathub/           # M365 Copilot ChatHub WebSocket client
│   ├── auth/              # OAuth / PKCE
│   ├── mcp/               # MCP tool gateway (SSE / JSON-RPC)
│   └── outbound/          # HTTP proxy pool
├── web/                   # Admin console (plain HTML / JS single-page app)
├── scripts/               # Operational scripts
│   ├── e2e_test.py        # End-to-end tests
│   ├── chathub_probe.py   # ChatHub protocol probe
│   ├── genprobe.py        # Image-generation protocol probe (raw frame dump)
│   ├── multimodal_probe.py # Multimodal image-input probe (upload + annotation flow)
│   ├── test-recorder.ps1  # Windows test recorder
│   └── m365-upload-forensic-trace.user.js  # Upload forensic-trace script
├── docs/screenshots/      # UI screenshots
├── manage.py              # start / stop / status / logs / err process management
├── docker-compose.yml · Dockerfile
└── data/                  # Runtime data (set by M365_DATA_DIR)
```

## Security notes

- **Listens on the internal network only by default**: running the binary directly defaults to `M365_LISTEN=127.0.0.1:4141`; for external service, always terminate TLS at a reverse proxy (Nginx / Caddy), and enable long-lived connections with `proxy_buffering off` for SSE and WebSocket.
- **Password change mandatory on first login**: after completing first login with the default or bootstrap password, you must change the admin password.
- **Minimal key exposure**: an API key can be read back from the console right after creation, so protect access to the console carefully.
- **File permissions on disk**: data files such as account credentials, token cache, session bindings, and API keys are written with `0600` permissions; `0700` is recommended for the data directory. Back up the data directory regularly.

## FAQ

**Q1: Why do cloud conversations keep growing?**

A background job automatically cleans up every 30 minutes: it reclaims cloud conversations idle for more than 2 hours (`M365_AUTO_CLEANUP_MAX_AGE_HOURS`, default 2) or exceeding the count cap (`M365_AUTO_CLEANUP_KEEP_N`, default 100); conversations referenced by an active session or on the whitelist are never reclaimed. Lower these two values for more aggressive cleanup; disable it entirely with `M365_AUTO_CLEANUP=0` (not recommended — cloud conversations will grow unbounded, which may trigger risk controls).

**Q2: How do I switch M365 accounts?**

You don't need to. In multi-account scenarios, the gateway automatically round-robins across all available accounts, and fails over to the next one on a single-account failure. To add an account, just start a new PKCE authorization on the console.

**Q3: Claude Code says "authentication may not work" — what do I do?**

This is usually because a leftover `ANTHROPIC_API_KEY` remains in the system environment variables, or `ANTHROPIC_AUTH_TOKEN` is also configured, causing the two auth methods to conflict. Keep only `ANTHROPIC_API_KEY` in `~/.claude/settings.json` (settings override system-level variables), and remove the leftover system-level variable or `AUTH_TOKEN`.

**Q4: What is X-M365-Session-Id?**

By default the gateway automatically reuses sessions based on content (context prefix / similarity); when you want to explicitly control the mapping between a client session and a cloud conversation, attach the `X-M365-Session-Id` request header, and the gateway binds directly to that ID (the local content fingerprint no longer participates in the priority decision).

**Q5: Conversations show cross-talk / mixed-up context?**

Session bindings are cleared automatically once they expire. If the local cache and the cloud are out of sync, you can manually delete the cloud conversation on the console's "Conversations" page; the gateway will clean up and rebuild the local binding along with it.

## Contributing

PRs Welcome! Before submitting, please note:

1. Fork the repository and create a dedicated branch; keep one PR focused on one issue.
2. Never commit credentials, cookies, account caches, logs, or build artifacts.
3. Run `gofmt -w` before changing Go files, and run `go test ./...`, `go vet ./...`, and `go build ./...` before submitting.
4. Describe the behavior change, and include corresponding tests for new logic.

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

[AGPL-3.0 with Non-Commercial API Relay Restriction](LICENSE).

**This project may not be used as a paid API relay service.** Do not use this project for any form of commercial API resale, paid proxying, metered billing services, etc. If you have serious production needs, subscribe directly to [Azure OpenAI](https://azure.microsoft.com/en-us/products/ai-services/openai-service) — that's the proper route.

This restriction exists purely to keep the project alive. Commercial resale would easily invite legal risk and get the project taken down. I don't want to see this project die — please understand and respect it.
