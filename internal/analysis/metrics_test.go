package analysis

import (
	"math/big"
	"testing"
	"time"

	"github.com/routeflags/ai-citation-monitor/internal/i18n"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

func run(id int64, provider, model, stage, question string, cited bool, observability string, status string, startedAt string) sqlite.Run {
	return sqlite.Run{
		ID: id, Provider: provider, Model: model, Stage: stage,
		QuestionKey: question, QuestionText: question + " text", Category: "definition",
		RepeatIndex: 1, Status: status, CitationObservability: observability,
		CitedOwn: cited, BrandMentioned: cited, StartedAtUTC: startedAt,
		QuestionVersion: "sha:x", APIEndpointVersion: "responses/v1", ToolVersion: "0.1.0+norm1",
		MeasurementMethod: "official_api", SearchEnabled: true,
	}
}

func host(value string) *string { return &value }

func TestRateUnmeasuredIsNotZero(t *testing.T) {
	rate := NewRate(0, 0)
	if rate.Value != nil {
		t.Fatalf("value = %v, want nil", *rate.Value)
	}
	if rate.Text != UnmeasuredMark {
		t.Fatalf("text = %q, want the neutral mark (never 0.0%%)", rate.Text)
	}
	if got := rate.Display(i18n.EN); got != "— (not measured) (n=0/0)" {
		t.Fatalf("display = %q", got)
	}
	if got := rate.Display(i18n.JA); got != "—（未測定）（n=0/0）" {
		t.Fatalf("display (ja) = %q", got)
	}
}

func TestRateFormatsWithDenominator(t *testing.T) {
	rate := NewRate(2, 5)
	if rate.Text != "40.0%" {
		t.Fatalf("text = %q", rate.Text)
	}
	if got := rate.Display(i18n.EN); got != "40.0% (n=2/5)" {
		t.Fatalf("display = %q", got)
	}
}

// The printed percentage is rounded from the ratio, not from float64: the tenths must be
// the same on every machine. Every case below is an exact halfway value except 5/8, and
// 23/80 printed 28.7% before D22 because float64(23)/float64(80)*100 lands below 28.75.
func TestRateRoundsTheRatioNotTheFloat(t *testing.T) {
	for _, c := range []struct {
		n, d int
		want string
	}{
		{1, 16, "6.2%"},
		{3, 16, "18.8%"},
		{23, 80, "28.8%"},
		{49, 80, "61.2%"},
		{51, 80, "63.8%"},
		{1, 2000, "0.0%"},
		{5, 8, "62.5%"},
	} {
		if got := NewRate(c.n, c.d).Text; got != c.want {
			t.Errorf("NewRate(%d, %d).Text = %q, want %q", c.n, c.d, got, c.want)
		}
	}
}

// Tenths is checked against exact rational arithmetic over every numerator up to 300, so a
// change to the rounding has to hold for every input, not only for the vectors in
// proofs/rate-vectors.csv.
func TestTenthsMatchesExactRounding(t *testing.T) {
	for d := 1; d <= 300; d++ {
		for n := 0; n <= d; n++ {
			want := exactTenths(n, d)
			if got := Tenths(n, d); got != want {
				t.Fatalf("Tenths(%d, %d) = %d, want %d", n, d, got, want)
			}
		}
	}
}

// exactTenths rounds 1000*n/d with big.Rat: the same rule, computed a different way.
func exactTenths(n, d int) int {
	r := new(big.Rat).SetFrac64(int64(1000*n), int64(d))
	q := new(big.Int)
	rem := new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	k := q.Int64()
	if twice := new(big.Int).Lsh(rem, 1); twice.Cmp(r.Denom()) > 0 ||
		(twice.Cmp(r.Denom()) == 0 && k%2 != 0) {
		k++
	}
	return int(k)
}

// A delta is the difference between the two numbers the table prints, so a reader who
// subtracts the rows gets the delta column. 1/5 -> 6/32 prints 20.0% -> 18.8%, so the
// delta is -1.2pt; rounding the exact difference gave -1.3pt before D22.
func TestDeltaIsTheDifferenceOfThePrintedRates(t *testing.T) {
	for _, c := range []struct {
		an, ad, bn, bd int
		want           string
	}{
		{1, 5, 6, 32, "-1.2pt"},
		{1, 5, 13, 16, "+61.2pt"},
		{2, 5, 1, 2, "+10.0pt"},
		{1, 2, 1, 5, "-30.0pt"},
	} {
		a, b := NewRate(c.an, c.ad), NewRate(c.bn, c.bd)
		if got := DeltaText(a, b, i18n.EN); got != c.want {
			t.Errorf("DeltaText(%d/%d, %d/%d) = %q, want %q", c.an, c.ad, c.bn, c.bd, got, c.want)
		}
		wantValue := float64(Tenths(c.bn, c.bd)-Tenths(c.an, c.ad)) / 10
		if got := Delta(a, b); got == nil || *got != wantValue {
			t.Errorf("Delta(%d/%d, %d/%d) = %v, want %v", c.an, c.ad, c.bn, c.bd, got, wantValue)
		}
	}
}

func TestBuildSeparatesProvidersAndModels(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "gpt-a", "smoke", "Q01", true, "observed", "success", "2026-11-12T01:00:00Z"),
		run(2, "openai", "gpt-a", "smoke", "Q02", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(3, "openai", "gpt-b", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(4, "gemini", "gemini-a", "smoke", "Q01", true, "observed", "success", "2026-11-12T01:00:00Z"),
	}
	citations := []sqlite.CitationRow{
		{RunID: 1, Position: 1, RawURL: "https://example.shop/a", Hostname: host("example.shop"), IsOwn: true, SourceType: "own"},
		{RunID: 1, Position: 2, RawURL: "mailto:x@y", Hostname: nil, Malformed: true, SourceType: "unknown"},
		{RunID: 4, Position: 1, RawURL: "https://example.shop/b", Hostname: host("example.shop"), IsOwn: true, SourceType: "own"},
	}
	groups := Build(runs, citations, Options{OwnDomain: "example.shop", Subdomains: true})
	if len(groups) != 3 {
		t.Fatalf("groups = %d, want 3 (provider x model x stage)", len(groups))
	}
	for _, group := range groups {
		if group.Provider == "openai" && group.Model == "gpt-a" {
			if group.CitedOwn.Text != "50.0%" || group.CitedOwn.D != 2 {
				t.Errorf("gpt-a rate = %s", group.CitedOwn.Display(i18n.EN))
			}
			if group.CitationTotal != 2 {
				t.Errorf("citation total = %d, want 2 (malformed counted)", group.CitationTotal)
			}
			if len(group.DomainShare) != 2 {
				t.Errorf("domain share rows = %d, want 2 (the hostless URL stays a row)", len(group.DomainShare))
			}
			if group.DomainShare[1].Share.D != 2 {
				t.Errorf("share denominator = %d, want 2", group.DomainShare[1].Share.D)
			}
		}
	}
}

func TestUnknownObservabilityIsCountedNotDivided(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "gemini", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(2, "gemini", "m", "smoke", "Q02", false, "unknown", "success", "2026-11-12T01:00:00Z"),
		run(3, "gemini", "m", "smoke", "Q03", false, "unknown", "error", "2026-11-12T01:00:00Z"),
	}
	groups := Build(runs, nil, Options{})
	if len(groups) != 1 {
		t.Fatalf("groups = %d", len(groups))
	}
	group := groups[0]
	if group.RunsUnknown != 2 {
		t.Errorf("unknown = %d, want 2", group.RunsUnknown)
	}
	if group.CitedOwn.D != 1 {
		t.Errorf("citation denominator = %d, want 1 (only the observed run)", group.CitedOwn.D)
	}
	if group.BrandMentioned.D != 2 {
		t.Errorf("mention denominator = %d, want 2 (successful runs)", group.BrandMentioned.D)
	}
	if group.Coverage.Rate.D != 1 {
		t.Errorf("coverage denominator = %d, want 1", group.Coverage.Rate.D)
	}
	if len(group.Coverage.UnknownOnlyQuestions) != 1 {
		t.Errorf("unknown-only questions = %v", group.Coverage.UnknownOnlyQuestions)
	}
}

