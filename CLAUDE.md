# cc-connect

A universal bridge connecting AI coding agents to messaging platforms.

## Build & Test

```bash
make build        # Build binary to dist/
make run          # Build and run
make test         # Run all tests
make lint         # golangci-lint
make release-all  # Cross-platform builds (linux/darwin/windows, amd64/arm64)
```

Build with version injection (required to prevent npm wrapper overwrite):
```bash
go build -ldflags "-X main.version=1.2.1 -X main.commit=custom -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o /tmp/cc-connect-new ./cmd/cc-connect/
```

Install built binary:
```bash
cp /tmp/cc-connect-new ~/.npm-global/lib/node_modules/cc-connect/bin/cc-connect
```

## Architecture

Plugin-based design — platforms and agents register via `init()`:
- `core.RegisterPlatform()` / `core.RegisterAgent()`

**Message flow:** Platform receives message → Engine routes to session → Agent subprocess processes → Engine captures output → Platform delivers response

### Key Packages

| Package | Purpose |
|---|---|
| `core/engine.go` | Central router, session lifecycle, permission modes (~5200 lines) |
| `core/interfaces.go` | Platform & Agent interface contracts |
| `core/api.go` | HTTP API (Unix socket) |
| `core/cron.go` | Scheduled task management |
| `core/relay.go` | Bot-to-bot relay logic |
| `platform/*/` | 9 messaging platform adapters |
| `agent/*/` | 7 AI agent adapters |
| `cmd/cc-connect/` | CLI entrypoint & subcommands |

### Supported Platforms (9)
Feishu, DingTalk, Telegram, Slack, Discord, LINE, WeChat Work, QQ (NapCat), QQ Bot Official

### Supported Agents (7)
Claude Code, Codex, Cursor, Gemini CLI, Qoder, OpenCode, iFlow

## Config

Default config location: `~/.cc-connect/config.toml`
See `config.example.toml` for full reference.

## Daemon

```bash
cc-connect daemon install   # Install as systemd/launchd service
cc-connect daemon start
cc-connect daemon stop
```

API socket: `~/.cc-connect/run/api.sock`

## Adding a New Platform or Agent

1. Create a new package under `platform/` or `agent/`
2. Implement the `core.Platform` or `core.Agent` interface
3. Register in `init()` via `core.RegisterPlatform()` / `core.RegisterAgent()`
4. Add a blank import in `cmd/cc-connect/main.go`
