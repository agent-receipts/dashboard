# Security Policy

## Supported Versions

Security fixes are applied to the latest release. We do not backport fixes to older versions.

## Reporting a Vulnerability

**Do not report security vulnerabilities through public GitHub issues.**

Instead, please use GitHub's **Report a vulnerability** feature on this repository:
[Report a vulnerability](https://github.com/agent-receipts/dashboard/security/advisories/new)

Include as much detail as possible: description, steps to reproduce, impact assessment, and any suggested fix.

## Scope

This policy covers the dashboard application. Security reports for this project include:

- Unauthorized write access to receipt SQLite databases (the dashboard must be read-only)
- Path traversal or arbitrary file access via database path configuration
- Cross-site scripting (XSS) through receipt data rendered in the web UI
- Server-side request forgery or command injection via configuration
- Information disclosure through error messages or debug output

For vulnerabilities in the protocol specification, SDKs, or MCP proxy, report to the [main repository](https://github.com/agent-receipts/ar/security/advisories/new) instead.

## Forensic key endpoints

The dashboard can hold an X25519 **forensic private key** in memory to decrypt the `parameters_disclosure` envelopes attached to receipts. Two endpoints load that key:

- `POST /api/forensic-key` — upload the raw key material in the request body (raw 32 bytes, hex, base64, or PKCS#8 PEM).
- `POST /api/forensic-key/path` — supply a JSON body naming a filesystem path (`{"path": "..."}`) that the server reads the key from.

Because a loaded key can decrypt every matching disclosure, these endpoints are deliberately confined to a locally-run operator. The threat model treats the local machine and its operator as trusted; the guards below close the remote and cross-origin paths to that trusted surface.

### Guards in place

| Guard | Mechanism | Applies to |
|-------|-----------|------------|
| Loopback-only bind | Key operations are refused unless the dashboard is bound to a loopback address (`localhost`/`127.0.0.1`/`::1`), which keeps the socket off the network. An all-interfaces bind (`""`/`0.0.0.0`/`::`) is network-reachable but disables forensic key operations entirely. | both endpoints |
| Host-header validation (DNS-rebinding defence) | Every request's `Host` header must name a loopback address, so a remote page cannot resolve its own domain to `127.0.0.1` and drive the loopback-bound server as if it were same-origin. | both endpoints |
| Cross-origin rejection | A request carrying an `Origin` header whose host:port does not exactly match the request `Host` is refused (an opaque `null` origin is refused too). Absent `Origin` — as sent by curl and SDK clients — is allowed. | both endpoints |
| CSRF: required `application/json` Content-Type | The path endpoint requires `Content-Type: application/json`, which forces a cross-origin browser POST into a CORS preflight instead of a "simple" request, closing the same-browser CSRF gap that could otherwise drive arbitrary local file reads. | `POST /api/forensic-key/path` only |
| Path allowlist (rejects `..` and NUL) | The supplied path is validated before any filesystem access: an embedded NUL byte or any `..` traversal segment is rejected outright, the cleaned result must be absolute, and it must equal or sit beneath an allowed root (boundary-safe prefix check). | `POST /api/forensic-key/path` only |

The raw-upload endpoint (`POST /api/forensic-key`) carries no path, so the path allowlist and the `application/json` requirement do not apply to it; it is protected by the loopback bind, Host-header, and cross-origin guards, and by a body-size limit.

### Default allowlist root

By default the path endpoint's allowlist contains **the operator's home directory**, which is allowed automatically whenever it can be resolved (in the rare case that the home directory cannot be resolved to an absolute path, it is silently omitted). As a result, any file under `$HOME` can normally be supplied as a candidate key path. Additional roots may be added — and the home directory default is never removed by adding them — via the `-forensic-key-dirs` flag (a comma-separated list of extra absolute directories).

### Residual risk

This is a **locally-run forensic tool**. The guards above close the remote and cross-origin attack paths: the key cannot be loaded or exercised from the network, via DNS rebinding, or by a hostile cross-origin page in the operator's browser. What they do not narrow is the *breadth of the default allowlist root*: with the home-directory default, a same-origin actor able to reach the loopback endpoint (i.e. code already running locally as the operator) can point the path endpoint at any file under `$HOME`. This is a default, not a fixed policy — it is narrowable with `-forensic-key-dirs`.

### Recommendation

Operators handling sensitive keys should set `-forensic-key-dirs` to the minimum directory (or directories) actually needed, rather than relying on the broad `$HOME` default. Scoping the allowlist to, for example, only the directory that holds the forensic key limits what the path endpoint can ever read.

## Disclosure Policy

- Reports are triaged within 48 hours
- Fixes are coordinated with the reporter before public disclosure
- Reporters are credited in release notes unless they prefer anonymity
