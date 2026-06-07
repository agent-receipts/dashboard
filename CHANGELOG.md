# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Activity timeline chart on the Overview tab** — a new collapsible "Activity timeline" card sits between the stats summary cards and the distribution grid. It renders an inline SVG stacked bar chart of receipt counts bucketed over the active time range: a green (success) segment at the bottom, red (failure) above it, and a muted grey segment for any remaining statuses (pending, other). Y-axis scales to the maximum bucket total; bars share the full panel width with small gaps. X-axis shows 4–6 evenly spaced tick labels, formatted as `HH:MM` for intraday ranges and `Mon D` for multi-day ranges. Each bar carries a native SVG `<title>` tooltip with the bucket timestamp, success count, failure count, and total. An empty-state message is shown when all buckets are zero. The chart updates automatically whenever the range picker changes (since `setOverviewRange()` re-calls `loadOverview()`), and its collapsed state is remembered in `localStorage` like other Overview cards.

- **Time range picker on the Overview tab** — a persistent preset bar (`1h · 6h · 24h · 7d · 30d · All`, default `24h`) at the top of the Overview drives every panel on that tab. Clicking a preset immediately re-renders all cards — stat totals, risk/status/action distributions, top actions by failure rate, server activity, and recent receipts — to reflect only receipts in the selected window. The active preset is stored in the URL hash (`#range=<preset>`) so it survives a page reload and can be bookmarked. A reusable `overviewRangeParams()` accessor returns `{ preset, after }` for consumption by future activity-timeline (#84) and error-rate (#85) panels.
- **`GET /api/stats/timeseries`** — a new endpoint returns a continuous time-bucketed series of receipt counts broken down by `status` and `risk_level`. Accepts `range=<Go duration>` (relative window ending now) or `from=<ISO>&to=<ISO>` (absolute); optional `bucket=<Go duration>` (auto-selected from the window when absent: ≤2h → 5m, ≤24h → 1h, ≤7d → 6h, else → 1d). Empty buckets are always included so callers get a continuous series. Responds with `{ "buckets": [...], "bucket_duration": "1h", "range_from": "...", "range_to": "..." }`. Pathological range/bucket combinations that would exceed 2 000 buckets return 400.
- **`GET /api/stats` now accepts `after` and `before` query parameters** — optional ISO-8601 timestamps (inclusive) that filter all aggregate counts (total, chains, by_risk, by_status, by_action, latest_timestamp) to the specified window. Omitting both preserves the existing all-time behaviour with no regression.

- **Top actions by failure rate** — a new `GET /api/stats/actions` endpoint returns per-action-type failure statistics (total, success, failure counts, and failure rate), filtered to action types with at least 5 receipts. An optional `range` query parameter (Go duration string, e.g. `24h`) restricts the window. Results are sorted by failure rate descending. A new **Actions** tab renders the full sortable table with clickable column headers and a "Show all" toggle; clicking a row pre-filters the Receipts view to that action type's failures. The Overview tab gains a new "Top actions by failure rate" summary card showing the top 5 action types as horizontal bars, with a "View all →" link to the Actions tab.
- **Free-text search on the Receipts tab** — a "Search" input field at the left of the filter bar lets you search across the full raw receipt JSON (action type, tool name, IDs, parameters, and more). The active term is reflected in the URL as `?q=<value>` on `GET /api/receipts`, making searches bookmarkable and shareable. Loading the dashboard with `?q=<term>` pre-fills the search and opens the Receipts view automatically. The match count updates to "N receipts matching '<term>'" when a search is active.
- **Server/tool breakdown panel** — a new "Servers" tab shows an expandable table of every MCP server (extracted from `credentialSubject.action.target.system`) with per-server totals, failure counts, and a mini failure-rate bar. Each server row expands to reveal its tools with the same metrics. Clicking a tool row pre-filters the Receipts view to that server and tool. Receipts with no server are folded into an "Unknown" group, which is listed after named servers. The Overview tab gains a "Server activity" summary card showing the top 5 named servers as horizontal bars. New endpoint: `GET /api/stats/servers` (optional `?range=<duration>` for time-scoped results).
- **Server and tool filters on Receipts** — two new filter inputs (`Server` and `Tool`) on the Receipts tab allow filtering by `target.system` and `tool_name` independently or in combination, with the same chip and clear-all behaviour as existing filters.
- **Collapsible Overview cards** — each card on the Overview tab (Risk / Status / Action distribution, Top actions, Server activity, Recent receipts) has a chevron to collapse it down to its title. Collapsed state is remembered per card in `localStorage`, so cards you hide stay hidden across reloads.

### Fixed

- **Overview "Top actions by failure rate" and "Server activity" cards no longer hang on their loading skeletons.** Both summary fetches captured the polling generation counter and then ran after `startPolling()` incremented it, so their stale-generation guard always tripped and the render was skipped. Live polling and keyboard nav now start first, and the summary cards load afterwards guarded by a fresh generation captured after `startPolling()` — so the cards render correctly while a slow or failing `/api/stats/*` still can't stall recent-receipts polling.
- **Overview "Server activity" card now includes the "Unknown" (no-server) group** instead of filtering it out, so receipts with no `target.system` still show activity rather than rendering "No data". The empty state now only appears for a genuinely empty store.

## [0.5.1] - 2026-06-03

### Fixed

- **Decrypted row preview now works for arbitrary disclosure schemas** — v0.5.0 only surfaced the `input` and `output` keys in the row tooltip, so MCP tool wrappers that capture parameters under other names (e.g. `command`, `arguments`, `result`) fell back to the generic "Additional disclosure fields present" message even when the forensic key successfully decrypted the envelope. The hydrator now keeps the first two non-empty top-level keys regardless of their names, with `input`/`output` still preferred for parity with the server's plaintext preview.

## [0.5.0] - 2026-06-03

### Added

- **Forensic key: path input and auto-load** — the "Forensic decryption key" modal now accepts a file path in addition to the file picker and paste field. The path input is pre-filled with the default location (`~/.local/share/agent-receipts/forensic.key`) so most operators can load their key with a single click. Leading `~` is expanded to the user's home directory on the server. Additionally, when the dashboard starts on a loopback address and finds a key file at that default location, it loads the key automatically — no UI step required for a standard single-user install. New endpoint: `POST /api/forensic-key/path`.
- **Decrypted parameter previews on receipt rows** — when the forensic key is loaded, the hover tooltip on encrypted-disclosure rows now shows the decrypted input/output snippets inline, so the operator no longer has to click into the detail modal to read the parameters. Decrypted snippets are cached in the browser tab only and dropped whenever the forensic key state changes.

### Security

- **CSRF guard on `/api/forensic-key/path`** — the endpoint now requires `Content-Type: application/json`, forcing cross-origin browser POSTs through a CORS preflight rather than letting a hostile page issue a "simple" request that would trigger arbitrary server-side file reads. The existing `POST /api/forensic-key` accepts a raw body for compatibility and is not affected by this change; tracked in [#79](https://github.com/agent-receipts/dashboard/issues/79).

## [0.4.0] - 2026-06-03

### Added

- **Forensic disclosure decryption** — operators can now load their X25519 forensic private key into the dashboard and view decrypted `parameters_disclosure` envelopes inline in the receipt detail view. A header "Forensic key" control accepts the raw 32-byte key (as written by `agent-receipts-daemon --init-forensic-key`), or a hex / base64 / PKCS#8 PEM paste, and shows the loaded key's `sha256:` fingerprint for verification against the daemon's startup log. Receipts encrypted to that key decrypt automatically; non-matching receipts show a clear "key mismatch" state, and receipts decrypt-fail or lock gracefully without ever blocking the receipt view. This closes the detail-view decryption follow-up deferred in 0.3.0.
  - The private key is held in the dashboard's process memory only — never persisted, never logged — and is zeroed on clear. Decryption reuses the audited SDK `receipt.DecryptDisclosure` (HPKE base mode, `hpke-x25519-hkdf-sha256-aes-256-gcm`).
  - Forensic key operations are refused unless the dashboard is bound to a loopback address (the default `127.0.0.1`), so a loaded key is never reachable from the network. As a second layer, the forensic endpoints also reject requests whose `Host` header is not loopback, blocking DNS-rebinding from a page in the operator's browser. New endpoints: `GET/POST/DELETE /api/forensic-key` and `GET /api/disclosure/{id}`.

### Changed

- **Bump `github.com/agent-receipts/ar/sdk/go` to `v0.15.0`** — picks up the forensic-disclosure helpers (`ForensicPublicFromPrivate`, `ForensicKeyFingerprint`) and HPKE encrypt/decrypt wiring from [agent-receipts/ar#722](https://github.com/agent-receipts/ar/pull/722).

### Fixed

- **Chain verification false negative** ([agent-receipts/ar#719](https://github.com/agent-receipts/ar/issues/719)) — `/api/chains/{id}/verify` reported valid chains as broken. The recomputed hash linkage round-tripped each receipt through the Go struct (`receipt.HashReceipt`), which drops any forward-compat fields a newer SDK wrote, so it disagreed with the canonical hash the collector stored. Verification now recomputes from the verbatim `receipt_json` wire bytes via `receipt.HashRawReceipt`, matching `agent-receipts verify` and any auditor reading the raw bytes. `store.GetChain` now returns the raw bytes alongside the parsed receipt.

## [0.3.0] - 2026-05-22

### Changed

- **Bump `github.com/agent-receipts/ar/sdk/go` to `v0.11.0`** (#67) — picks up the v0.3.0 envelope-shape `Action.parameters_disclosure` (`*receipt.DisclosureEnvelope` instead of the legacy `map[string]string`). Per [agent-receipts/ar#280](https://github.com/agent-receipts/ar/issues/280) and validated in [agent-receipts/ar#519](https://github.com/agent-receipts/ar/issues/519) Section 5.

### Fixed

- `internal/store/reader_test.go` refactored to compile against the envelope-shape disclosure type and to keep coverage of the SQL `OutputStatusMismatch` detector for both shapes (#67, #68). A new `TestReader_ListReceipts_OutputStatusMismatch_LegacyShape` inserts a raw v0.2.x-shaped `receipt_json` directly into the test DB so the legacy-flat-map mismatch path stays covered after the typed `sdkstore.Insert` switched to enforcing the envelope shape.

### Known follow-up

- The list-view disclosure preview path in `internal/store/reader.go` (`json_extract` of `.input` / `.output`, `disclosurePreviewMaxLen` truncation) is now functionally dead under v0.3.0: the envelope payload is opaque ciphertext, so the SQL extraction always yields empty strings. The detail-view rendering UX for encrypted disclosure (whether to surface envelope metadata, wire up a decryption path, etc.) is deliberately out of scope for this release and tracked separately.

## [0.2.2] - 2026-05-20

### Fixed

- Action distribution chart labels now truncate with ellipsis and show the full name on hover, preventing long action types from wrapping across multiple lines

## [0.2.1] - 2026-05-20

### Fixed

- Action distribution chart capped to top 5 types to keep the overview compact; remaining types shown as "+N more"

## [0.2.0] - 2026-05-20

### Added

- **Ed25519 signature verification** — `/api/chains/{id}/verify` now accepts a `?public_key=` (PEM-encoded) parameter; per-receipt `signature_valid` is returned and shown as Sig ✓/✗ badges in the chain modal (#5)
- **By-action stats breakdown** — `/api/stats` now includes `by_action` grouped by action type, rendered as an "Action distribution" bar chart alongside Risk and Status (#4)
- **Structured Outcome section** in receipt detail: reversibility badge (Reversible/Irreversible/Unknown), reversal method, reversal window, error banner, and before/after state-change hashes (#13)
- **Structured Intent and Authorization sections** in receipt detail: conversation/reasoning hashes with tooltips, truncated-preview indicator, scopes as badges, granted/expires timestamps with expiry indicator, and grant ref (#6)
- **JSON export** — download individual receipts as `receipt-{id}.json` or full chains as `chain-{chainId}.json` directly from the detail modals (#7)
- Output status mismatch detection — flags receipts where `outcome.status` is `success` but the disclosed tool output reports `isError: true` (#52)
- Dashboard UI polish: context header showing active database, keyboard navigation between receipts, and loading skeletons (#53)
- `?limit=N` query parameter on `/api/receipts` (capped at 10,000) (#58)

### Changed

- Default `ListReceipts` limit raised from 1,000 to 10,000 to match the ar SDK default (#11)

### Fixed

- Overview "Recent receipts" fetches only `RECENT_LIMIT` rows instead of the full store, reducing unnecessary bandwidth on every page load (#58)

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

[Unreleased]: https://github.com/agent-receipts/dashboard/compare/v0.5.1...HEAD
[0.5.1]: https://github.com/agent-receipts/dashboard/compare/v0.5.0...v0.5.1
[0.5.0]: https://github.com/agent-receipts/dashboard/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/agent-receipts/dashboard/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/agent-receipts/dashboard/compare/v0.2.2...v0.3.0
[0.2.2]: https://github.com/agent-receipts/dashboard/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/agent-receipts/dashboard/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/agent-receipts/dashboard/compare/v0.1.6...v0.2.0
[0.1.6]: https://github.com/agent-receipts/dashboard/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/agent-receipts/dashboard/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/agent-receipts/dashboard/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/agent-receipts/dashboard/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/agent-receipts/dashboard/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/agent-receipts/dashboard/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/agent-receipts/dashboard/releases/tag/v0.1.0
