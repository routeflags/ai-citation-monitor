package costs

import (
	"testing"

	"github.com/routeflags/ai-citation-monitor/internal/config"
)

func intPointer(value int) *int { return &value }

func TestCallEstimateIsUnknownWithoutAPrice(t *testing.T) {
	estimator := New(map[string]config.Price{}, "USD")
	cost, known, basis := estimator.CallEstimate("openai", "gpt-test")
	if known {
		t.Fatalf("cost = %v, want unknown when no price is configured", cost)
	}
	if basis == "" {
		t.Error("the reason must be explained")
	}
}

func TestCallEstimateNeedsATokenAssumption(t *testing.T) {
	estimator := New(map[string]config.Price{
		"openai/gpt-test": {InputPerMTok: 1, OutputPerMTok: 2},
	}, "USD")
	if _, known, _ := estimator.CallEstimate("openai", "gpt-test"); known {
		t.Fatal("a price without a per-call token assumption cannot be predicted")
	}
	estimator = New(map[string]config.Price{
		"openai/gpt-test": {InputPerMTok: 1, OutputPerMTok: 2, InputTokensPerCall: 1_000_000, OutputTokensPerCall: 500_000},
	}, "USD")
	cost, known, _ := estimator.CallEstimate("openai", "gpt-test")
	if !known {
		t.Fatal("a full price table must produce an estimate")
	}
	if cost < 1.9 || cost > 2.1 {
		t.Fatalf("cost = %v, want about 2", cost)
	}
}

func TestRunCostUsesReportedTokens(t *testing.T) {
	estimator := New(map[string]config.Price{
		"openai/gpt-test": {InputPerMTok: 1, OutputPerMTok: 2},
	}, "USD")
	cost, known, _ := estimator.RunCost("openai", "gpt-test", intPointer(1_000_000), intPointer(500_000))
	if !known || cost < 1.9 || cost > 2.1 {
		t.Fatalf("cost = %v known = %v", cost, known)
	}
	if _, known, _ := estimator.RunCost("openai", "gpt-test", nil, nil); known {
		t.Fatal("no tokens reported is unknown, not zero")
	}
}

func TestBudgetDecisions(t *testing.T) {
	if !Allows(0, 100, 100, true) {
		t.Error("no budget means no limit")
	}
	if Allows(5, 4.5, 1, true) {
		t.Error("4.5 + 1 exceeds 5")
	}
	if !Allows(5, 4.5, 1, false) {
		t.Error("an unknown estimate cannot be proven to exceed the budget; it must warn instead of blocking")
	}
	if got := Remaining(5, 4.5); got < 0.49 || got > 0.51 {
		t.Errorf("remaining = %v", got)
	}
}
