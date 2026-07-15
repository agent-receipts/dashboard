package enrich

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
)

// sourceClaudeCode is the source name and enrichment `source` value for the
// Claude Code agent.
const sourceClaudeCode = "claude-code"

// claudeProjectsDir returns the directory Claude Code partitions its session
// transcripts under: ~/.claude/projects/<encoded-cwd>/<session-id>.jsonl.
// Returns "" when the home directory cannot be resolved, which disables the
// source (its glob returns no matches).
func claudeProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// claudeCodeSource builds the Claude Code enricher source rooted at projectsDir.
func claudeCodeSource(projectsDir string) enricherSource {
	return enricherSource{
		name: sourceClaudeCode,
		glob: func(sessionID string) ([]string, error) {
			// Claude Code stores one file per session, but partitions sessions
			// by encoded working directory. A receipt's session may have run in
			// any cwd, so search every project directory.
			if projectsDir == "" || !validSessionID(sessionID) {
				return nil, nil
			}
			return filepath.Glob(filepath.Join(projectsDir, "*", sessionID+".jsonl"))
		},
		parse: parseClaudeCodeSession,
	}
}

// validSessionID guards the trust boundary: session_id originates from receipt
// JSON in the SQLite store and is interpolated into a filesystem glob. Restrict
// it to the character set Claude Code session UUIDs use so it cannot traverse
// directories or inject glob metacharacters.
func validSessionID(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// ccRecord is the subset of a Claude Code JSONL line the enricher reads. The
// format is owned by an external tool and intentionally not contracted here:
// unknown fields are ignored, and any line that does not match this shape is
// skipped rather than treated as an error.
type ccRecord struct {
	Type string `json:"type"`
	// IsSidechain marks a subagent (Task-tool) turn. Claude Code logs subagent
	// turns into the same session file; the flag lets the parser fold their
	// tokens into the total while keeping the context-window figure to the main
	// thread.
	IsSidechain bool       `json:"isSidechain"`
	Message     *ccMessage `json:"message"`
}

type ccMessage struct {
	Model string   `json:"model"`
	Usage *ccUsage `json:"usage"`
}

type ccUsage struct {
	InputTokens              int64          `json:"input_tokens"`
	OutputTokens             int64          `json:"output_tokens"`
	CacheReadInputTokens     int64          `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64          `json:"cache_creation_input_tokens"`
	CacheCreation            *ccCacheDetail `json:"cache_creation"`
}

// ccCacheDetail is the per-TTL split of cache-creation tokens, used to price
// 5-minute vs 1-hour cache writes differently. Absent on older lines, in which
// case all cache-creation tokens are treated as 5-minute writes.
type ccCacheDetail struct {
	Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
	Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
}

// parseClaudeCodeSession reads a Claude Code session transcript and summarises
// its token usage, model, context fill, and estimated cost. It returns nil (no
// error) when the file contains no assistant usage. It fails soft on malformed
// lines: an unparseable line is skipped, never fatal.
func parseClaudeCodeSession(path string, _ os.FileInfo) (*Enrichment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	enr := &Enrichment{Unverified: true, Source: sourceClaudeCode}
	var mainModel, anyModel string
	var lastMainContext, lastAnyContext int64
	var sawUsage, sawMainUsage bool
	var subTurns int
	var subTokens int64
	// Cost is accumulated per turn at each turn's own model. priceable stays
	// true only while every counted turn had a known model; a single unmodelled
	// turn makes the corresponding cost unknown (nil), never a partial guess.
	var totalCost, subCost float64
	totalPriceable, subPriceable := true, true

	r := bufio.NewReader(f)
	for {
		// ReadBytes accumulates across the buffer boundary, so an individual
		// large line (e.g. a big tool result) does not break parsing the way a
		// fixed-size scanner token would.
		line, readErr := r.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			if rec := decodeRecord(trimmed); rec != nil && rec.Type == "assistant" && rec.Message != nil {
				msg := rec.Message
				// turnTokens gates on real usage: a zero-usage assistant line is
				// a bookkeeping placeholder (Claude Code writes an all-zero
				// "<synthetic>" line) — it carries no tokens, no real model, and
				// no context, so skipping it stops one from blanking out the
				// cost or hijacking the reported model/context.
				var turnTokens int64
				if u := msg.Usage; u != nil {
					turnTokens = u.InputTokens + u.OutputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
				}
				if turnTokens > 0 {
					u := msg.Usage
					side := rec.IsSidechain
					sawUsage = true
					if msg.Model != "" {
						anyModel = msg.Model
						if !side {
							mainModel = msg.Model
						}
					}

					// Displayed totals span every turn, subagents included.
					enr.InputTokens += u.InputTokens
					enr.OutputTokens += u.OutputTokens
					enr.CacheReadTokens += u.CacheReadInputTokens
					enr.CacheCreationTokens += u.CacheCreationInputTokens

					// Price this turn at its own model.
					var tt usageTotals
					addUsage(&tt, u)
					c, ok := costUSD(msg.Model, tt)
					if ok {
						totalCost += c
					} else {
						totalPriceable = false
					}

					turnContext := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
					lastAnyContext = turnContext
					if side {
						subTurns++
						subTokens += turnTokens
						if ok {
							subCost += c
						} else {
							subPriceable = false
						}
					} else {
						// Context fill tracks the main thread only — a subagent
						// turn's context is its own transient window, not the
						// session's.
						sawMainUsage = true
						lastMainContext = turnContext
					}
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, readErr
		}
	}

	if !sawUsage {
		return nil, nil
	}

	// The reported Model is the main thread's, falling back to any model seen
	// only if the session had no main-thread turn carrying a model. It drives
	// the context-window lookup; per-turn pricing above is independent of it.
	model := mainModel
	if model == "" {
		model = anyModel
	}
	enr.Model = model
	enr.TotalTokens = enr.InputTokens + enr.OutputTokens + enr.CacheReadTokens + enr.CacheCreationTokens

	if sawMainUsage {
		enr.ContextTokens = lastMainContext
	} else {
		enr.ContextTokens = lastAnyContext
	}
	applyContextWindow(enr, model)

	if totalPriceable {
		enr.EstimatedCostUSD = &totalCost
	}

	if subTurns > 0 {
		enr.SubagentTurns = subTurns
		enr.SubagentTokens = subTokens
		if subPriceable {
			enr.SubagentCostUSD = &subCost
		}
	}
	return enr, nil
}

// addUsage folds one turn's usage into running cost totals, splitting cache
// writes by TTL. A line without the cache_creation split is priced as a
// 5-minute write.
func addUsage(t *usageTotals, u *ccUsage) {
	t.input += u.InputTokens
	t.output += u.OutputTokens
	t.cacheRead += u.CacheReadInputTokens
	if cc := u.CacheCreation; cc != nil {
		t.cacheWrite5m += cc.Ephemeral5m
		t.cacheWrite1h += cc.Ephemeral1h
	} else {
		t.cacheWrite5m += u.CacheCreationInputTokens
	}
}

// decodeRecord unmarshals one JSONL line, returning nil on any decode error so
// the caller can skip a malformed line and keep going.
func decodeRecord(line []byte) *ccRecord {
	var rec ccRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		return nil
	}
	return &rec
}
