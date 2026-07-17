package enrich

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSession writes a session transcript at
// <projectsDir>/<project>/<sessionID>.jsonl and returns the file path.
func writeSession(t *testing.T, projectsDir, project, sessionID, body string) string {
	t.Helper()
	dir := filepath.Join(projectsDir, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	return path
}

// claudeEnricher builds an Enricher whose only source is the Claude Code source
// rooted at a temp projects directory.
func claudeEnricher(projectsDir string) *Enricher {
	return newEnricher(claudeCodeSource(projectsDir))
}

const twoTurnSession = `{"type":"user","message":{"role":"user"}}
{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":2000,"cache_creation_input_tokens":4000,"cache_creation":{"ephemeral_5m_input_tokens":4000,"ephemeral_1h_input_tokens":0}}}}
this line is not json and must be skipped
{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":3,"output_tokens":200,"cache_read_input_tokens":5000,"cache_creation_input_tokens":0}}}
`

func TestEnrich_ClaudeCodeHappyPath(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-home-user-proj", "9f8c1234-abcd", twoTurnSession)

	e := claudeEnricher(dir)
	got := e.Enrich("9f8c1234-abcd")
	if got == nil {
		t.Fatal("Enrich returned nil, want enrichment")
	}

	if !got.Unverified {
		t.Error("Unverified = false, want true")
	}
	if got.Source != "claude-code" {
		t.Errorf("Source = %q, want claude-code", got.Source)
	}
	if got.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q, want claude-opus-4-8", got.Model)
	}

	if got.InputTokens != 1003 {
		t.Errorf("InputTokens = %d, want 1003", got.InputTokens)
	}
	if got.OutputTokens != 700 {
		t.Errorf("OutputTokens = %d, want 700", got.OutputTokens)
	}
	if got.CacheReadTokens != 7000 {
		t.Errorf("CacheReadTokens = %d, want 7000", got.CacheReadTokens)
	}
	if got.CacheCreationTokens != 4000 {
		t.Errorf("CacheCreationTokens = %d, want 4000", got.CacheCreationTokens)
	}
	if got.TotalTokens != 12703 {
		t.Errorf("TotalTokens = %d, want 12703", got.TotalTokens)
	}

	// Context fill is the last assistant turn's input context (3 + 5000 + 0).
	if got.ContextTokens != 5003 {
		t.Errorf("ContextTokens = %d, want 5003", got.ContextTokens)
	}
	if got.ContextWindow != 1_000_000 {
		t.Errorf("ContextWindow = %d, want 1000000", got.ContextWindow)
	}
	if got.ContextPct == nil {
		t.Fatal("ContextPct = nil, want a value")
	}
	if wantPct := 5003.0 / 1_000_000 * 100; math.Abs(*got.ContextPct-wantPct) > 1e-9 {
		t.Errorf("ContextPct = %v, want %v", *got.ContextPct, wantPct)
	}

	// Cost: input 1003*5e-6 + output 700*25e-6 + cacheRead 7000*5e-6*0.1
	//       + cacheWrite5m 4000*5e-6*1.25.
	wantCost := 1003*5e-6 + 700*25e-6 + 7000*5e-6*0.10 + 4000*5e-6*1.25
	if got.EstimatedCostUSD == nil {
		t.Fatal("EstimatedCostUSD = nil, want a value")
	}
	if math.Abs(*got.EstimatedCostUSD-wantCost) > 1e-9 {
		t.Errorf("EstimatedCostUSD = %v, want %v", *got.EstimatedCostUSD, wantCost)
	}
}

// A session that ends on a subagent (sidechain) turn: totals must include the
// subagent, context must come from the last MAIN turn (not the trailing
// subagent), pricing must use the main-thread model, and the subagent split
// must be broken out.
const subagentSession = `{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000,"output_tokens":100,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":2,"output_tokens":40,"cache_read_input_tokens":3000,"cache_creation_input_tokens":0}}}
{"type":"assistant","isSidechain":true,"message":{"model":"claude-haiku-4-5","usage":{"input_tokens":50,"output_tokens":30,"cache_read_input_tokens":200,"cache_creation_input_tokens":0}}}
`

