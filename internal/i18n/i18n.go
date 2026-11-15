// Package i18n holds the two presentation languages.
//
// The CLI, the help text and the errors are English only (this is a published tool).
// The generated report has a language setting, defaulting to English.
package i18n

import "strings"

// Lang is a presentation language.
type Lang string

// Supported languages.
const (
	EN Lang = "en"
	JA Lang = "ja"
)

// Parse maps a configured value to a language. Anything unknown is English: a
// typo must not silently produce a half-translated report.
func Parse(value string) Lang {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ja", "jp", "ja-jp", "ja_jp":
		return JA
	default:
		return EN
	}
}

// Pick returns the English or the Japanese variant.
func (l Lang) Pick(en, ja string) string {
	if l == JA {
		return ja
	}
	return en
}

// Unmeasured is the display for a rate with no denominator. It is never "0%".
func (l Lang) Unmeasured() string {
	return l.Pick("— (not measured)", "—（未測定）")
}

// RateFormat renders a rate with its denominator.
func (l Lang) RateFormat() string {
	return l.Pick("%s (n=%d/%d)", "%s（n=%d/%d）")
}

// HistoryShort renders "not enough history".
func (l Lang) HistoryShort() string {
	return l.Pick("— (no history)", "—（履歴不足）")
}
