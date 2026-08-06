package enrich

// modelInfo holds the local, hand-maintained pricing and context-window figures
// for one model. Prices are US dollars per million tokens. These are estimates
// used only for display-labelled-unverified enrichment, not billing.
type modelInfo struct {
	inputPerMTok  float64
	outputPerMTok float64
	contextWindow int64
}

// models is the local pricing/context table keyed by model name. A model absent
// from this table produces a nil estimated cost and no context-window percentage
// — never a zero or a guess. Keep the keys aligned with the exact model
// identifiers agents write into their session files.
var models = map[string]modelInfo{
	"claude-fable-5":    {inputPerMTok: 10, outputPerMTok: 50, contextWindow: 1_000_000},
	"claude-opus-5":     {inputPerMTok: 5, outputPerMTok: 25, contextWindow: 1_000_000},
	"claude-opus-4-8":   {inputPerMTok: 5, outputPerMTok: 25, contextWindow: 1_000_000},
	"claude-opus-4-7":   {inputPerMTok: 5, outputPerMTok: 25, contextWindow: 1_000_000},
	"claude-opus-4-6":   {inputPerMTok: 5, outputPerMTok: 25, contextWindow: 1_000_000},
	"claude-sonnet-5":   {inputPerMTok: 3, outputPerMTok: 15, contextWindow: 1_000_000},
	"claude-sonnet-4-6": {inputPerMTok: 3, outputPerMTok: 15, contextWindow: 1_000_000},
	"claude-haiku-4-5":  {inputPerMTok: 1, outputPerMTok: 5, contextWindow: 200_000},
}

// Cache-token pricing multipliers relative to the base input rate. A cache read
// is billed at ~0.1x input; a 5-minute cache write at 1.25x; a 1-hour cache
// write at 2x.
const (
	cacheReadRateMult    = 0.10
	cacheWrite5mRateMult = 1.25
	cacheWrite1hRateMult = 2.0
)

// usageTotals is the token breakdown a cost estimate is computed from, summed
// across a session's turns.
type usageTotals struct {
	input        int64
	output       int64
	cacheRead    int64
	cacheWrite5m int64
	cacheWrite1h int64
}

// costUSD returns the estimated cost of the given token totals at the model's
// rates and whether the model was found. ok=false means the caller must treat
// the cost as unknown (nil) — an unmodelled turn must not be priced at zero or
// guessed. Cost is accumulated per turn so a session that mixes models (e.g. an
// Opus main thread with a Haiku subagent) prices each turn at its own rate.
func costUSD(model string, t usageTotals) (float64, bool) {
	info, ok := models[model]
	if !ok {
		return 0, false
	}
	const perMTok = 1_000_000.0
	inRate := info.inputPerMTok / perMTok
	outRate := info.outputPerMTok / perMTok

	cost := float64(t.input)*inRate +
		float64(t.output)*outRate +
		float64(t.cacheRead)*inRate*cacheReadRateMult +
		float64(t.cacheWrite5m)*inRate*cacheWrite5mRateMult +
		float64(t.cacheWrite1h)*inRate*cacheWrite1hRateMult
	return cost, true
}

// applyContextWindow fills ContextWindow and ContextPct from the local table
// when the model is known. Unknown models leave both unset (window zero,
// percentage nil).
func applyContextWindow(enr *Enrichment, model string) {
	info, ok := models[model]
	if !ok || info.contextWindow <= 0 {
		return
	}
	enr.ContextWindow = info.contextWindow
	pct := float64(enr.ContextTokens) / float64(info.contextWindow) * 100
	enr.ContextPct = &pct
}