func TestEnrich_SubagentBreakout(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess-subagent", subagentSession)

	got := claudeEnricher(dir).Enrich("sess-subagent")
	if got == nil {
		t.Fatal("Enrich = nil")
	}

	// Model is the main thread's, not the subagent's haiku.
	if got.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q, want claude-opus-4-8 (main-thread model, not the subagent's)", got.Model)
	}

	// Totals include the subagent turn.
	if got.InputTokens != 1052 {
		t.Errorf("InputTokens = %d, want 1052 (1000+2+50, subagent included)", got.InputTokens)
	}
	if got.TotalTokens != 4422 {
		t.Errorf("TotalTokens = %d, want 4422", got.TotalTokens)
	}

	// Context comes from the last MAIN turn (2+3000), not the trailing
	// subagent turn (50+200).
	if got.ContextTokens != 3002 {
		t.Errorf("ContextTokens = %d, want 3002 (last main turn); a subagent turn must not drive it", got.ContextTokens)
	}

	// Subagent breakout.
	if got.SubagentTurns != 1 {
		t.Errorf("SubagentTurns = %d, want 1", got.SubagentTurns)
	}
	if got.SubagentTokens != 280 {
		t.Errorf("SubagentTokens = %d, want 280 (50+30+200)", got.SubagentTokens)
	}
	if got.SubagentCostUSD == nil {
		t.Fatal("SubagentCostUSD = nil, want a value")
	}
	// The subagent ran claude-haiku-4-5, so its cost is priced at Haiku rates
	// ($1/$5 per MTok, cache-read 0.1x) — NOT the main thread's Opus rates.
	wantSub := 50*1e-6 + 30*5e-6 + 200*1e-6*0.10
	if math.Abs(*got.SubagentCostUSD-wantSub) > 1e-12 {
		t.Errorf("SubagentCostUSD = %v, want %v (subagent priced at its own Haiku model)", *got.SubagentCostUSD, wantSub)
	}

	// Total cost prices each turn at its own model: two Opus main turns plus the
	// Haiku subagent turn.
	mainCost := (1000*5e-6 + 100*25e-6) + (2*5e-6 + 40*25e-6 + 3000*5e-6*0.10)
	wantCost := mainCost + wantSub
	if got.EstimatedCostUSD == nil {
		t.Fatal("EstimatedCostUSD = nil")
	}
	if math.Abs(*got.EstimatedCostUSD-wantCost) > 1e-12 {
		t.Errorf("EstimatedCostUSD = %v, want %v (per-model pricing)", *got.EstimatedCostUSD, wantCost)
	}
}

// A known main model plus a subagent on an unmodelled model: tokens still sum,
// but neither the total nor the subagent cost can be priced (nil, not a guess).
func TestEnrich_MixedKnownUnknownModelNilCost(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess-mixed",
		`{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
{"type":"assistant","isSidechain":true,"message":{"model":"future-model-x","usage":{"input_tokens":20,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`)
	got := claudeEnricher(dir).Enrich("sess-mixed")
	if got == nil {
		t.Fatal("Enrich = nil")
	}
	if got.TotalTokens != 135 {
		t.Errorf("TotalTokens = %d, want 135 (tokens still sum across an unpriced model)", got.TotalTokens)
	}
	if got.SubagentTokens != 25 {
		t.Errorf("SubagentTokens = %d, want 25", got.SubagentTokens)
	}
	if got.EstimatedCostUSD != nil {
		t.Errorf("EstimatedCostUSD = %v, want nil when any turn's model is unpriced", *got.EstimatedCostUSD)
	}
	if got.SubagentCostUSD != nil {
		t.Errorf("SubagentCostUSD = %v, want nil for an unpriced subagent model", *got.SubagentCostUSD)
	}
}