func TestCoverageCountsUnmeasuredQuestions(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-12T01:00:00Z"),
		run(2, "openai", "m", "smoke", "Q02", false, "observed", "success", "2026-11-12T01:00:00Z"),
	}
	groups := Build(runs, nil, Options{Roster: []string{"Q01", "Q02", "Q03"}})
	coverage := groups[0].Coverage
	if coverage.Rate.Text != "50.0%" || coverage.Rate.D != 2 {
		t.Errorf("coverage = %s, want 50.0%% of 2 judged questions", coverage.Rate.Display(i18n.EN))
	}
	if len(coverage.UnmeasuredQuestions) != 1 || coverage.UnmeasuredQuestions[0] != "Q03" {
		t.Errorf("unmeasured = %v, want [Q03]", coverage.UnmeasuredQuestions)
	}
}

func TestCompareRefusesDifferentConditions(t *testing.T) {
	base := &Result{
		Groups: Build([]sqlite.Run{run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-01T01:00:00Z")}, nil, Options{}),
		Runs:   []sqlite.Run{run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-01T01:00:00Z")},
	}
	changedRun := run(2, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-08T01:00:00Z")
	changedRun.QuestionSetSHA256 = "other"
	current := &Result{
		Groups: Build([]sqlite.Run{changedRun}, nil, Options{}),
		Runs:   []sqlite.Run{changedRun},
	}
	rows := Compare(base, current)
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].CitedDelta != nil {
		t.Error("a delta across different conditions must not be computed")
	}
	if rows[0].Note == "" {
		t.Error("the row must say why the delta is missing")
	}
}

func TestCompareComputesPercentagePoints(t *testing.T) {
	baseRuns := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-01T01:00:00Z"),
		run(2, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-01T01:00:00Z"),
	}
	currentRuns := []sqlite.Run{
		run(3, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-08T01:00:00Z"),
		run(4, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-08T01:00:00Z"),
	}
	rows := Compare(&Result{Groups: Build(baseRuns, nil, Options{}), Runs: baseRuns},
		&Result{Groups: Build(currentRuns, nil, Options{}), Runs: currentRuns})
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].CitedDeltaText != "+50.0pt" {
		t.Fatalf("delta = %q, want +50.0pt", rows[0].CitedDeltaText)
	}
	if rows[0].Comparable != true {
		t.Error("equal conditions and equal n must be comparable")
	}
}

func TestTrendsBucketsAndHistory(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-01T01:00:00Z"),
		run(2, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-08T01:00:00Z"),
	}
	series, err := Trends(runs, "weekly")
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	if len(series) != 1 || len(series[0].Points) != 2 {
		t.Fatalf("series = %+v", series)
	}
	if series[0].Points[0].Reason != "no_history" {
		t.Errorf("the first bucket must say it has no history: %+v", series[0].Points[0])
	}
	if series[0].Points[1].CitedDeltaText != "-100.0pt" {
		t.Errorf("delta = %q", series[0].Points[1].CitedDeltaText)
	}
	if _, err := Trends(runs, "hourly"); err == nil {
		t.Error("an unknown interval must fail")
	}
}

func TestTrendsNotesADifferentDenominator(t *testing.T) {
	// Bucket 1: cited 1 of 2. Bucket 2: cited 1 of 1. Bucket 3: nothing judgeable.
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-01T01:00:00Z"),
		run(2, "openai", "m", "smoke", "Q02", false, "observed", "success", "2026-11-01T01:00:00Z"),
		run(3, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-08T01:00:00Z"),
		run(4, "openai", "m", "smoke", "Q01", false, "unknown", "error", "2026-11-15T01:00:00Z"),
	}
	series, err := Trends(runs, "weekly")
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	if len(series) != 1 || len(series[0].Points) != 3 {
		t.Fatalf("series = %+v", series)
	}
	points := series[0].Points

	if points[0].Reason != "no_history" || points[0].Detail != "" {
		t.Errorf("the first bucket must only say it has no history: %+v", points[0])
	}

	// Same measure, different sample size: the delta is printed with a note that
	// uses the compare vocabulary (reason n_differs, detail the two n).
	if points[1].Reason != "n_differs" {
		t.Errorf("reason = %q, want n_differs", points[1].Reason)
	}
	if points[1].Detail != "2 vs 1" {
		t.Errorf("detail = %q, want the two denominators", points[1].Detail)
	}
	if points[1].Note != "n differs (2 vs 1)" {
		t.Errorf("note = %q", points[1].Note)
	}
	if points[1].CitedDeltaText != "+50.0pt" {
		t.Errorf("a different n must not hide the delta: %q", points[1].CitedDeltaText)
	}

	// An unmeasured bucket still suppresses the delta itself (existing rule).
	if points[2].Reason != "earlier_unmeasured" {
		t.Errorf("reason = %q, want earlier_unmeasured", points[2].Reason)
	}
	if points[2].CitedDelta != nil || points[2].Detail != "" {
		t.Errorf("an unmeasured bucket must not produce a delta or an n note: %+v", points[2])
	}

	ja := TrendPoint{Reason: "n_differs", Detail: "2 vs 1"}.NoteFor(i18n.JA)
	if ja != "n が違う（2 vs 1）" {
		t.Errorf("ja note = %q, want the compare wording", ja)
	}
}

func TestParsePeriodForms(t *testing.T) {
	now := time.Date(2026, 11, 15, 3, 0, 0, 0, time.UTC)
	period, err := ParsePeriod("7d", now)
	if err != nil {
		t.Fatalf("ParsePeriod: %v", err)
	}
	if period.StartUTC != "2026-11-08T15:00:00Z" {
		t.Fatalf("start = %s, want the JST day boundary", period.StartUTC)
	}
	if _, err := ParsePeriod("2026-11-10:2026-11-01", now); err == nil {
		t.Error("an inverted period must fail")
	}
	previous, ok := PreviousPeriod(period)
	if !ok {
		t.Fatal("PreviousPeriod refused a bounded period")
	}
	if previous.EndUTC != period.StartUTC {
		t.Errorf("the previous window must end exactly where the current one starts (got %s, want %s)", previous.EndUTC, period.StartUTC)
	}
	if _, ok := PreviousPeriod(Period{Label: "all"}); ok {
		t.Error("an unbounded period has no previous window")
	}
}
