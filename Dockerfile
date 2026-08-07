FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/m365-copilot2api ./cmd/server

FROM alpine:3.20 AS compressor
RUN apk add --no-cache upx && mkdir -p /out
COPY --from=build /out/m365-copilot2api /tmp/m365-copilot2api
RUN upx --lzma --best /tmp/m365-copilot2api && cp /tmp/m365-copilot2api /out/m365-copilot2api

FROM alpine:3.20
RUN addgroup -S m365 && adduser -S -G m365 m365 \
    && mkdir -p /data /app
WORKDIR /app
COPY --from=compressor /out/m365-copilot2api /app/m365-copilot2api
COPY --from=build /src/web /app/web
RUN chown -R m365:m365 /app /data
USER m365
EXPOSE 4141
ENV M365_LISTEN=0.0.0.0:4141 \
    M365_DATA_DIR=/data \
    M365_CONFIG=/data/accounts.json \
    M365_TOKEN_CACHE=/data/token-cache.json \
    M365_SESSION_CACHE=/data/sessions.json \
    M365_API_KEYS=/data/api-keys.json \
    M365_ADMIN_PASSWORD_FILE=/data/admin-password \
    M365_ADMIN_PASSWORD_BOOTSTRAP_FILE=/run/secrets/m365_admin_password
VOLUME ["/data"]
ENTRYPOINT ["/app/m365-copilot2api"]