// CostPoints samples the running total after every priced turn, in transcript
// order, so a receipt's timestamp can look up an approximate "spent so far"
// figure — this test pins down the cumulative values turn by turn.
func TestEnrich_CostPointsCumulative(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess-costpoints",
		`{"type":"assistant","timestamp":"2026-01-01T00:00:00.000Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-01-01T00:00:05.000Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":50,"output_tokens":5,"cache_read_input_tokens":1000,"cache_creation_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-01-01T00:00:10.000Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":1,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`)
	got := claudeEnricher(dir).Enrich("sess-costpoints")
	if got == nil {
		t.Fatal("Enrich = nil")
	}
	if len(got.CostPoints) != 3 {
		t.Fatalf("len(CostPoints) = %d, want 3", len(got.CostPoints))
	}

	cost1 := 100*5e-6 + 10*25e-6
	cost2 := 50*5e-6 + 5*25e-6 + 1000*5e-6*0.10
	cost3 := 10*5e-6 + 1*25e-6
	wantCumulative := []float64{cost1, cost1 + cost2, cost1 + cost2 + cost3}
	wantTimestamps := []string{
		"2026-01-01T00:00:00.000Z",
		"2026-01-01T00:00:05.000Z",
		"2026-01-01T00:00:10.000Z",
	}
	for i, pt := range got.CostPoints {
		if pt.Timestamp != wantTimestamps[i] {
			t.Errorf("CostPoints[%d].Timestamp = %q, want %q", i, pt.Timestamp, wantTimestamps[i])
		}
		if math.Abs(pt.CumulativeUSD-wantCumulative[i]) > 1e-12 {
			t.Errorf("CostPoints[%d].CumulativeUSD = %v, want %v", i, pt.CumulativeUSD, wantCumulative[i])
		}
	}

	// The curve's final point must agree with the session-wide total.
	if got.EstimatedCostUSD == nil {
		t.Fatal("EstimatedCostUSD = nil")
	}
	if math.Abs(got.CostPoints[2].CumulativeUSD-*got.EstimatedCostUSD) > 1e-12 {
		t.Errorf("final CostPoints value = %v, want it to equal EstimatedCostUSD %v", got.CostPoints[2].CumulativeUSD, *got.EstimatedCostUSD)
	}
}

// When any turn's model is unpriced, EstimatedCostUSD is nil rather than a
// partial guess (see TestEnrich_MixedKnownUnknownModelNilCost) — CostPoints
// must follow the same all-or-nothing rule, since a curve missing one turn's
// contribution would silently understate every point after it.
func TestEnrich_CostPointsNilWhenUnpriceable(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess-mixed-costpoints",
		`{"type":"assistant","timestamp":"2026-01-01T00:00:00.000Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
{"type":"assistant","timestamp":"2026-01-01T00:00:05.000Z","message":{"model":"future-model-x","usage":{"input_tokens":20,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`)
	got := claudeEnricher(dir).Enrich("sess-mixed-costpoints")
	if got == nil {
		t.Fatal("Enrich = nil")
	}
	if got.EstimatedCostUSD != nil {
		t.Fatalf("EstimatedCostUSD = %v, want nil", *got.EstimatedCostUSD)
	}
	if got.CostPoints != nil {
		t.Errorf("CostPoints = %v, want nil when the session isn't fully priceable", got.CostPoints)
	}
}

// Real Claude Code transcripts always carry a timestamp, but the parser must
// not panic or fabricate one when a line lacks it — it just can't contribute
// a usable point to the curve.
func TestEnrich_CostPointsSkipsLinesWithoutTimestamp(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess-no-timestamp",
		`{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`)
	got := claudeEnricher(dir).Enrich("sess-no-timestamp")
	if got == nil {
		t.Fatal("Enrich = nil")
	}
	if got.EstimatedCostUSD == nil {
		t.Fatal("EstimatedCostUSD = nil, want a value — cost is still tracked even without a timestamp")
	}
	if len(got.CostPoints) != 0 {
		t.Errorf("CostPoints = %v, want empty — no timestamp means no usable point", got.CostPoints)
	}
}

