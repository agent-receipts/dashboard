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
