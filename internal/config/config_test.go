package config

import (
	"strings"
	"testing"
)

// usableConfig is a configuration with every required setting present, so a test can
// add one competitor and see only that competitor's problem.
func usableConfig() *Config {
	return &Config{
		SiteID:      "demo",
		OwnDomain:   "example.shop",
		Market:      "ja-JP",
		Language:    "ja",
		Report:      ReportSettings{Language: "en"},
		Competitors: nil,
	}
}

// A site under a hosting suffix has its own registrable domain, and an ordinary
// domain or an IDN is not a shared suffix: none of these may be rejected.
func TestProblemsAcceptsRegistrableCompetitors(t *testing.T) {
	for _, competitors := range [][]string{
		nil,
		{"example.com"},
		{"user.github.io"},
		{"blog.example.com", "例え.テスト"},
	} {
		cfg := usableConfig()
		cfg.Competitors = competitors
		if problems := cfg.Problems(); len(problems) != 0 {
			t.Errorf("competitors %v: problems = %v, want none", competitors, problems)
		}
	}
}

// A competitor row stands for a whole site (D23), so a shared suffix would count
// every customer under it as one competitor. The config refuses it the same way
// own_domain is refused, whatever the spelling of the value.
func TestProblemsRejectsAPublicSuffixCompetitor(t *testing.T) {
	for _, competitor := range []string{"co.jp", "github.io", "com", "https://CO.JP/path"} {
		cfg := usableConfig()
		cfg.Competitors = []string{competitor}
		problems := cfg.Problems()
		if len(problems) != 1 {
			t.Fatalf("competitors [%q]: problems = %v, want one", competitor, problems)
		}
		if !strings.Contains(problems[0], "public suffix") {
			t.Errorf("competitors [%q]: the message must name the problem: %s", competitor, problems[0])
		}
		if !strings.Contains(problems[0], "register a registrable domain") {
			t.Errorf("competitors [%q]: the message must say how to fix it: %s", competitor, problems[0])
		}
	}
}

// Two spellings of the same suffix are one problem, and the message names the
// canonical value that the report would actually match.
func TestProblemsReportsEachInvalidSuffixOnce(t *testing.T) {
	cfg := usableConfig()
	cfg.Competitors = []string{"co.jp", "https://co.jp/", "github.io"}
	problems := cfg.Problems()
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want one per suffix", problems)
	}
	if !strings.Contains(problems[0], `"co.jp"`) || !strings.Contains(problems[1], `"github.io"`) {
		t.Errorf("problems must be sorted and canonical: %v", problems)
	}
	if strings.Contains(strings.Join(problems, "\n"), "https://") {
		t.Errorf("the message must name the canonical domain, not the typed value: %v", problems)
	}
}
