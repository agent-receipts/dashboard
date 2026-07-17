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

	// Token counts summed across every assistant turn in the session,
	// including subagent (Task-tool) turns, which the agent logs into the same
	// session file. See SubagentTokens for the subagent portion.
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	TotalTokens         int64 `json:"total_tokens"`

	// SubagentTokens is the portion of TotalTokens attributable to subagent
	// (sidechain) turns. Subagents run in their own context windows spun up and
	// torn down within the session; their tokens still cost money, so they are
	// counted in the totals and broken out here. Omitted when the session
	// spawned no subagents.
	SubagentTokens int64 `json:"subagent_tokens,omitempty"`
	// SubagentTurns is the number of subagent assistant turns counted. Omitted
	// when zero.
	SubagentTurns int `json:"subagent_turns,omitempty"`
	// SubagentCostUSD is the estimated cost of the subagent turns alone — a
	// subset of EstimatedCostUSD — priced per subagent turn's own model. Nil
	// when any subagent turn's model is absent from the local table. Omitted
	// when the session spawned no subagents.
	SubagentCostUSD *float64 `json:"subagent_cost_usd,omitempty"`

	// ContextTokens is the input context size (input + cache-read +
	// cache-creation) of the session's most recent MAIN-THREAD turn — how full
	// the primary conversation's context window was on its last turn. Subagent
	// turns are deliberately excluded: they have their own transient context
	// windows and would misreport the session's fill.
	ContextTokens int64 `json:"context_tokens"`
	// ContextWindow is the model's context window in tokens, when the model is
	// known to the local table. Zero (omitted) otherwise.
	ContextWindow int64 `json:"context_window,omitempty"`
	// ContextPct is ContextTokens / ContextWindow * 100, when the window is
	// known. Nil when the model is not in the table.
	ContextPct *float64 `json:"context_pct,omitempty"`

	// EstimatedCostUSD is a local estimate from a pricing table keyed by model
	// name, summed per turn at each turn's own model. It is nil — never zero,
	// never a guess — when any turn's model is absent from the table.
	EstimatedCostUSD *float64 `json:"estimated_cost_usd"`

	// CostPoints is the running total spent, sampled after every priced turn in
	// transcript order. A receipt has no join key to a specific turn, so this
	// lets a caller derive an approximate "spent so far" figure for a receipt
	// from its own timestamp: the cumulative value of the last point at or
	// before that timestamp. Omitted whenever EstimatedCostUSD is nil, for the
	// same reason EstimatedCostUSD itself is nil rather than partial: a curve
	// missing an unpriceable turn's contribution would silently understate
	// every point after it.
	CostPoints []CostPoint `json:"cost_points,omitempty"`
}

// CostPoint is one sample on a session's cumulative-cost-over-time curve: the
// running total spent (across every priced turn so far, main thread and
// subagent alike) as of Timestamp.
type CostPoint struct {
	Timestamp     string  `json:"timestamp"`
	CumulativeUSD float64 `json:"cumulative_usd"`
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

// cacheEntry is a parsed result together with the file identity it was parsed
// from. The cache is keyed by path (one entry per session file), and the mtime
// and size validate that entry: an edited or rotated file overwrites its entry
// rather than accumulating a new one, so the cache stays bounded by the number
// of distinct session files seen — not by how often they change.
type cacheEntry struct {
	mtime int64
	size  int64
	enr   *Enrichment
}

// Enricher is the default SessionEnricher. It tries each registered source in
// order and returns the first non-nil enrichment. Parsed results (including
// "no data") are cached per file path and validated by mtime and size.
type Enricher struct {
	sources []enricherSource

	mu    sync.Mutex
	cache map[string]cacheEntry
}

// New returns an Enricher with the Claude Code source registered.
func New() *Enricher {
	return newEnricher(claudeCodeSource(claudeProjectsDir()))
}

// newEnricher builds an Enricher over an explicit source list. Used by tests to
// inject sources rooted at a temporary directory.
func newEnricher(sources ...enricherSource) *Enricher {
	return &Enricher{sources: sources, cache: map[string]cacheEntry{}}
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

	mtime, size := bestInfo.ModTime().UnixNano(), bestInfo.Size()

	e.mu.Lock()
	if ent, ok := e.cache[bestPath]; ok && ent.mtime == mtime && ent.size == size {
		e.mu.Unlock()
		return ent.enr
	}
	e.mu.Unlock()

	enr, err := s.parse(bestPath, bestInfo)
	if err != nil {
		// Fail soft: a parse error yields no enrichment, never a surfaced error.
		// Not cached, since the failure may be transient (e.g. a partial write).
		log.Printf("enrich: %s parse %s: %v", s.name, bestPath, err)
		return nil
	}

	// Cache the outcome (including a nil "no usable data" result) so an
	// unchanged file is not re-parsed; a later mtime/size overwrites this entry.
	e.mu.Lock()
	e.cache[bestPath] = cacheEntry{mtime: mtime, size: size, enr: enr}
	e.mu.Unlock()
	return enr
}
