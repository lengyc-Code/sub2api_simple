# sub2api_simple

`sub2api_simple` is a lightweight standalone gateway that exposes a unified API for multiple upstream AI accounts (Anthropic and OpenAI), with account pooling, failover, sticky sessions, and streaming support.

## Features

- Unified HTTP API for:
  - `POST /v1/messages` (Anthropic-compatible)
  - `POST /v1/responses` and `POST /responses` (OpenAI-compatible)
  - `POST /v1/chat/completions` (OpenAI Chat Completions-compatible)
  - `GET /v1/models`
  - `GET /health`
- Multi-account pool with:
  - Priority-based scheduling
  - Concurrency limits per account
  - Sticky session routing
  - Automatic failover on transient upstream errors
- OAuth and API key support:
  - OpenAI OAuth browser login flow (`/auth/login`)
  - OpenAI refresh token auto-refresh and persistence
  - Anthropic/OpenAI API key mode
- Streaming and non-streaming handling:
  - SSE passthrough for streaming requests
  - SSE aggregation for non-streaming clients when needed

## Requirements

- Go 1.21+

## Quick Start

1. Copy and edit config:

```bash
cp config.example.json config.json
```

2. Build:

- Windows (PowerShell):

```powershell
./build.ps1
```

- Linux/macOS:

```bash
bash ./build.sh
```

Default output is `output/`.

3. Run:

```bash
go run . -config config.json
```

or run the built binary:

- Windows:

```powershell
./output/sub2api_simple.exe -config config.json
```

- Linux/macOS:

```bash
./output/sub2api_simple -config config.json
```

## Authentication

Client requests must provide one of the configured `auth_tokens`:

- `Authorization: Bearer <token>`
- or `x-api-key: <token>`

## OAuth Browser Login (OpenAI)

If an OpenAI OAuth account has no available token, the gateway logs a login URL on startup.

You can also open it manually:

```text
http://localhost:8080/auth/login?account=<account-name>
```

The callback listener uses:

```text
http://localhost:1455/auth/callback
```

## Configuration

See [config.example.json](./config.example.json) for full options.

Main fields:

- `listen_addr`: bind address (for example `:8080`)
- `auth_tokens`: client-facing API tokens for this gateway
- `accounts`: upstream account definitions
- `openai_default_instructions`: default instructions by model (supports `*`)
- `model_extra_params`: default extra params by model (supports `*`)
- `enable_request_log`: enable request/response error logging
- `enable_stream_debug_log`: verbose stream line logging
- `enable_model_debug_log`: log upstream model request payloads and downstream client responses
- `max_account_switches`: maximum failover switches per request
- `sticky_session_ttl`: sticky routing TTL (duration string)
- `stream_read_timeout`: stream inactivity timeout (duration string)

### Account Modes

Each account supports:

- `platform`: `anthropic` or `openai`
- `type`: `oauth` or `api_key`

Common auth patterns:

- Anthropic/OpenAI API key:
  - set `type: "api_key"` + `api_key`
- OpenAI OAuth refresh:
  - set `type: "oauth"` + `refresh_token`
- OpenAI OAuth browser login:
  - set `type: "oauth"` without token, then use `/auth/login`

## API Endpoints

- `POST /v1/messages`
  - Anthropic-compatible message API proxy
- `POST /v1/responses`
  - OpenAI-compatible responses API proxy
- `POST /responses`
  - Alias of `/v1/responses`
- `POST /v1/chat/completions`
  - OpenAI Chat Completions-compatible proxy (request/response format conversion)
- `GET /v1/models`
  - Combined model list based on configured platforms
- `GET /health`
  - Service and account runtime status
- `GET /auth/login`
  - Start OpenAI OAuth browser flow

## Testing

### Build-only validation

```bash
go test ./... -run ^$
```

### Integration tests

`client_test.go` expects a running gateway at `http://localhost:8080`:

```bash
go test ./...
```

## Project Structure

```text
.
├── main.go
├── internal/gateway
│   ├── oauth/       # token refresh, PKCE, OAuth session
│   ├── forwarder/   # failover policy and stream helpers
│   ├── router/      # endpoint dispatch
│   ├── claude/      # Claude request shaping
│   └── openai/      # OpenAI request shaping
├── cmd/chatclient   # optional interactive client
├── build.ps1
├── build.sh
└── config.example.json
```

## License

This project is licensed under the MIT License. See [LICENSE](./LICENSE).
