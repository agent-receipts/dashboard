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

## Disclosure Policy

- Reports are triaged within 48 hours
- Fixes are coordinated with the reporter before public disclosure
- Reporters are credited in release notes unless they prefer anonymity