func TestEnrich_NoSubagentOmitsBreakout(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess-nosub",
		`{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`)
	got := claudeEnricher(dir).Enrich("sess-nosub")
	if got == nil {
		t.Fatal("Enrich = nil")
	}
	if got.SubagentTurns != 0 || got.SubagentTokens != 0 || got.SubagentCostUSD != nil {
		t.Errorf("subagent fields = {%d turns, %d tokens, cost %v}, want all zero/nil for a subagent-free session",
			got.SubagentTurns, got.SubagentTokens, got.SubagentCostUSD)
	}
}

// Claude Code writes an all-zero-usage "<synthetic>" assistant line for
// injected/placeholder messages. It must not null the cost, nor become the
// reported model or drive the context figure — even when it is the last line.
func TestEnrich_SyntheticZeroUsageLineIgnored(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess-synth",
		`{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":9000,"cache_creation_input_tokens":0}}}
{"type":"assistant","message":{"model":"<synthetic>","usage":{"input_tokens":0,"output_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`)
	got := claudeEnricher(dir).Enrich("sess-synth")
	if got == nil {
		t.Fatal("Enrich = nil")
	}
	if got.Model != "claude-opus-4-8" {
		t.Errorf("Model = %q, want claude-opus-4-8 (synthetic line must not become the model)", got.Model)
	}
	// Context comes from the real Opus turn (100+9000), not the trailing
	// zero-usage synthetic line (which would give 0).
	if got.ContextTokens != 9100 {
		t.Errorf("ContextTokens = %d, want 9100 (real turn, not the synthetic zero line)", got.ContextTokens)
	}
	// Cost is priced from the real turn; the unmodelled synthetic line does not
	// blank it out.
	if got.EstimatedCostUSD == nil {
		t.Fatal("EstimatedCostUSD = nil; the zero-usage synthetic line must not null the cost")
	}
	wantCost := 100*5e-6 + 50*25e-6 + 9000*5e-6*0.10
	if math.Abs(*got.EstimatedCostUSD-wantCost) > 1e-12 {
		t.Errorf("EstimatedCostUSD = %v, want %v", *got.EstimatedCostUSD, wantCost)
	}
}

// An implausibly large session file is skipped (returns nil) rather than read,
// bounding worst-case time and memory. Uses a sparse truncate so the test is
// cheap — no real bytes are written.
func TestEnrich_OversizeFileSkipped(t *testing.T) {
	dir := t.TempDir()
	path := writeSession(t, dir, "proj", "sess-huge",
		`{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`)
	if err := os.Truncate(path, maxSessionFileBytes+1); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if got := claudeEnricher(dir).Enrich("sess-huge"); got != nil {
		t.Errorf("Enrich = %+v, want nil for an oversize file", got)
	}
}

func TestEnrich_MissingSessionFileIsEmptyNotError(t *testing.T) {
	dir := t.TempDir()
	e := claudeEnricher(dir)
	if got := e.Enrich("does-not-exist"); got != nil {
		t.Errorf("Enrich = %+v, want nil for missing session", got)
	}
}

func TestEnrich_EmptySessionID(t *testing.T) {
	e := claudeEnricher(t.TempDir())
	if got := e.Enrich("   "); got != nil {
		t.Errorf("Enrich(blank) = %+v, want nil", got)
	}
}

func TestEnrich_NoAssistantUsageYieldsNil(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess-user-only",
		`{"type":"user","message":{"role":"user"}}
{"type":"system","subtype":"hook"}
`)
	e := claudeEnricher(dir)
	if got := e.Enrich("sess-user-only"); got != nil {
		t.Errorf("Enrich = %+v, want nil when no assistant usage present", got)
	}
}

func TestEnrich_UnknownModelHasNilCostAndPct(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess-unknown-model",
		`{"type":"assistant","message":{"model":"mystery-model-9","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`)
	e := claudeEnricher(dir)
	got := e.Enrich("sess-unknown-model")
	if got == nil {
		t.Fatal("Enrich = nil, want enrichment even for an unpriced model")
	}
	if got.Model != "mystery-model-9" {
		t.Errorf("Model = %q, want mystery-model-9", got.Model)
	}
	if got.TotalTokens != 30 {
		t.Errorf("TotalTokens = %d, want 30", got.TotalTokens)
	}
	if got.EstimatedCostUSD != nil {
		t.Errorf("EstimatedCostUSD = %v, want nil for a model not in the pricing table", *got.EstimatedCostUSD)
	}
	if got.ContextPct != nil {
		t.Errorf("ContextPct = %v, want nil for an unknown model", *got.ContextPct)
	}
	if got.ContextWindow != 0 {
		t.Errorf("ContextWindow = %d, want 0 for an unknown model", got.ContextWindow)
	}
}

