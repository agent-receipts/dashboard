# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Overview "Recent receipts" now fetches only `RECENT_LIMIT` (10) rows from the server instead of the full store (up to 10,000 after #55), reducing unnecessary bandwidth and memory usage. The `/api/receipts` endpoint now honours a `?limit=N` query parameter (capped at 10,000) (#58)

### Added

- Output status mismatch detection — flags receipts where the declared output hash does not match the computed value (#52)
- Dashboard UI polish: context header showing active database, keyboard navigation between receipts, and loading skeletons (#53)
- JSON export buttons in the receipt detail and chain detail modals — download individual receipts as `receipt-{id}.json` or entire chains as `chain-{chainId}.json` (#7)

## [0.1.6] - 2026-05-19

### Added

- `parameters_disclosure` field surfaced in the receipts list and detail views (#49)

## [0.1.5] - 2026-05-18

### Added

- Live polling for new receipts — the list auto-refreshes without a page reload (#48)

## [0.1.4] - 2026-05-16

### Added

- Default `-db` path: dashboard now defaults to `~/.local/share/agent-receipts/receipts.db` when no flag is supplied (#44, #46)

## [0.1.3] - 2026-05-01

### Changed

- SDK dependency bumped to v0.6.0 (#42)

## [0.1.2] - 2026-04-24

### Added

- SERVER and TOOL columns in the receipt list and detail views (#35)

## [0.1.1] - 2026-04-24

### Added

- GoReleaser build pipeline with GitHub Actions release workflow and Homebrew formula (#33)
- Dependabot config for automated Go module and GitHub Actions dependency updates (#24)

### Changed

- SDK dependency bumped to v0.4.0 (#31)

### Security

- SHA-pinned all GitHub Actions to commit SHAs for supply chain integrity (#23)

## [0.1.0] - 2026-04-05

### Added

- Initial dashboard — read-only receipt viewer for Agent Receipts SQLite stores, built with Go and htmx
- CI workflow, PR template, and Copilot review instructions (#19)
- Conventional Commits enforcement via Lefthook and convco (#18)
- shellcheck linting in CI and Lefthook (#22)
- Go module publish workflow (#20)
- Release script (#21)
- `CONTRIBUTING.md` for new contributors (#16)
- GitHub YAML issue templates for bugs and feature requests (#17)

### Fixed

- Server now binds to `localhost` by default; `--host` flag added for custom binding

[Unreleased]: https://github.com/agent-receipts/dashboard/compare/v0.1.6...HEAD
[0.1.6]: https://github.com/agent-receipts/dashboard/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/agent-receipts/dashboard/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/agent-receipts/dashboard/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/agent-receipts/dashboard/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/agent-receipts/dashboard/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/agent-receipts/dashboard/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/agent-receipts/dashboard/releases/tag/v0.1.0
