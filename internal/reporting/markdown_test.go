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

// A period can put records of different measurement conditions in one group. The
// rate is still printed, and the report says the average is not one measurement
// (D24); a period whose conditions agree says nothing.
func TestBuildReportsAGroupThatMixesConditions(t *testing.T) {
	base := sqlite.Run{
		Provider: "openai", Model: "gpt-test", Stage: "smoke",
		QuestionKey: "Q01", QuestionText: "THCVとは？", Category: "definition",
		Status: "success", CitationObservability: "observed", CitedOwn: true,
		StartedAtUTC: "2026-11-12T01:00:00Z", QuestionVersion: "sha:1",
		APIEndpointVersion: "responses/v1", ToolVersion: "0.1.0+norm3",
		QuestionSetVersion: "1.0.0", QuestionSetSHA256: "set-a",
		MeasurementMethod: "official_api", SearchEnabled: true,
	}
	first := base
	first.ID = 1
	second := base
	second.ID = 2
	second.ToolVersion = "0.2.0+norm3"
	runs := []sqlite.Run{first, second}
	result := &analysis.Result{
		Period: analysis.Period{Label: "2026-11-12:2026-11-12"},
		Runs:   runs,
		Groups: analysis.Build(runs, nil, analysis.Options{}),
		Conditions: analysis.Conditions{
			Providers: []string{"openai"}, Models: []string{"gpt-test"},
			ToolVersions: []string{"0.1.0+norm3", "0.2.0+norm3"},
		},
	}

	en := Build(result, Options{Language: i18n.EN, GeneratedAt: "2026-11-15T00:00:00Z"})
	want := "**openai/gpt-test/smoke** mixes measurement conditions (tool_version: 0.1.0+norm3, 0.2.0+norm3). Its rate is an average over them, not one measurement."
	if !strings.Contains(en, want) {
		t.Errorf("the English report is missing the mixed-condition line:\n%s", en)
	}

	ja := Build(result, Options{Language: i18n.JA, GeneratedAt: "2026-11-15T00:00:00Z"})
	if !strings.Contains(ja, "**openai/gpt-test/smoke** は計測条件が混在している（tool_version: 0.1.0+norm3, 0.2.0+norm3）") {
		t.Errorf("the Japanese report is missing the mixed-condition line:\n%s", ja)
	}

	clean := Build(sampleResult(t), Options{Language: i18n.EN, GeneratedAt: "2026-11-15T00:00:00Z"})
	if strings.Contains(clean, "mixes measurement conditions") {
		t.Error("a period of one measurement must not warn about a mix")
	}
}

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
	for _, want := range []string{"Conditions", "responses/v1", "question set", "citation metadata not observed (unknown)", "does not say"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("report does not contain %q", want)
		}
	}
	// Status and observability are separate dimensions, and the report says so.
	if !strings.Contains(markdown, "appears in both `failure` and `unknown`") {
		t.Error("the observed note must say a failed run is counted in both dimensions")
	}
}

// A mention rate rests on the chosen terms, so an unconfirmed term list is stated in
// the conditions (the fact, read from the records) and next to the rate (what it means
// for the number). A confirmed or undeclared list gets no caution: the rate means what
// it always meant.
func TestBuildShowsTheRecordedBrandTermsStatus(t *testing.T) {
	cases := []struct {
		name      string
		statuses  []string
		enRow     string
		jaRow     string
		enCaution bool
		jaCaution bool
	}{
		{
			name:      "proposed",
			statuses:  []string{analysis.BrandTermsStatusProposed},
			enRow:     "| brand terms status (question set) | proposed (not confirmed) |",
			jaRow:     "| ブランド語の状態（質問セット） | 未確定（proposed） |",
			enCaution: true,
			jaCaution: true,
		},
		{
			name:     "confirmed",
			statuses: []string{analysis.BrandTermsStatusConfirmed},
			enRow:    "| brand terms status (question set) | confirmed |",
			jaRow:    "| ブランド語の状態（質問セット） | 確定済み |",
		},
		{
			name:     "not declared is never shown as confirmed",
			statuses: []string{analysis.BrandTermsNotDeclared},
			enRow:    "| brand terms status (question set) | not declared |",
			jaRow:    "| ブランド語の状態（質問セット） | 未宣言 |",
		},
		{
			name:      "a mixed period lists both and keeps the caution",
			statuses:  []string{analysis.BrandTermsStatusProposed, analysis.BrandTermsStatusConfirmed},
			enRow:     "| brand terms status (question set) | proposed (not confirmed), confirmed |",
			jaRow:     "| ブランド語の状態（質問セット） | 未確定（proposed）, 確定済み |",
			enCaution: true,
			jaCaution: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := sampleResult(t)
			result.Conditions.BrandTermsStatuses = tc.statuses
			en := Build(result, Options{Language: i18n.EN, GeneratedAt: "2026-11-15T00:00:00Z"})
			ja := Build(result, Options{Language: i18n.JA, GeneratedAt: "2026-11-15T00:00:00Z"})
			if !strings.Contains(en, tc.enRow) {
				t.Errorf("English report is missing %q:\n%s", tc.enRow, en)
			}
			if !strings.Contains(ja, tc.jaRow) {
				t.Errorf("Japanese report is missing %q:\n%s", tc.jaRow, ja)
			}
			// The status line must say where it came from, so a reader does not take it
			// for a property of the configured brand terms.
			if !strings.Contains(en, "The status is what the imported question set declared") {
				t.Errorf("the status line must explain its provenance:\n%s", en)
			}
			if got := strings.Contains(en, "not confirmed yet"); got != tc.enCaution {
				t.Errorf("English caution shown = %v, want %v:\n%s", got, tc.enCaution, en)
			}
			if got := strings.Contains(ja, "ブランド語はまだ確定していない"); got != tc.jaCaution {
				t.Errorf("Japanese caution shown = %v, want %v:\n%s", got, tc.jaCaution, ja)
			}
		})
	}
}

// A period with no runs has no recorded status, so the report must not print one: the
// conditions table keeps saying "not measured" and nothing claims a term list at all.
func TestBuildOmitsTheBrandTermsStatusWhenThePeriodHasNoRuns(t *testing.T) {
	empty := &analysis.Result{Period: analysis.Period{Label: "2026-11-13:2026-11-13"}}
	for _, lang := range []i18n.Lang{i18n.EN, i18n.JA} {
		markdown := Build(empty, Options{Language: lang, GeneratedAt: "2026-11-15T00:00:00Z"})
		if strings.Contains(markdown, "brand terms status") || strings.Contains(markdown, "ブランド語の状態") {
			t.Errorf("an empty period must not print a brand terms status (%s):\n%s", lang, markdown)
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
