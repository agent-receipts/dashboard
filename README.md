<div align="center">

# Agent Receipts Dashboard

Lightweight local web UI for browsing [Agent Receipts](https://github.com/agent-receipts/ar) audit trails. Single Go binary; UI loads htmx and Tailwind from CDNs.

[![CI](https://github.com/agent-receipts/dashboard/actions/workflows/ci.yml/badge.svg)](https://github.com/agent-receipts/dashboard/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/agent-receipts/dashboard)](https://github.com/agent-receipts/dashboard/releases/latest)
[![Go](https://img.shields.io/badge/go-1.26%2B-blue)](https://go.dev)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue)](LICENSE)

</div>

![Dashboard overview showing stats, risk distribution, and recent receipts](docs/screenshot.png)

## What is this?

[Agent Receipts](https://github.com/agent-receipts/ar) is a protocol for cryptographically signed, tamper-evident audit trails produced by AI agents. Every action an agent takes — file writes, API calls, shell commands — is recorded as a receipt: signed, chained, and verifiable.

The dashboard is a read-only local viewer for those receipt databases. Point it at any SQLite database written by an Agent Receipts SDK (Go, TypeScript, Python) or MCP proxy, then browse, filter, and verify your agent's activity in your browser.

## Quick start

**Homebrew** (macOS / Linux):

```sh
brew install agent-receipts/tap/dashboard
dashboard
```

**Pre-built binary** — download from [Releases](https://github.com/agent-receipts/dashboard/releases/latest), make it executable, then run:

```sh
./dashboard
```

**Go install:**

```sh
go install github.com/agent-receipts/dashboard/cmd/dashboard@latest
dashboard
```

**Build from source:**

```sh
git clone https://github.com/agent-receipts/dashboard.git
cd dashboard
make build
./dashboard
```

Opens http://localhost:8080 and reads `~/.local/share/agent-receipts/receipts.db` by default — the same path used by the SDKs and MCP proxy.

## CLI reference

| Flag | Default | Description |
|------|---------|-------------|
| `-db` | `~/.local/share/agent-receipts/receipts.db` | Path to receipts SQLite database |
| `-port` | `8080` | HTTP server port |
| `-host` | `127.0.0.1` | Address to bind (use `0.0.0.0` for all interfaces) |

```sh
# Reads ~/.local/share/agent-receipts/receipts.db by default
dashboard

# Custom database
dashboard -db ./my-receipts.db

# Custom port and bind address
dashboard -host 0.0.0.0 -port 9090
```

## Features

- **Read-only** — opens the SQLite database in read-only mode and never modifies your data
- **Universal** — reads databases produced by any Agent Receipts SDK (Go, TypeScript, Python) or MCP proxy
- **Filter** — narrow receipts by action type, risk level, status, time range, and chain ID
- **Chain verification** — validates hash linkage and sequence ordering for any chain
- **Detail view** — inspect any receipt with its full raw JSON payload
- **Dark theme** — risk-level color coding for at-a-glance triage

## Project structure

| Path | Description |
|------|-------------|
| `cmd/dashboard/` | CLI entry point and flag parsing |
| `internal/server/` | HTTP server, routes, and handlers |
| `internal/server/static/` | Embedded HTML with htmx + Tailwind (no build step) |
| `internal/store/` | Read-only SQLite access, queries, and filters |
| `internal/verify/` | Hash linkage and chain verification |

## Development

**Requirements:** Go 1.26+ (no CGO — SQLite driver is pure Go).

| Command | Description |
|---------|-------------|
| `make build` | Build the binary |
| `make test` | Run all tests |
| `make lint` | Run `go vet` |
| `make run` | Build and run (default database) |
| `make run DB=path/to/receipts.db` | Build and run against a specific database |

Or with the Go toolchain directly:

```sh
go build ./cmd/dashboard   # build
go test ./...              # test
go vet ./...               # lint
```

## Ecosystem

| Project | Description |
|---------|-------------|
| [ar](https://github.com/agent-receipts/ar) | Agent Receipts Go SDK — receipt types, signing, and verification |
| [mcp-proxy](https://github.com/agent-receipts/ar/tree/main/mcp-proxy) | MCP proxy — records agent activity as receipts transparently |
| [openclaw](https://github.com/agent-receipts/openclaw) | Open-source autonomous personal AI agent |
| [spec](https://github.com/agent-receipts/ar/tree/main/spec) | Agent Receipts protocol specification |

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).

For security vulnerabilities, use [GitHub Security Advisories](https://github.com/agent-receipts/dashboard/security/advisories/new) rather than public issues. See [SECURITY.md](SECURITY.md).

## License

Apache 2.0 — see [LICENSE](LICENSE).
