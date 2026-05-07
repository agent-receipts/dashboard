# Agent Receipts Dashboard

Lightweight local web UI for browsing [Agent Receipt](https://github.com/agent-receipts/ar) SQLite databases. Single Go binary, no backend dependencies. The UI loads htmx and Tailwind CSS from CDNs.

![Dashboard overview showing stats, risk distribution, and recent receipts](docs/screenshot.png)

## Features

- Read-only viewer for receipt databases produced by any Agent Receipts SDK (Go, TypeScript, Python) or MCP proxy
- Filter receipts by action type, risk level, status, time range, and chain ID
- Chain verification — validates hash linkage and sequence ordering
- Receipt detail view with raw JSON
- Dark theme with risk-level color coding

## Install

Homebrew (macOS / Linux):

```sh
brew install agent-receipts/tap/dashboard
```

Pre-built binaries for darwin/linux (amd64, arm64) are attached to each [GitHub release](https://github.com/agent-receipts/dashboard/releases).

With a Go toolchain:

```sh
go install github.com/agent-receipts/dashboard/cmd/dashboard@latest
```

Or build from source:

```sh
git clone https://github.com/agent-receipts/dashboard.git
cd dashboard
make build
```

## Usage

```sh
# Reads ~/.agent-receipts/receipts.db by default (matches mcp-proxy / SDK convention)
dashboard

# Point at a different receipt database
dashboard -db ./receipts.db

# Custom port
dashboard -port 9090
```

Then open http://localhost:8080 in your browser.

## How it works

The dashboard opens your SQLite receipt database in **read-only** mode and serves a web UI at localhost. It never modifies your data.

All three Agent Receipts SDKs and the MCP proxy use an identical SQLite schema for receipt storage. The dashboard reads from any of them.

## Development

```sh
go test ./...    # run tests
go vet ./...     # lint
make run DB=./test.db  # build and run
```

## License

Apache 2.0 — see [LICENSE](LICENSE).
