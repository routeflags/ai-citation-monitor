// Package reporting renders the Markdown report and decides its file name.
//
// The file name carries every group condition (period, provider, model, stage and
// the question set hash), so two runs of the same period with different
// conditions can never overwrite each other (the bug fixed during extraction).
package reporting

import (
	"strings"

	"github.com/routeflags/ai-citation-monitor/internal/analysis"
)

// FileName builds the report file name from the conditions actually present.
func FileName(period analysis.Period, conditions analysis.Conditions) string {
	parts := []string{"report", sanitize(period.Label)}
	parts = append(parts, sanitize(joinOrAll(conditions.Providers, "all-providers")))
	parts = append(parts, sanitize(joinOrAll(conditions.Models, "all-models")))
	parts = append(parts, sanitize(joinOrAll(conditions.Stages, "all-stages")))
	if len(conditions.QuestionSetSHA256s) > 0 {
		shortest := conditions.QuestionSetSHA256s[0]
		if len(shortest) > 12 {
			shortest = shortest[:12]
		}
		parts = append(parts, "qs-"+sanitize(shortest))
	}
	return strings.Join(parts, "-") + ".md"
}

func joinOrAll(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return fallback
	}
	return strings.Join(cleaned, "+")
}

// sanitize keeps file names portable: no path separators, no colons.
func sanitize(value string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "_", " ", "_", "..", ".",
	)
	value = replacer.Replace(value)
	if value == "" {
		return "unknown"
	}
	return value
}
