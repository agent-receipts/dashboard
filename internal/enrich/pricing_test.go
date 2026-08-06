package enrich

import "testing"

// TestCostUSDKnownModels locks in the pricing table entries for models
// expected to be present. A model missing from the table produces a silent
// "unavailable" cost in the dashboard UI rather than a test failure, so this
// guards against that regression for the models we explicitly support.
func TestCostUSDKnownModels(t *testing.T) {
	tt := usageTotals{input: 1_000_000, output: 1_000_000}
	for _, model := range []string{
		"claude-fable-5",
		"claude-opus-5",
		"claude-opus-4-8",
		"claude-opus-4-7",
		"claude-opus-4-6",
		"claude-sonnet-5",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
	} {
		if _, ok := costUSD(model, tt); !ok {
			t.Errorf("costUSD(%q, ...) ok = false, want true (model missing from pricing table)", model)
		}
	}
}

// TestCostUSDOpus5Rate checks claude-opus-5 is priced at Opus 4.8's rate:
// $5/MTok input, $25/MTok output.
func TestCostUSDOpus5Rate(t *testing.T) {
	tt := usageTotals{input: 1_000_000, output: 1_000_000}
	got, ok := costUSD("claude-opus-5", tt)
	if !ok {
		t.Fatalf("costUSD(claude-opus-5, ...) ok = false, want true")
	}
	want := 5.0 + 25.0
	if got != want {
		t.Errorf("costUSD(claude-opus-5, 1M in + 1M out) = %v, want %v", got, want)
	}
}

// TestCostUSDUnknownModel checks that an unmodelled model (e.g. a bare
// Claude Code alias like "sonnet", or a model newer than the local table)
// reports unknown rather than guessing a price.
func TestCostUSDUnknownModel(t *testing.T) {
	tt := usageTotals{input: 100, output: 100}
	if _, ok := costUSD("sonnet", tt); ok {
		t.Errorf("costUSD(sonnet, ...) ok = true, want false (bare alias must stay unpriced)")
	}
}