func TestEnrich_MalformedLinesFailSoft(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess-malformed",
		`{"type":"assistant","message":{"model":"claude-haiku-4-5","usage":{"input_tokens":5,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
{ this is broken json
{"type":"assistant"
[]
{"type":"assistant","message":{"model":"claude-haiku-4-5","usage":{"input_tokens":7,"output_tokens":3,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`)
	e := claudeEnricher(dir)
	got := e.Enrich("sess-malformed")
	if got == nil {
		t.Fatal("Enrich = nil, want the two valid turns parsed despite malformed lines")
	}
	if got.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12 (5+7); malformed lines should be skipped", got.InputTokens)
	}
	if got.OutputTokens != 8 {
		t.Errorf("OutputTokens = %d, want 8 (5+3)", got.OutputTokens)
	}
}

func TestEnrich_CachesByPathMtimeSize(t *testing.T) {
	dir := t.TempDir()
	path := writeSession(t, dir, "proj", "sess-cache",
		`{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":100,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`)
	e := claudeEnricher(dir)

	first := e.Enrich("sess-cache")
	if first == nil {
		t.Fatal("first Enrich = nil")
	}
	second := e.Enrich("sess-cache")
	if second != first {
		t.Error("second Enrich returned a different pointer; expected a cache hit for an unchanged file")
	}

	// Rewrite with different content (different size) and a newer mtime — the
	// cache key changes, so the file must be re-parsed rather than served stale.
	if err := os.WriteFile(path,
		[]byte(`{"type":"assistant","message":{"model":"claude-opus-4-8","usage":{"input_tokens":999,"output_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}
`), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	third := e.Enrich("sess-cache")
	if third == nil {
		t.Fatal("third Enrich = nil after rewrite")
	}
	if third.InputTokens != 999 {
		t.Errorf("InputTokens = %d after rewrite, want 999 (stale cache served)", third.InputTokens)
	}
}

func TestValidSessionID(t *testing.T) {
	valid := []string{"9f8c1234-abcd", "abc_DEF-123", "0"}
	for _, s := range valid {
		if !validSessionID(s) {
			t.Errorf("validSessionID(%q) = false, want true", s)
		}
	}
	// Path traversal and glob-metacharacter payloads must be rejected so the
	// session id can never escape the projects directory.
	invalid := []string{"", "../../etc/passwd", "a/b", "abc*", "a?b", "a[b]", "back\\slash", "sp ace", "..", "uniécode"}
	for _, s := range invalid {
		if validSessionID(s) {
			t.Errorf("validSessionID(%q) = true, want false", s)
		}
	}
}

func TestEnrich_SourceRegistryOrder(t *testing.T) {
	marker := &Enrichment{Unverified: true, Source: "second"}

	empty := enricherSource{
		name: "first",
		glob: func(string) ([]string, error) { return nil, nil },
		parse: func(string, os.FileInfo) (*Enrichment, error) {
			t.Fatal("parse of an empty source should not run")
			return nil, nil
		},
	}
	// A source whose glob matches a real file, whose parse returns the marker.
	dir := t.TempDir()
	writeSession(t, dir, "proj", "sess", "{}\n")
	filler := enricherSource{
		name: "second",
		glob: func(string) ([]string, error) {
			return filepath.Glob(filepath.Join(dir, "proj", "*.jsonl"))
		},
		parse: func(string, os.FileInfo) (*Enrichment, error) { return marker, nil },
	}

	// First source yields nothing, second yields the marker: the second wins.
	e := newEnricher(empty, filler)
	if got := e.Enrich("sess"); got != marker {
		t.Errorf("Enrich = %+v, want the marker from the second source", got)
	}
}
