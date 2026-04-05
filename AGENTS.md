# AGENTS.md

Lightweight local dashboard for viewing Agent Receipts — cryptographically signed audit trails produced by the Agent Receipts SDKs and MCP proxy. Reads directly from existing SQLite receipt stores. Single Go binary, no external dependencies at runtime.

## Project layout

```
cmd/dashboard/     # CLI entry point
internal/server/   # HTTP server, routes, handlers
internal/store/    # Read-only SQLite access, queries, multi-DB
internal/verify/   # Hash linkage and chain verification
web/static/        # Embedded HTML, htmx, CSS (no build step)
```

## Quick reference

| Task | Command |
|------|---------|
| Run | `go run ./cmd/dashboard --db path/to/receipts.db` |
| Build | `go build -o dashboard ./cmd/dashboard` |
| Test | `go test ./...` |
| Lint | `go vet ./...` |

## Conventions

- All changes go through pull requests — never push directly to main
- Go standard library preferred — minimize external dependencies
- Pure Go SQLite (`modernc.org/sqlite`) — no CGO dependency
- All web assets embedded via `//go:embed` — single binary distribution
- The dashboard opens SQLite databases **read-only** — it must never write to receipt stores
- Run `go vet` and `go test ./...` before committing

## Dependencies

This project depends on the Agent Receipts Go SDK for receipt verification and taxonomy:

```
github.com/agent-receipts/ar/sdk/go   # Receipt types, hashing, chain verification, taxonomy
modernc.org/sqlite                     # Pure Go SQLite driver
```

## Security

- The dashboard is a read-only viewer — it must never modify receipt databases
- Never commit real private keys. Use test fixtures for testing.
- Ed25519 is the only supported signing algorithm.
- Validate all inputs at trust boundaries (query parameters, SQLite data, config files).
- Report vulnerabilities via [GitHub Security Advisories](https://github.com/agent-receipts/dashboard/security/advisories/new), not public issues. See [SECURITY.md](SECURITY.md).

## Mindset

- Think before acting. Understand the problem before writing code.
- Work like a craftsman — do the better fix, not the quickest fix.
- Fix from first principles, not bandaids.
- Write idiomatic, simple, maintainable code.
- Delete unused code ruthlessly. No breadcrumb comments ("moved to X", "removed").
- Leave the repo better than you found it.

## Papercut rule

- Fix small issues you notice while working (typos, dead imports, minor inconsistencies).
- Raise larger cleanups with the user before expanding scope.

## Timeout handling

- If a command runs longer than 35 minutes, stop it, capture logs/context, and check with the user.
- Do not wait indefinitely for hung processes.

## Adding dependencies

- Research before adding — prefer well-maintained, widely-used packages with good APIs.
- Avoid unmaintained dependencies (check last commit date, open issues, bus factor).
- Prefer the standard library when it covers the use case adequately.
- New dependencies require justification in the PR description.
- For Go: check pkg.go.dev for import counts and maintenance signals.
- Supply chain security matters for a cryptographic protocol project — evaluate carefully.

## Completing work

Before marking work as complete:

1. Confirm all touched tests and linters pass.
2. Re-read your full diff — check for mistakes, consistency, and completeness.
3. Summarise changes with file and line references.
4. Mention any opportunistic papercut fixes made along the way.
5. Call out TODOs, follow-up work, or uncertainties.
6. If opening a PR, verify the description accurately reflects the changes.

## Agent safety rules

When working in this repo as an AI coding agent, these rules apply in addition to the conventions above:

- **Never modify CI/CD workflows** (`.github/workflows/`) without explicit human review
- **Never weaken cryptographic parameters** — do not change key sizes, hash algorithms, or signature schemes
- **Never skip or delete existing tests** — add tests, don't remove them
- **Never generate real cryptographic keys** — always use test fixtures
- **Always run the full test suite** before proposing a PR
- **The dashboard is read-only** — never add code that writes to receipt SQLite databases
- **Use git worktrees** for new work — do not edit directly on main or shared branches
- **Write tests first** — new functions must have test coverage before pushing
- **Self-review before committing** — follow the Completing work checklist above
