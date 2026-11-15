package reporting

import (
	"regexp"
	"strings"
	"testing"

	"github.com/routeflags/ai-citation-monitor/internal/analysis"
	"github.com/routeflags/ai-citation-monitor/internal/config"
	"github.com/routeflags/ai-citation-monitor/internal/i18n"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

func sampleResult(t *testing.T) *analysis.Result {
	t.Helper()
	runs := []sqlite.Run{
		{
			ID: 1, Provider: "openai", Model: "gpt-test", Stage: "smoke",
			QuestionKey: "Q01", QuestionText: "THCVとは？", Category: "definition",
			Status: "success", CitationObservability: "observed", CitedOwn: true,
			BrandMentioned: true, StartedAtUTC: "2026-11-12T01:00:00Z",
			QuestionVersion: "sha:1", APIEndpointVersion: "responses/v1",
			ToolVersion: "0.1.0+norm1", MeasurementMethod: "official_api", SearchEnabled: true,
		},
	}
	citations := []sqlite.CitationRow{{
		RunID: 1, Position: 1, RawURL: "https://example.shop/a",
		Hostname: stringPointer("example.shop"), SourceType: "own", IsOwn: true,
	}}
	groups := analysis.Build(runs, citations, analysis.Options{OwnDomain: "example.shop", Subdomains: true})
	result := &analysis.Result{
		Period: analysis.Period{Label: "2026-11-12:2026-11-12", StartUTC: "2026-11-11T15:00:00Z", EndUTC: "2026-11-12T15:00:00Z"},
		Runs:   runs,
		Groups: groups,
		Conditions: analysis.Conditions{
			Providers:        []string{"openai"},
			Models:           []string{"gpt-test"},
			Stages:           []string{"smoke"},
			EndpointVersions: []string{"responses/v1"},
			ToolVersions:     []string{"0.1.0+norm1"},
		},
		Totals: analysis.Totals{RunsTotal: 1, RunsSuccess: 1, Groups: 1},
	}
	return result
}

func stringPointer(value string) *string { return &value }

func TestBuildStartsWithTheSampleStatement(t *testing.T) {
	result := sampleResult(t)
	en := Build(result, Options{Language: i18n.EN, GeneratedAt: "2026-11-15T00:00:00Z"})
	if !strings.Contains(en, SampleStatementEN) {
		t.Error("the English report must carry the sample statement")
	}
	if !strings.Contains(en, "do not represent") {
		t.Error("the sample statement must say the numbers are not the consumer UI")
	}
	ja := Build(result, Options{Language: i18n.JA, GeneratedAt: "2026-11-15T00:00:00Z"})
	if !strings.Contains(ja, SampleStatementJA) {
		t.Error("the Japanese report must carry the sample statement")
	}
	if !strings.HasPrefix(en, "# AI citation report") {
		t.Errorf("English title missing:\n%s", firstLine(en))
	}
	if !strings.HasPrefix(ja, "# AI 引用計測レポート") {
		t.Errorf("Japanese title missing:\n%s", firstLine(ja))
	}
}

func TestBuildShowsDenominatorsAndNeverAZeroForNothing(t *testing.T) {
	result := sampleResult(t)
	markdown := Build(result, Options{Language: i18n.EN, GeneratedAt: "2026-11-15T00:00:00Z"})
	if !strings.Contains(markdown, "100.0% (n=1/1)") {
		t.Errorf("a rate must carry its denominator:\n%s", markdown)
	}
	if zeroRate.MatchString(markdown) {
		t.Errorf("no measured rate is zero here, so 0.0%% must not appear:\n%s", markdown)
	}

	empty := &analysis.Result{
		Period: analysis.Period{Label: "2026-11-13:2026-11-13"},
	}
	markdown = Build(empty, Options{Language: i18n.EN, GeneratedAt: "2026-11-15T00:00:00Z"})
	if !strings.Contains(markdown, "— (not measured)") {
		t.Errorf("an empty period must say not measured:\n%s", markdown)
	}
	if zeroRate.MatchString(markdown) {
		t.Errorf("an empty period must never print 0.0%%:\n%s", markdown)
	}
	markdownJA := Build(empty, Options{Language: i18n.JA, GeneratedAt: "2026-11-15T00:00:00Z"})
	if !strings.Contains(markdownJA, "—（未測定）") {
		t.Errorf("the Japanese report must use the Japanese unmeasured form:\n%s", markdownJA)
	}
}

func TestBuildNeverPrintsACrossGroupTotal(t *testing.T) {
	result := sampleResult(t)
	runs := append([]sqlite.Run{}, result.Runs...)
	other := result.Runs[0]
	other.ID = 2
	other.Provider = "gemini"
	other.Model = "gemini-test"
	runs = append(runs, other)
	result.Runs = runs
	result.Groups = analysis.Build(runs, nil, analysis.Options{})

	markdown := Build(result, Options{Language: i18n.EN, GeneratedAt: "2026-11-15T00:00:00Z"})
	if strings.Contains(markdown, "total citation") || strings.Contains(markdown, "all providers") {
		t.Errorf("the report must not aggregate across providers:\n%s", markdown)
	}
	if !strings.Contains(markdown, "openai / gpt-test") || !strings.Contains(markdown, "gemini / gemini-test") {
		t.Errorf("both groups must appear separately:\n%s", markdown)
	}
}

func TestBuildListsTheConfiguredConditions(t *testing.T) {
	result := sampleResult(t)
	cfg := &config.Config{SiteID: "demo", OwnDomain: "example.shop", Market: "ja-JP", BrandTerms: []string{"Example"}}
	markdown := Build(result, Options{Config: cfg, Language: i18n.EN, GeneratedAt: "2026-11-15T00:00:00Z"})
	for _, want := range []string{"Conditions", "responses/v1", "question set", "no citation metadata", "does not say"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("report does not contain %q", want)
		}
	}
}

// zeroRate matches a "0.0%" that is not part of 100.0%.
var zeroRate = regexp.MustCompile(`(^|[^0-9])0\.0%`)

func firstLine(value string) string {
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return value[:index]
	}
	return value
}
