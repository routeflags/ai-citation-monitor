// Package costs turns provider usage into a cost estimate and enforces a budget.
//
// An estimate is only produced when a price is configured for the provider/model
// pair. Otherwise the status is `unknown`, which is not zero: an unpriced model
// must not look free.
package costs

import (
	"fmt"
	"sort"
	"strings"

	"github.com/routeflags/ai-citation-monitor/internal/config"
)

// Status values for a cost figure.
const (
	StatusEstimated = "estimated"
	StatusUnknown   = "unknown"
)

// Estimator answers "what would this call cost?" and "what did it cost?".
type Estimator struct {
	Prices   map[string]config.Price
	Currency string
}

// New builds an estimator from the configuration price table.
func New(prices map[string]config.Price, currency string) *Estimator {
	if currency == "" {
		currency = "USD"
	}
	return &Estimator{Prices: prices, Currency: currency}
}

func key(provider, model string) string {
	return strings.ToLower(strings.TrimSpace(provider)) + "/" + strings.TrimSpace(model)
}

// Price returns the configured price for one provider/model.
func (e *Estimator) Price(provider, model string) (config.Price, bool) {
	if e == nil || e.Prices == nil {
		return config.Price{}, false
	}
	price, ok := e.Prices[key(provider, model)]
	return price, ok
}

// CallEstimate is the cost of one planned call. known is false when the price is
// missing, or when the configured per-call token assumption is missing.
func (e *Estimator) CallEstimate(provider, model string) (usd float64, known bool, basis string) {
	price, ok := e.Price(provider, model)
	if !ok {
		return 0, false, fmt.Sprintf("no price for %s (cost is unknown, not zero)", key(provider, model))
	}
	if price.InputTokensPerCall <= 0 && price.OutputTokensPerCall <= 0 {
		return 0, false, fmt.Sprintf("price for %s has no per-call token assumption (cost is unknown)", key(provider, model))
	}
	input := float64(price.InputTokensPerCall) / 1_000_000 * price.InputPerMTok
	output := float64(price.OutputTokensPerCall) / 1_000_000 * price.OutputPerMTok
	return input + output, true, fmt.Sprintf(
		"%s: %d input tokens x %g /Mtok + %d output tokens x %g /Mtok",
		key(provider, model), price.InputTokensPerCall, price.InputPerMTok,
		price.OutputTokensPerCall, price.OutputPerMTok,
	)
}

// RunCost prices an actual call from the usage numbers the provider reported.
func (e *Estimator) RunCost(provider, model string, inputTokens, outputTokens *int) (usd float64, known bool, basis string) {
	price, ok := e.Price(provider, model)
	if !ok {
		return 0, false, fmt.Sprintf("no price for %s", key(provider, model))
	}
	if inputTokens == nil && outputTokens == nil {
		return 0, false, "provider reported no token usage"
	}
	input := 0
	if inputTokens != nil {
		input = *inputTokens
	}
	output := 0
	if outputTokens != nil {
		output = *outputTokens
	}
	total := float64(input)/1_000_000*price.InputPerMTok + float64(output)/1_000_000*price.OutputPerMTok
	return total, true, fmt.Sprintf("%s: %d input tokens, %d output tokens", key(provider, model), input, output)
}

// Table renders the configured prices (used by `aicom config show`-style output).
func (e *Estimator) Table() []string {
	if e == nil || len(e.Prices) == 0 {
		return nil
	}
	keys := make([]string, 0, len(e.Prices))
	for name := range e.Prices {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, name := range keys {
		price := e.Prices[name]
		lines = append(lines, fmt.Sprintf("%s: input %g/Mtok, output %g/Mtok", name, price.InputPerMTok, price.OutputPerMTok))
	}
	return lines
}

// Remaining is the budget left, or -1 when no budget is set.
func Remaining(maxUSD, spent float64) float64 {
	if maxUSD <= 0 {
		return -1
	}
	return maxUSD - spent
}

// Allows reports whether spending `estimated` more stays inside the budget.
// An unknown estimate never blocks on its own: the tool cannot prove it fits or
// that it does not, so it says so and leaves the decision to --yes.
func Allows(maxUSD, spent, estimated float64, known bool) bool {
	if maxUSD <= 0 {
		return true
	}
	if !known {
		return true
	}
	return spent+estimated <= maxUSD
}
