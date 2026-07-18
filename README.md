# m365-native

M365 ChatHub gateway for **authorized Microsoft 365 Copilot sessions**. It exposes OpenAI-compatible and Anthropic-compatible HTTP APIs for chat, streaming, multimodal input, tool calls, session continuity, and upstream image-event parsing.

> This project is an interoperability gateway, not an authentication bypass. You must use a Microsoft account and tenant you are authorized to use. Upstream model availability, quotas, tools, vision, and image generation depend on the account and Microsoft service.

## Reference repositories

- **M365-Copilot2API:** <https://github.com/HEXUXIU/M365-Copilot2API>
- **Microsoft 365 Copilot:** <https://www.microsoft.com/microsoft-365/copilot>

## Features

- OpenAI-compatible `/v1/chat/completions`
- OpenAI Responses-compatible `/v1/responses`
- Anthropic-compatible `/v1/messages`
- Streaming responses and multimodal input
- Gateway-level tool/function calling with protocol conversion
- Persistent conversation mapping through `session_key`
- Model catalog with GPT-5.5, GPT-5.5 reasoning, GPT-5.6 reasoning, and Claude Sonnet routes when available upstream
- Upstream image-event/GraphicArt parsing when enabled for the account
- Web console for account, API-key, settings, conversations, and debug management

## Requirements

- Go 1.22+ for source builds, or Docker/Compose
- An authorized Microsoft account and tenant
- OAuth access obtained through the bundled PKCE flow or an existing account cache

## Quick start: source build

```bash
git clone https://github.com/uefi2333/m365-native.git
cd m365-native
cp .env.example .env
# Edit .env. Never commit real passwords or tokens.
set -a; . ./.env; set +a
go test ./...
go vet ./...
go run ./cmd/server
```

The default bind address is `127.0.0.1:4141`. Open <http://127.0.0.1:4141/> and complete administrator setup/login. Keep the service on localhost unless you add TLS and an access-control layer.

Build a standalone binary:

```bash
go build -trimpath -o m365-native ./cmd/server
./m365-native
```

## Docker deployment (recommended)

Docker is the recommended deployment method for a reproducible runtime. The image runs as a non-root user and stores mutable credentials/state under `/data`.

### 1. Prepare directories and admin secret

```bash
mkdir -p data secrets
printf '%s\n' 'replace-with-a-long-random-admin-password' > secrets/m365_admin_password
chmod 600 secrets/m365_admin_password
```

Do not commit `data/` or `secrets/`. The provided `.gitignore` excludes them.

### 2. Build and start

```bash
docker compose build
docker compose up -d

docker compose ps
docker compose logs -f m365-native
```

The default Compose mapping is local-only:

```text
127.0.0.1:4141 -> container:4141
```

For a reverse proxy or LAN deployment, change the `ports` mapping deliberately and put TLS/authentication in front of it.

### 3. Persistent data

The Compose file mounts:

```text
./data/accounts.json       OAuth account cache
./data/token-cache.json    token cache
./data/sessions.json       session_key mapping
./data/api-keys.json       API-key hashes
./secrets/m365_admin_password administrator password secret
```

Back up these files securely. `accounts.json` and token caches are credentials. Never paste them into issues, logs, screenshots, or public repositories.

### 4. First login and API key

Open:

```text
http://127.0.0.1:4141/
```

Log in to the web console, complete the Microsoft authorization flow, and create an API key from the administration interface. Use that key with `/v1`:

```bash
curl http://127.0.0.1:4141/v1/models \
  -H 'Authorization: Bearer YOUR_M365_NATIVE_API_KEY'
```

The gateway accepts either `Authorization: Bearer ...` or `X-API-Key: ...`.

## API examples

OpenAI Chat Completions:

```bash
curl http://127.0.0.1:4141/v1/chat/completions \
  -H 'Authorization: Bearer YOUR_M365_NATIVE_API_KEY' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "gpt-5.6-reasoning",
    "messages": [{"role": "user", "content": "Hello"}],
    "stream": true,
    "session_key": "my-conversation-1"
  }'
```

Keep `session_key` stable for every turn of the same conversation. Use a different key for a different conversation. The gateway stores the corresponding upstream `ConversationID` and `SessionID` in the session cache.

Anthropic-compatible endpoint:

```bash
curl http://127.0.0.1:4141/v1/messages \
  -H 'x-api-key: YOUR_M365_NATIVE_API_KEY' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "claude-sonnet",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

## Model routing

The public model IDs are gateway aliases. The current stable catalog is:

| Public model | Upstream tone |
|---|---|
| `gpt-5.5` | `Gpt_5_5_Chat` |
| `gpt-5.5-reasoning` | `Gpt_5_5_Reasoning` |
| `gpt-5.6-reasoning` | `Gpt_5_6_Reasoning` |
| `claude-sonnet` | `Claude_Sonnet` |
| `claude-sonnet-reasoning` | `Claude_Sonnet_Reasoning` |

Availability and latency remain controlled by Microsoft 365 ChatHub and the account entitlement.

## Configuration

Common environment variables:

| Variable | Default | Purpose |
|---|---|---|
| `M365_LISTEN` | `127.0.0.1:4141` | HTTP bind address |
| `M365_CONFIG` | `~/.config/m365-native/accounts.json` | OAuth account cache |
| `M365_ADMIN_PASSWORD` | bootstrap default only | Admin password; prefer a secret file |
| `M365_ADMIN_PASSWORD_FILE` | unset | File containing admin password |
| `M365_TOKEN_CACHE` | platform default | Token cache path |
| `M365_SESSION_CACHE` | temp directory | Persistent `session_key` mapping |
| `M365_API_KEYS` | `~/.config/m365-native/api-keys.json` | API-key hash store |
| `M365_CHAT_TIMEOUT_SECONDS` | `120` | Chat timeout |
| `M365_IMAGE_TIMEOUT_SECONDS` | `150` | Image request timeout |
| `M365_MAX_TOOL_ROUNDS` | `16` | Maximum tool rounds |
| `M365_MAX_TOOL_CALLS_PER_TURN` | `1` | Tool-call limit per turn |
| `M365_CONTEXT_WINDOW` | `128000` | Advertised context window |
| `M365_MAX_OUTPUT_TOKENS` | `16384` | Advertised output limit |

## Development and verification

```bash
gofmt -w cmd internal
go test ./...
go vet ./...
go build ./...
```

## Security notes

- Bind to localhost by default.
- Change the administrator password immediately.
- Keep OAuth caches, token files, API-key files, and Docker secrets private.
- Use TLS and an additional access-control layer before exposing the service outside localhost.
- Do not log or publish access tokens, cookies, authorization headers, or raw authenticated WebSocket URLs.
- This gateway only supports accounts and services you are authorized to access.

## License

MIT. See [LICENSE](LICENSE).
