# m365-native

A small Go gateway for authorized Microsoft 365 Copilot ChatHub sessions. It exposes
an OpenAI-compatible HTTP surface for text, streaming, multimodal requests, tools,
and image-generation intent/event parsing.

> Image generation is upstream- and account-dependent. The gateway can parse
> GraphicArt/VisualCreator events, but it cannot grant quota or bypass Microsoft
> entitlement checks.

## Requirements

- Go 1.22+
- A Microsoft account and tenant you are authorized to use
- OAuth access through the bundled PKCE flow or an existing account cache

## Run

```bash
cp .env.example .env
export M365_CONFIG="$HOME/.config/m365-native/accounts.json"
go run ./cmd/server
```

The server binds to `127.0.0.1:4141` by default. Set `M365_LISTEN` to change it. The development default is `admin123`; change it for any shared or public deployment. You can override it with `M365_ADMIN_PASSWORD` through a protected environment file or secret manager. The web console requires administrator login before management APIs can be used.
Keep the service on localhost unless you provide TLS and an additional access control layer.

## Authentication and storage

The account cache contains OAuth credentials. By default it lives at `~/.config/m365-native/accounts.json` and is created with mode `0600`; protect it like a password and revoke the account session if it is exposed. Set `M365_CONFIG` explicitly when using a different location. Never put tokens
in issues, logs, URLs, screenshots, or HAR files.

## API

The web service includes OpenAI-compatible routes under `/v1`, including chat,
streaming, multimodal input, tool calls, and `/v1/images/generations`. The latter
forwards image intent to the upstream service and returns an upstream-unavailable or
quota result when GraphicArt/VisualCreator is not enabled for the account.

## Development

```bash
gofmt -w .
go test ./...
go vet ./...
go build ./...
```

## License

MIT. See [LICENSE](LICENSE).
