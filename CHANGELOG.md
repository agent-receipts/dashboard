# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Structured issuer and principal details in the receipt detail view** — the detail modal now renders the issuer's `type` and (when present) top-level `model` and `operator` (`{name, id}`) as their own labelled fields, and shows the principal's `type` as a badge alongside its id. Previously these fields were only visible in the raw JSON blob. All new fields render conditionally and degrade gracefully when absent.
- **Action type taxonomy awareness** — the dashboard now surfaces the SDK's built-in action-type registry so auditors can interpret raw action types without external docs. A new `GET /api/taxonomy` endpoint serves every known action type with its description and default risk level, grouped by category (Filesystem, System, Data, Network, Diagnostic, Other). The Actions view gains a collapsible **Action type reference** card listing all built-ins grouped by category, and known action types in the receipts list, Actions stats table, and receipt detail now carry a hover tooltip ("Read a file · default risk: low"); the detail modal additionally shows a **Meaning** row. Unknown action types degrade gracefully to plain text.

### Fixed

- **Chain signature verification no longer false-negatives on forward-compat receipts** ([#73](https://github.com/agent-receipts/dashboard/issues/73)) — `GET /api/chains/{id}/verify?public_key=…` now checks each Ed25519 signature against the receipt's verbatim wire bytes via the SDK's new `receipt.VerifyRaw`, instead of re-marshalling the parsed Go struct with `receipt.Verify`. The struct path dropped any field a newer SDK signed over but nested inside the payload (e.g. under `credentialSubject`), canonicalizing different bytes and reporting a genuinely valid signature as invalid. This is the signature-side twin of the hash-linkage fix for [#719](https://github.com/agent-receipts/ar/issues/719); the signature path now matches the existing `HashRawReceipt` hash path. Requires SDK `v0.20.0-alpha.2`.

## [0.9.0-alpha.1] - 2026-06-19

### Added

- **Connected-component highlight in agent graph** — clicking a node now highlights its entire state-dep connected component (all agents reachable via shared-resource contention edges), dims everything outside it, and emphasizes the in-component state-dep edges. Delegation edges are excluded from component computation so the root node does not collapse the whole graph into one component. A node with no state-dep edges highlights only itself, preserving today's single-node behavior. The blast-radius panel and receipts filter remain keyed to the clicked node, unchanged.

## [0.8.0] - 2026-06-12

### Fixed

- **`⚠ mv ops` no longer fires on file deletions** — `has_move_ops` was detected with `strings.Contains(actionType, "move")`, which matched `filesystem.file.remove` (delete) because "remove" contains the substring "move". Detection now requires an exact match against `filesystem.file.move` or `filesystem.file.rename`.
- **Bidirectional state-dep edges collapse to one** — when two agents alternately wrote the same file (A→B→A), both A→B and B→A edges were emitted, rendering as contradictory overlapping arrows. Edge keys are now canonicalised (sorted) so any pair of agents produces at most one state-dep edge per shared resource.
- **Empty-session attribution response uses `[]` not `null`** — `/api/sessions/{id}/attribution` returned `"nodes":null,"state_deps":null` for sessions with no receipts; these fields now always serialize as `[]`.
- **Unknown edge types no longer rendered as delegation** — the session graph filtered delegation edges with `!e.type || e.type === 'delegation'`; any future edge type without a frontend handler would silently appear as a dashed-gray delegation line. The filter is now an exact `type === 'delegation'` check.
- **Agent-ID truncation in blast-radius panel no longer appends `…` to short IDs** — IDs shorter than 12 characters were labelled with a spurious trailing ellipsis implying truncation; the panel now uses `truncateHash` which only appends `…` when the string was actually clipped.
- **Coverage integers wrapped with `escapeHtml`** — `identity_receipts` and `total_receipts` are now passed through `escapeHtml(String(...))` before being interpolated into the coverage bar `innerHTML`.

### Added

- **Transcript-derived model and token usage in receipt detail** — the dashboard now surfaces `issuer.runtime.model`, `issuer.runtime.capture_method`, and token usage (`input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`) from `issuer.runtime.usage` when present in a receipt (obsigna PR #779). These fields are shown in the receipt detail modal: `runtime.model` and `runtime.capture_method` appear alongside the existing `runtime.agent_id`/`agent_type` in the Issuer block, and a dedicated **Token usage** row shows the per-call token counts (including cache reads and writes when non-null). Older receipts without these fields degrade gracefully. The `runtime_model` field is also included in every `ReceiptRow` API response, and the session delegation graph now prefers the transcript-derived model over the static `issuer.model` when both are present.
- **ADR-0029 §4 attribution and blast-radius view** — the session graph panel now shows cross-agent state-dependency edges alongside delegation edges. State deps (solid blue curved arrows with arrowheads) are drawn when two agents touch the same `action.target.resource` path, ordered by timestamp + chain + sequence; these are provably causal. Delegation edges are now dashed gray to stay visually distinct.
- **Blast-radius panel on node click** — selecting a graph node reveals a compact attribution bar showing: the files the node touched (identity coverage), cross-agent state deps it participates in (→ / ← labels), and a heuristic semantic-coupling warning count. Semantic deps are surfaced as a warning annotation only and are never drawn as edges, per ADR-0029 §4.
- **Risk rings on graph nodes** — nodes with high or critical maximum risk level now display a coloured outer ring (orange for high, red for critical) so high-risk nodes with large state-dependency sets are immediately visible.
- **Coverage fraction in modal header** — the Session Graph modal header shows how many receipts in the session are identity-indexable (`N / M receipts identity-indexed (P%)`). When move/rename operations are present, a `⚠ mv ops` warning appears because path strings may not reliably identify file versions across renames.
- **`GET /api/sessions/{sessionID}/attribution` endpoint** — returns the full §4 attribution payload: coverage fraction, has_move_ops flag, per-node attribution (receipt count, identity count, max risk, risk profile, semantic dep count), cross-agent state dep edges, and per-agent blast-radius resource lists. No schema migration; `action.target.resource` is extracted from `receipt_json` via `json_extract` at query time.

## [0.7.0] - 2026-06-11

### Added

- **Session graph view** — each row in the Sessions table now has a "Graph" button that opens a node-link graph of the session's agent delegation tree. Nodes represent agents (orchestrator + sub-agents), sized by receipt count and labelled by `agent_type`. Delegation edges connect orchestrators to their sub-agents. Clicking a node filters the receipt list below the graph to that agent's receipts; clicking again resets the filter. Works for single-agent sessions (just the root node) and multi-agent sessions.
- **Model label in session graph** — each agent node in the delegation graph now shows the model name (e.g. `claude-sonnet-4-6`) when it is present in the receipt data (`issuer.model`). The field is exposed through the store layer as `issuer_model` on every `ReceiptRow`.

### Fixed

- **Forensic key parsing no longer fails for keys ending in a line-ending byte** — `parseForensicPrivateKey` was using `bytes.TrimSpace` to handle raw keys with a trailing newline, but `TrimSpace` strips any whitespace byte (0x0A, 0x0D, 0x09, 0x20). Since X25519 keys are random bytes, ~2% of keys end in such a byte, causing the trailing-newline and CRLF upload paths to silently consume a real key byte and return "unrecognised key encoding or wrong length". Fixed by stripping trailing `\r`/`\n` bytes only while the slice exceeds 32 bytes, so the parser never trims into the key itself.

## [0.7.0-alpha.5] - 2026-06-10

### Fixed

- **Session ID filter no longer sticky** — clearing the Session ID filter (via the chip ×, "Clear all", or emptying the input) now removes `session_id` from the URL instead of leaving a stale value behind. Previously only `?q=` was kept in sync, so a `?session_id=…` deep link or session click would survive a clear and get re-applied on the next reload/poll, forcing users to hand-edit the URL to widen the view.

## [0.7.0-alpha.4] - 2026-06-09

### Fixed

- **Sub-agent grouping now works against live receipts** — the dashboard was reading sub-agent identity from `issuer.agent_id`, a flat field that the daemon never emitted. As of protocol v0.5.0 / daemon v0.18.0-alpha.1 (ADR-0026), agent identity lives under `issuer.runtime`. The session-grouping and list queries now extract `$.issuer.runtime.agent_id`; `$.issuer.runtime.agent_type` is also surfaced and shown as a badge on subagent swimlane headers and in the receipt detail modal. SDK bumped from 0.15.0 → 0.17.0-alpha.1 to pick up the updated `Issuer.Runtime` struct.

## [0.7.0-alpha.3] - 2026-06-09

### Fixed

- **Sessions tab agent count** — sessions now always show at least 1 agent. Previously the agent count used `issuer.agent_id`, a Layer 3 extension field not yet produced by any current daemon version, so the count was always 0. The query now falls back to `issuer.id` when `agent_id` is absent; once the daemon starts emitting `agent_id` for multi-agent sessions, the correct subagent count will appear automatically.

## [0.7.0-alpha.2] - 2026-06-09

### Added

- **Session ID filter** — the Receipts tab now has a "Session ID" filter input that restricts the list to receipts from a specific agent session (`issuer.session_id` exact match). Clicking a session header in the grouped view pre-fills this input and reloads. The "Clear filters" / per-chip clear buttons also clear the new input. The `GET /api/receipts` endpoint accepts a `session_id` query parameter backed by a `json_extract` WHERE clause.
- **Session column in receipts table** — a Session column (truncated to 8 chars with the full ID in a tooltip) appears after Seq in both the flat and grouped receipts table. Clicking the session chip pre-fills the Session ID filter and reloads the list. Receipts without a session ID show a dash.
- **Session link in receipt detail modal** — the receipt detail view now shows a clickable Session field (below Chain) when the receipt carries an `issuer.session_id`. Clicking closes the modal and filters the receipts list to that session.
- **Keyboard navigation between receipts in the detail view** — while the receipt detail modal is open, pressing `j` / `↓` opens the next receipt in the current list and `k` / `↑` opens the previous one. Navigation stops at the ends (no wrap). Works correctly in both flat and grouped table layouts; session/agent header rows are skipped. The keyboard shortcuts help modal documents these new bindings.
- **Jump to session from Overview recent receipts** — each receipt row in the Overview "Recent receipts" mini-table now shows a small `session` pill when the receipt carries a `session_id`. Clicking the pill switches to the Receipts tab and pre-filters it to that session. A `?session_id=<value>` URL parameter is also supported as a deep link (consistent with the existing `?q=` search deep link).
- **Sessions tab** — a new dedicated Sessions tab lists all agent sessions extracted from stored receipts. Each row shows the session ID, receipt count, number of distinct agents, and first/last seen timestamps. Clicking a row switches to the Receipts tab pre-filtered to that session. A `GET /api/sessions` endpoint backs the tab, accepting an optional `?range=` parameter (e.g. `24h`, `7d`) to restrict to recent sessions. The `g s` keyboard shortcut navigates to the tab. Receipts without a `session_id` are excluded.

## [0.7.0-alpha.1] - 2026-06-09

### Added

- **Layer 3 attribution rendering** — the Receipts view now renders session grouping, subagent swimlanes, correlation pairing, and delegation edges for receipts emitted by daemon ≥ v0.17.0 / hook ≥ v0.14.0. When any receipt in the current result set carries a `session_id`, the flat table is replaced with a grouped layout:
  - **Session groups** — receipts are grouped under collapsible session headers keyed by `issuer.session_id`; clicking the ▾ button hides all rows in that session.
  - **Subagent swimlanes** — within each session, receipts are split by `issuer.agent_id` into labelled sub-groups ("Orchestrator" for the root agent, "↳ Subagent <id>" for spawned agents).
  - **Delegation edges** — subagent swimlane headers show a `← <parent_chain_id>` indicator when the first receipt of that agent carries a `credentialSubject.delegation` object, making parent→child chain relationships visible at a glance.
  - **Correlation pairing** — hook pre-check and mcp-proxy post-action receipts sharing the same `credentialSubject.correlation_id` are collapsed into a single row showing both statuses (`pre-status → post-status`) with a `pre+post` badge; clicking opens the post-action receipt.
  - **Graceful degradation** — old receipts without these fields continue to render correctly in both the flat and grouped layouts; receipts without a `session_id` are grouped under a "Legacy receipts" section at the bottom.

## [0.6.1] - 2026-06-09

### Fixed

- **Overview no longer crashes when the selected time range contains no receipts** — API endpoints now return `[]` instead of `null` for empty result sets, and the frontend guards against a `null` receipts response to show a clean zero-state instead of an error banner.

## [0.6.0] - 2026-06-08

### Added

- **Activity timeline chart on the Overview tab** — a new collapsible "Activity timeline" card sits between the stats summary cards and the distribution grid. It renders an inline SVG stacked bar chart of receipt counts bucketed over the active time range: a green (success) segment at the bottom, red (failure) above it, and a muted grey segment for any remaining statuses (pending, other). Y-axis scales to the maximum bucket total; bars share the full panel width with small gaps. X-axis shows 4–6 evenly spaced tick labels, formatted as `HH:MM` for intraday ranges and `Mon D` for multi-day ranges. Each bar carries a native SVG `<title>` tooltip with the bucket timestamp, success count, failure count, and total. An empty-state message is shown when all buckets are zero. The chart updates automatically whenever the range picker changes (since `setOverviewRange()` re-calls `loadOverview()`), and its collapsed state is remembered in `localStorage` like other Overview cards.
- **Error-rate sparkline on the Overview tab** — a new collapsible "Error rate over time" card renders an inline SVG polyline of failure % per timeseries bucket (same buckets as the `/api/stats/timeseries` endpoint). The current rate is shown as large text coloured green (<5%), amber (5–20%), or red (>20%). A trend arrow (↑/↓/→) compares the latest non-empty bucket to the previous one. Dashed reference lines at 5% and 20% mark the colour thresholds. Null (zero-total) buckets lift the line off the axis instead of snapping to 0%. The card collapses and remembers state via the existing `data-card="error-rate"` mechanism.
- **Throughput stat card (replaces "Latest")** — the fourth Overview stat card now shows **Receipts / hr** (switching to **Receipts / min** when the hourly rate exceeds 60). The rate is computed as `stats.total ÷ range hours`; for the "All" preset the span is derived from the timeseries `range_from`/`range_to`. A ↑/↓/→ trend arrow compares the current rate against the immediately preceding equal-length window (fetched from `GET /api/stats?after=&before=`); for "All" no comparison is shown. The app header "latest" slot (last-activity time) is unaffected.

- **Time range picker on the Overview tab** — a persistent preset bar (`1h · 6h · 24h · 7d · 30d · All`, default `24h`) at the top of the Overview drives every panel on that tab. Clicking a preset immediately re-renders all cards — stat totals, risk/status/action distributions, top actions by failure rate, server activity, and recent receipts — to reflect only receipts in the selected window. The active preset is stored in the URL hash (`#range=<preset>`) so it survives a page reload and can be bookmarked. A reusable `overviewRangeParams()` accessor returns `{ preset, after }` for consumption by future activity-timeline (#84) and error-rate (#85) panels.
- **`GET /api/stats/timeseries`** — a new endpoint returns a continuous time-bucketed series of receipt counts broken down by `status` and `risk_level`. Accepts `range=<Go duration>` (relative window ending now) or `from=<ISO>&to=<ISO>` (absolute); optional `bucket=<Go duration>` (auto-selected from the window when absent: ≤2h → 5m, ≤24h → 1h, ≤7d → 6h, else → 1d). Empty buckets are always included so callers get a continuous series. Responds with `{ "buckets": [...], "bucket_duration": "1h", "range_from": "...", "range_to": "..." }`. Pathological range/bucket combinations that would exceed 2 000 buckets return 400.
- **`GET /api/stats` now accepts `after` and `before` query parameters** — optional ISO-8601 timestamps (inclusive) that filter all aggregate counts (total, chains, by_risk, by_status, by_action, latest_timestamp) to the specified window. Omitting both preserves the existing all-time behaviour with no regression.

- **Top actions by failure rate** — a new `GET /api/stats/actions` endpoint returns per-action-type failure statistics (total, success, failure counts, and failure rate), filtered to action types with at least 5 receipts. An optional `range` query parameter (Go duration string, e.g. `24h`) restricts the window. Results are sorted by failure rate descending. A new **Actions** tab renders the full sortable table with clickable column headers and a "Show all" toggle; clicking a row pre-filters the Receipts view to that action type's failures. The Overview tab gains a new "Top actions by failure rate" summary card showing the top 5 action types as horizontal bars, with a "View all →" link to the Actions tab.
- **Free-text search on the Receipts tab** — a "Search" input field at the left of the filter bar lets you search across the full raw receipt JSON (action type, tool name, IDs, parameters, and more). The active term is reflected in the URL as `?q=<value>` on `GET /api/receipts`, making searches bookmarkable and shareable. Loading the dashboard with `?q=<term>` pre-fills the search and opens the Receipts view automatically. The match count updates to "N receipts matching '<term>'" when a search is active.
- **Server/tool breakdown panel** — a new "Servers" tab shows an expandable table of every MCP server (extracted from `credentialSubject.action.target.system`) with per-server totals, failure counts, and a mini failure-rate bar. Each server row expands to reveal its tools with the same metrics. Clicking a tool row pre-filters the Receipts view to that server and tool. Receipts with no server are folded into an "Unknown" group, which is listed after named servers. The Overview tab gains a "Server activity" summary card showing the top 5 named servers as horizontal bars. New endpoint: `GET /api/stats/servers` (optional `?range=<duration>` for time-scoped results).
- **Server and tool filters on Receipts** — two new filter inputs (`Server` and `Tool`) on the Receipts tab allow filtering by `target.system` and `tool_name` independently or in combination, with the same chip and clear-all behaviour as existing filters.
- **Collapsible Overview cards** — each card on the Overview tab (Risk / Status / Action distribution, Top actions, Server activity, Recent receipts) has a chevron to collapse it down to its title. Collapsed state is remembered per card in `localStorage`, so cards you hide stay hidden across reloads.
- **GitHub link in the header** — a GitHub icon next to the keyboard-shortcuts button opens the project repository (`github.com/agent-receipts/dashboard`) in a new tab, so operators can find the source or report an issue.

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

[Unreleased]: https://github.com/agent-receipts/dashboard/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/agent-receipts/dashboard/compare/v0.7.0-alpha.5...v0.7.0
[0.6.0]: https://github.com/agent-receipts/dashboard/compare/v0.5.1...v0.6.0
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
