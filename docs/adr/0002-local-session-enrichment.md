# ADR 0002: Local Session Enrichment (display-only, unverified)

**Status:** Accepted
**Date:** 2026-07-15

## Context

Every receipt the dashboard renders is a cryptographically signed record: the
signature covers `issuer`, `credentialSubject`, and the rest of the
`AgentReceipt`, and the verification path recomputes the canonical hash to
prove the chain is intact. That is the dashboard's whole value proposition —
what you see on a receipt is what was signed.

Operators running the dashboard on the same host as an agent also have
**local, unsigned** data that would make a receipt more useful to look at:
how many tokens the agent's session burned, how full its context window got,
roughly what the session cost. Claude Code, for example, writes a per-session
JSONL transcript under `~/.claude/projects/<encoded-cwd>/<session-id>.jsonl`
whose `assistant` lines carry `message.model` and `message.usage`
(input/output/cache token counts).

The temptation is to fold that data into the receipt view so it reads as one
record. That is exactly what we must not do. This data has **no cryptographic
standing**: it is read from a mutable local file, it is trivially forgeable,
it may be absent, and its schema is owned by an external tool that can change
it without notice. Mixing it into the signed structure — even visually — would
undermine the one guarantee the dashboard exists to provide.

This ADR records where the enrichment boundary sits and why, in the same
spirit as a pluggable-backend interface decision: the mechanism must make the
correct thing (keep enrichment out of the signed path) the easy thing, and
make adding a second agent source additive rather than a rewrite.

## Decision

Introduce **local session enrichment** as a display-only, explicitly
unverified sidecar to the signed receipt, implemented entirely within the
dashboard process.

### The boundary

1. **Enrichment lives only in the dashboard process.** The daemon, the
   agent-side emitter, and the receipt schema are not touched. Enrichment is
   computed at request time from local files; it is never written back into
   the SQLite store, a receipt, or anything that is hashed or signed.

2. **It is a sibling of `receipt`, never a member of it.** The receipt-detail
   API response becomes `{ "receipt": <AgentReceipt>, "enrichment": <Enrichment|null> }`.
   `enrichment` is never merged into `credentialSubject`, `issuer`, or any
   signed sub-object. The receipt object handed to the client is byte-for-byte
   the signed structure.

3. **Every enrichment payload is self-labelling.** It always carries
   `unverified: true` and a `source` naming the agent type it came from
   (`"claude-code"` to start). The UI renders it with muted styling and a
   "local, unverified" tooltip, visually distinct from signed fields.

4. **The join key is `issuer.session_id`** — an existing optional receipt
   field — matched against locally discoverable session files. No receipt
   without a session id can be enriched; that is not an error.

### The interface

Source lookup is pluggable via an `enricherSource` registry. Each source is
defined by three things:

- **name** — the agent type it represents (`"claude-code"`).
- **glob** — how it expands a `session_id` into candidate file paths.
- **parse** — how it turns one file into an `Enrichment`.

Adding a second agent type (a different transcript layout, a different tool)
means writing one more source and registering it — the enricher, the API
shape, and the UI do not change. This pass implements only the Claude Code
source; the interface exists so the next one is additive.

### Failure is silence, not error

- A missing, unreadable, or malformed session file yields **empty
  enrichment** (`null`), never an error surfaced to the user and never a block
  on receipt rendering. The parser fails soft: it logs and omits, and never
  panics on an unexpected shape.
- Cost is derived from a **local pricing table keyed by model name**. If the
  model is absent from the table, `estimated_cost_usd` is `nil` — never zero,
  never a guess.
- Parsed results are cached on `(path, mtime, size)`, not `session_id` alone,
  so an edited or rotated session file is re-read rather than served stale.

## Consequences

- The signed-vs-unsigned distinction is preserved structurally: enrichment
  cannot leak into a receipt because it is never assembled into one. The read
  path to the store stays read-only.
- The dashboard now depends, softly, on Claude Code's on-disk JSONL layout.
  That dependency is deliberately un-contracted: there is no schema-stability
  promise, and the parser is written to degrade to empty enrichment rather
  than fail when the format shifts. If the location or field names differ from
  what a future Claude Code writes, the surface to fix is a single source's
  glob/parse.
- Enrichment is best-effort by construction. It appears when the dashboard
  runs on the same host/user as the agent and the session file is present;
  otherwise it is simply absent. No cross-machine or remote lookup is in
  scope.
- Cost and context-window figures are estimates against a hand-maintained
  pricing/window table. They are labelled unverified precisely because they
  can drift from real billing; they are an operator convenience, not an
  accounting record.
