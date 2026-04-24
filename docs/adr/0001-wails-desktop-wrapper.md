# ADR 0001: Wails as Desktop Wrapper

**Status:** Accepted  
**Date:** 2026-04-24

## Context

The dashboard is currently a Go HTTP server served in a browser tab. Users must launch it from the terminal and navigate to `localhost:<port>` manually. A native desktop experience — double-click to open, native file picker for `.db` files, menu bar integration — would significantly improve usability.

Three options were evaluated:

| Option | Binary size | Supply chain | Native feel |
|---|---|---|---|
| Electron | ~100 MB (bundled Chromium) | Large (hundreds of npm deps) | Excellent |
| Wails | ~15 MB (OS webview) | Small (Go modules + minimal JS) | Very good |
| go-webview | ~5 MB (OS webview) | Smallest | Minimal (DIY menus/dialogs) |

## Decision

Adopt **Wails v2** as the desktop wrapper.

The existing HTTP server and htmx/Tailwind frontend remain intact. Wails replaces the standalone `net/http` listener with its own IPC bridge, keeping Go as the primary language throughout.

## Rationale

- **Supply chain:** Electron pulls in Node plus hundreds of transitive npm packages (past incidents: `event-stream`, `ua-parser-js`, `node-ipc`). Wails' dependency footprint is a Go module tree plus an optional small frontend bundler — auditable in `go.sum`.
- **OS webview security:** Instead of shipping a pinned Chromium, Wails delegates rendering to the OS webview (WebKit on macOS/Linux, WebView2 on Windows). Security patches arrive through OS updates rather than requiring a separate Chromium patch cadence.
- **Go-first ethos:** The codebase is pure Go today. Wails keeps backend logic in Go with no Node runtime required at build or runtime.
- **Binary size:** ~15 MB vs ~100 MB for Electron; single distributable per platform.
- **go-webview** was ruled out because it provides no built-in menus, dialogs, or auto-update hooks — features needed for a polished file-picker experience.

## Consequences

- Rendering is subject to OS webview engine differences (WebKit vs WebView2 vs WebKitGTK). The htmx + Tailwind UI must be tested on each platform.
- The `./dashboard -db <path> -port <port>` CLI flow is preserved; Wails wraps it and adds a native open-file dialog as an alternative entry point.
- Distribution changes: platform-specific bundles (`.app`, `.exe`, `.deb`) replace the single cross-platform binary. Code signing and notarization are required for macOS and Windows distribution.
- A GitHub Actions workflow will need updating to build Wails targets per platform.
