---
applyTo: "**"
---

# Review guidelines

This is the Agent Receipts dashboard — a read-only web UI for viewing receipt SQLite databases. Single Go binary, no external runtime dependencies.

## Security

- Flag any real private keys, secrets, or credentials in the diff.
- Ed25519 is the only supported signing algorithm. Flag any introduction of alternative or weaker schemes.
- SHA-256 is the only supported hashing algorithm. Flag any introduction of alternative or weaker schemes.
- Flag any changes to `.github/workflows/` — these require explicit maintainer review.

## Read-only constraint

- The dashboard must NEVER write to receipt SQLite databases. Flag any code that opens a database in write mode or executes INSERT, UPDATE, or DELETE statements.
- Database connections should use read-only mode flags (e.g., `?mode=ro` or `SQLITE_OPEN_READONLY`).

## Pure Go SQLite

- SQLite access is via modernc.org/sqlite — a pure Go driver with no CGO or C dependencies. Flag any introduction of CGO-based SQLite drivers.

## Embedded assets

- All web assets (HTML, CSS, JS) are embedded via `//go:embed`. The binary must be self-contained. Flag any runtime file reads for serving static assets.

## SDK usage

- When the ar SDK already provides a capability (verification, hashing, taxonomy), use it — do not reimplement. Flag duplicated logic that the SDK already covers.

## Code quality

- Flag unused code, dead imports, and breadcrumb comments ("moved to X", "removed").
- Prefer first-principles fixes over bandaids.
- Flag any `TODO` or `FIXME` comments that don't reference an issue number.
- Errors must be wrapped with context (`fmt.Errorf("operation: %w", err)`). Flag bare error returns.
- Tests sit alongside source files as `*_test.go`. Flag test files in separate directories.
- Run `go vet ./...` before committing.
