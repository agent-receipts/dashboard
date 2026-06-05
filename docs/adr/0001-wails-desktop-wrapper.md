# ADR 0001: Wails as Desktop Wrapper

**Status:** Superseded  
**Date:** 2026-04-24  
**Superseded:** 2026-06-05 — see [issue #38](https://github.com/agent-receipts/dashboard/issues/38)

## Context

The dashboard is currently a Go HTTP server served in a browser tab. Users must launch it from the terminal and navigate to `localhost:<port>` manually. A native desktop experience — double-click to open, native file picker for `.db` files, menu bar integration — would significantly improve usability.

Options evaluated:

| Option | Binary size | Supply chain | Native feel |
|---|---|---|---|
| Electron | ~100 MB (bundled Chromium) | Large (hundreds of npm deps) | Excellent |
| Wails v2 | ~15–30 MB (OS webview) | Small (Go modules + minimal JS) | Very good |
| go-webview | ~5–10 MB (OS webview) | Smallest | Minimal (DIY menus/dialogs) |
| Tauri | ~10–20 MB (OS webview) | Small (Rust crates) | Very good |

Tauri was set aside because it would introduce a Rust toolchain to an otherwise pure-Go codebase.

## Decision

Adopt **Wails v2** as the desktop wrapper, running alongside (not replacing) the existing CLI/HTTP mode.

### Architecture

The existing `net/http` server is **retained**. htmx drives the UI via HTTP requests (`hx-get`, `hx-post`), which are incompatible with Wails' JS-binding IPC model. The pragmatic path is:

- Wails launches the dashboard's HTTP server on a local loopback port.
- Wails' embedded webview loads `http://127.0.0.1:<port>/`.
- Wails bindings are used only for features the webview can't provide — native file-open dialog, menu actions, window lifecycle.

This keeps the htmx/Tailwind UI and the `internal/server` routes completely unchanged. The read-only `internal/store` layer is untouched — the Wails wrapper adds no write paths.

### Distribution modes

Both modes coexist:

- **CLI mode** (`./dashboard -db <path>`): unchanged, for headless / server / SSH use.
- **Desktop mode** (`Dashboard.app` / `.exe` / `.deb`): Wails-wrapped, for double-click launch with a native file picker.

## Rationale

- **Supply chain:** Electron pulls in Node plus hundreds of transitive npm packages (past incidents: `event-stream`, `ua-parser-js`, `node-ipc`). Wails' Go deps are auditable in `go.sum`; any frontend tooling (e.g. Tailwind CLI) is tracked in a lockfile (`package-lock.json` / `pnpm-lock.yaml`) — still far smaller than Electron's tree.
- **OS webview security:** Instead of shipping a pinned Chromium, Wails delegates rendering to the OS webview — WKWebView on macOS, WebView2 on Windows, WebKitGTK on Linux. Security patches arrive through OS updates rather than requiring a separate Chromium patch cadence.
- **Go-first ethos:** The codebase is pure Go today. Wails keeps backend logic and the runtime entirely in Go — no Node at runtime. Build-time tooling depends on the chosen frontend pipeline (e.g. Tailwind CLI requires Node).
- **Binary size:** ~15–30 MB (platform-dependent) vs ~100 MB for Electron.
- **go-webview** was ruled out because it provides no built-in menus or native dialogs — features needed for a polished file-picker experience. Neither Wails nor go-webview ship a first-party auto-updater; that's acceptable for now.

## Consequences

### Architectural

- The htmx + Tailwind UI must be tested on each platform's webview (WebKit, WebView2, WebKitGTK) — rendering differences are possible.
- Wails v2's default templates assume a Vite + Svelte/React/Vue frontend. Using raw htmx + Tailwind is off the paved road; we accept owning a custom Wails project layout.
- **Tailwind CDN must be replaced** with a vendored build (Tailwind CLI or a small embedded bundle) so the desktop app works offline. The current CDN dependency is the single biggest blocker.
- The read-only guarantee is preserved — `internal/store` continues to open SQLite in read-only mode; Wails does not introduce new write paths.

### Build & distribution

- Platform-specific bundles (`.app`, `.exe`, `.deb`) are produced in addition to the existing single Go binary.
- Code signing and notarization are required for distributing macOS and Windows builds.
- CI (`.github/workflows`, `lefthook.yml`) needs updating to build Wails targets per platform.
- Go `go test ./...` continues to work unchanged for backend logic; UI testing strategy for the Wails shell is TBD.
