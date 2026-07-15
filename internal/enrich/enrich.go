// Package enrich resolves display-only, locally-derived session metadata for a
// receipt and joins it by session id.
//
// This data is UNVERIFIED by construction: it is read from an agent's local
// session files on the same host, never from the signed receipt. It must never
// be written into a receipt, the daemon, the SQLite store, or the verification
// path. It exists only to make the receipt view more useful for an operator
// running on the same machine as the agent. See
// docs/adr/0002-local-session-enrichment.md for the boundary rationale.
package enrich

import (
	"log"
	"os"
	"strings"
	"sync"
)

// Enrichment is display-only, locally-derived, unverified session data joined
// to a receipt by session id. It is a sibling of the signed receipt in the API
// response, never a member of it.
type Enrichment struct {
	// Unverified is always true. It is emitted explicitly so a client cannot
	// mistake this payload for signed data.
	Unverified bool `json:"unverified"`
	// Source names the agent type the data came from, e.g. "claude-code".
	Source string `json:"source"`
	// Model is the model identifier reported by the session file (e.g.
	// "claude-opus-4-8"). Empty when the session recorded no model.
	Model string `json:"model,omitempty"`

	// Token counts summed across the session's assistant turns.
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	TotalTokens         int64 `json:"total_tokens"`

	// ContextTokens is the input context size of the session's most recent
	// turn (input + cache-read + cache-creation) — i.e. how full the context
	// window was on the last turn.
	ContextTokens int64 `json:"context_tokens"`
	// ContextWindow is the model's context window in tokens, when the model is
	// known to the local table. Zero (omitted) otherwise.
	ContextWindow int64 `json:"context_window,omitempty"`
	// ContextPct is ContextTokens / ContextWindow * 100, when the window is
	// known. Nil when the model is not in the table.
	ContextPct *float64 `json:"context_pct,omitempty"`

	// EstimatedCostUSD is a local estimate from a pricing table keyed by model
	// name. It is nil — never zero, never a guess — when the model is absent
	// from the table.
	EstimatedCostUSD *float64 `json:"estimated_cost_usd"`
}

// SessionEnricher resolves display-only enrichment for a receipt's session id.
//
// A missing, unreadable, or unparseable session file is not an error: Enrich
// returns nil so the caller renders the receipt without enrichment. There is
// deliberately no error return — enrichment must never surface a failure to the
// user or block receipt rendering.
type SessionEnricher interface {
	Enrich(sessionID string) *Enrichment
}

// enricherSource is a pluggable per-agent-type provider of local session data.
// Adding a second agent type means implementing another source and registering
// it — the enricher, the API shape, and the UI do not change. A source is fully
// described by its name, a glob that expands a session id into candidate file
// paths, and a parse step that turns one file into an Enrichment.
type enricherSource struct {
	name  string
	glob  func(sessionID string) ([]string, error)
	parse func(path string, info os.FileInfo) (*Enrichment, error)
}

// cacheKey identifies a parsed session file by path and the file's mtime and
// size, not by session id alone. An edited or rotated file changes the key and
// is re-parsed rather than served stale.
type cacheKey struct {
	path  string
	mtime int64
	size  int64
}

// Enricher is the default SessionEnricher. It tries each registered source in
// order and returns the first non-nil enrichment. Parsed results (including
// "no data") are cached on (path, mtime, size).
type Enricher struct {
	sources []enricherSource

	mu    sync.Mutex
	cache map[cacheKey]*Enrichment
}

// New returns an Enricher with the Claude Code source registered.
func New() *Enricher {
	return newEnricher(claudeCodeSource(claudeProjectsDir()))
}

// newEnricher builds an Enricher over an explicit source list. Used by tests to
// inject sources rooted at a temporary directory.
func newEnricher(sources ...enricherSource) *Enricher {
	return &Enricher{sources: sources, cache: map[cacheKey]*Enrichment{}}
}

// Enrich resolves enrichment for sessionID, or nil when no local session data
// is available. It never returns an error and never panics on a malformed file.
func (e *Enricher) Enrich(sessionID string) *Enrichment {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	for _, s := range e.sources {
		if enr := e.enrichFrom(s, sessionID); enr != nil {
			return enr
		}
	}
	return nil
}

// enrichFrom resolves enrichment from a single source, applying the cache. A
// glob or parse failure is logged and treated as "no enrichment".
func (e *Enricher) enrichFrom(s enricherSource, sessionID string) *Enrichment {
	paths, err := s.glob(sessionID)
	if err != nil {
		log.Printf("enrich: %s glob for %q: %v", s.name, sessionID, err)
		return nil
	}

	// A session id can match in more than one project directory; use the most
	// recently modified file as the best representative of the live session.
	var bestPath string
	var bestInfo os.FileInfo
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil || info.IsDir() {
			continue
		}
		if bestInfo == nil || info.ModTime().After(bestInfo.ModTime()) {
			bestPath, bestInfo = p, info
		}
	}
	if bestInfo == nil {
		return nil
	}

	key := cacheKey{path: bestPath, mtime: bestInfo.ModTime().UnixNano(), size: bestInfo.Size()}

	e.mu.Lock()
	if cached, ok := e.cache[key]; ok {
		e.mu.Unlock()
		return cached
	}
	e.mu.Unlock()

	enr, err := s.parse(bestPath, bestInfo)
	if err != nil {
		// Fail soft: a parse error yields no enrichment, never a surfaced error.
		// Not cached, since the failure may be transient (e.g. a partial write).
		log.Printf("enrich: %s parse %s: %v", s.name, bestPath, err)
		return nil
	}

	// Cache the outcome, including a nil ("no usable data") result, so an
	// unchanged file is not re-parsed on every request.
	e.mu.Lock()
	e.cache[key] = enr
	e.mu.Unlock()
	return enr
}
