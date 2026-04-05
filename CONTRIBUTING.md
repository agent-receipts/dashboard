# Contributing to the Agent Receipts Dashboard

Contributions are welcome! This project is part of the [Agent Receipts](https://github.com/agent-receipts) ecosystem.

## Reporting issues

Open a GitHub issue for:

- Bugs in receipt display, filtering, or chain verification
- Missing or incorrect data rendering
- UI/UX improvements
- Documentation gaps

> **Security vulnerabilities**: please report via [GitHub Security Advisories](https://github.com/agent-receipts/dashboard/security/advisories/new), not public issues. See [SECURITY.md](SECURITY.md).

## Development setup

```bash
git clone https://github.com/agent-receipts/dashboard.git
cd dashboard
go build ./cmd/dashboard
go test ./...
```

**Requirements:** Go 1.26+ (no CGO needed — SQLite driver is pure Go).

### Commands

| Command | Description |
|---------|-------------|
| `go build ./cmd/dashboard` | Build the binary |
| `go test ./... -count=1` | Run all tests (no cache) |
| `go vet ./...` | Static analysis |
| `go run ./cmd/dashboard --db path/to/receipts.db` | Run locally |

Or via Make:

| Command | Description |
|---------|-------------|
| `make build` | Build the binary |
| `make test` | Run tests |
| `make lint` | Run `go vet` |
| `make run DB=path/to/receipts.db` | Build and run |

## Development process

1. Fork the repo and create a branch from `main`
2. Write tests before implementation — the test suite is the source of truth
3. Run `go vet ./...` and `go test ./...` before committing
4. Open a pull request

## Code conventions

- Go standard library preferred — minimize external dependencies
- Pure Go SQLite (`modernc.org/sqlite`) — no CGO
- All web assets embedded via `//go:embed` — the binary is self-contained
- Test files colocated as `*_test.go` alongside source
- The dashboard is **read-only** — it must never write to receipt SQLite databases

## Commit messages

This project follows [Conventional Commits](https://www.conventionalcommits.org/). Every commit message must start with a type:

```
feat: add new feature
fix: correct a bug
docs: update documentation
chore: maintenance task
refactor: restructure without behavior change
test: add or update tests
ci: change CI/CD configuration
```

The `commit-msg` hook enforces this via [convco](https://convco.github.io/). Install hooks with:

```bash
brew install lefthook convco
lefthook install
```

## Project structure

```
cmd/dashboard/          CLI entry point
internal/server/        HTTP server, routes, handlers
internal/server/static/ Embedded HTML with htmx + Tailwind (no build step)
internal/store/         Read-only SQLite access, queries, filters
internal/verify/        Hash linkage and chain verification
```

## Good first contributions

- **Add ByAction stats** (#4) — small backend change to include action type breakdown in `/api/stats`
- **Align default limit** (#11) — change a single constant to match the SDK default
- **Improve the README** (#15) — add badges, screenshots, ecosystem links
- **Improve error messages** — clearer messages help everyone debug faster
- **Add test cases** — especially edge cases for filters and chain verification

## Working with AI agents

AI agents (Claude Code, GitHub Copilot, etc.) are first-class contributors to this project. See [AGENTS.md](AGENTS.md) for the full agent safety rules and conventions.

**Test-driven workflow** — the highest-leverage pattern for agent-assisted development:

1. Write a failing test that describes the expected behavior.
2. Let the agent implement the fix or feature to make the test pass.
3. The test output gives the agent a tight feedback loop — it can iterate without guessing.

**Agent boundaries** — agents must follow the [Agent safety rules](AGENTS.md#agent-safety-rules). Key constraints: no CI workflow changes without human approval, no real cryptographic keys, the dashboard must remain read-only.

## SDK integration

This dashboard depends on the [Agent Receipts Go SDK](https://github.com/agent-receipts/ar/tree/main/sdk/go). When the SDK already provides a capability (verification, hashing, taxonomy), use it — do not reimplement. See issues labeled `sdk-alignment` for current integration work.

## Pre-submit checklist

Before opening a PR, verify:

- [ ] `go test ./...` passes
- [ ] `go vet ./...` passes
- [ ] No real keys or secrets in the diff — use test fixtures only
- [ ] New functionality includes tests
- [ ] AGENTS.md updated if you changed project structure
- [ ] Commit message follows [Conventional Commits](https://www.conventionalcommits.org/) format

## License

Apache 2.0
