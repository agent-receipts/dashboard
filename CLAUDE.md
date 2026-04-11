# dashboard

Lightweight local web UI for viewing Agent Receipt SQLite databases. Single Go binary, no external runtime dependencies.

## Toolchain

- **Language:** Go 1.22+
- **SQLite:** modernc.org/sqlite (pure Go, no CGO)
- **Frontend:** Single embedded HTML file with htmx + Tailwind CDN (no build step)
- **Testing:** `go test`

## Commands

```sh
go build ./cmd/dashboard   # build binary
go test ./...              # run all tests
go vet ./...               # static analysis
make build                 # build via Makefile
make test                  # test via Makefile
```

## Running

```sh
./dashboard -db path/to/receipts.db           # open a receipt database
./dashboard -db path/to/receipts.db -port 9090  # custom port
```

## Project structure

```
cmd/dashboard/      # CLI entry point
internal/server/    # HTTP server, routes, embedded static files
internal/store/     # Read-only SQLite reader, queries, filters
internal/verify/    # Hash linkage and sequence verification for chains
```

## Conventions

- All changes go through pull requests — never push directly to main
- The dashboard is **read-only** — it must never write to receipt SQLite databases
- Web assets are embedded via `//go:embed` — the binary is self-contained
- Tests use the SDK's `store.Open()` to create seeded test databases, then open them read-only
- Run `go vet ./...` and `go test ./...` before committing
- Tests sit alongside source files as `*_test.go`
- Write tests before implementation (TDD)
