package analysis

import (
	"math/big"
	"reflect"
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

// The permanent exhaustive range is every n <= d for d <= 3000: 4,504,500 pairs (D25).
//
// big.Rat over the same range costs about 0.8s on a laptop, which buys nothing that
// this second integer route does not: midpointTenths divides once, then compares the
// exact ratio with the halfway point between the two candidate tenths, so it never
// repeats Tenths' remainder test. The whole range takes about 0.01s, which is why it
// runs in `go test ./...` and not in a separate nightly job.
func TestTenthsMatchesMidpointOracleOverFullRange(t *testing.T) {
	checked := 0
	for d := 1; d <= 3000; d++ {
		for n := 0; n <= d; n++ {
			checked++
			if got, want := Tenths(n, d), midpointTenths(n, d); got != want {
				t.Fatalf("Tenths(%d, %d) = %d, want %d (independent integer oracle)", n, d, got, want)
			}
		}
	}
	// A narrowed range must fail rather than pass on fewer pairs: d <= 300 is
	// exactly what the permanent check used to be.
	if checked != 4_504_500 {
		t.Fatalf("checked %d pairs, want 4504500 for d <= 3000", checked)
	}
}

// The oracle has to state the same rule, not merely the same shape: every exact
// halfway value up to 3000 (4,600 of them, the inputs where the old float64 version
// printed the other tenth for 553) is compared with the exact rational arithmetic.
// Together with TestTenthsMatchesExactRounding (d <= 300) this pins the fast oracle to
// the rule that proofs/Rate.lean proves.
func TestMidpointOracleMatchesExactRounding(t *testing.T) {
	checked := 0
	for d := 1; d <= 3000; d++ {
		for n := 0; n <= d; n++ {
			if !isExactHalfway(n, d) {
				continue
			}
			checked++
			if got, want := midpointTenths(n, d), exactTenths(n, d); got != want {
				t.Fatalf("midpointTenths(%d, %d) = %d, want %d (exact rational)", n, d, got, want)
			}
		}
	}
	if checked != 4600 {
		t.Fatalf("checked %d halfway values, want 4600: the scan is not looking at d <= 3000", checked)
	}
}

// isExactHalfway reports whether 1000*n/d sits exactly halfway between two tenths,
// which is where the tie-break decides the printed digit.
func isExactHalfway(n, d int) bool {
	n64, d64 := int64(n), int64(d)
	twice := 2000 * n64
	return twice%d64 == 0 && (twice/d64)%2 == 1
}

// midpointTenths is an independent oracle for Tenths: the lower tenth by division,
// then one exact comparison with the midpoint between it and the next tenth.
//
// It exists only to check Tenths: Tenths computes the remainder and compares 2*r with
// d, while this compares 2000*n with (2*lower+1)*d. A mistake in either comparison
// (or in the tie-break) makes the two disagree, so the exhaustive test is not the
// implementation checked against itself.
func midpointTenths(n, d int) int {
	n64, d64 := int64(n), int64(d)
	lower := 1000 * n64 / d64
	switch below, halfway := 2000*n64, (2*lower+1)*d64; {
	case below < halfway:
		return int(lower)
	case below > halfway:
		return int(lower + 1)
	case lower%2 == 0:
		return int(lower)
	default:
		return int(lower + 1)
	}
}

// `1000 * n` was computed in int, which overflows above 2,147,484 on a 32-bit
// build and would print a negative percentage. No 32-bit binary is released
// (.goreleaser.yaml ships amd64/arm64), so this is hygiene rather than a shipped
// defect: the counts below fit both widths, and on 64-bit the test passes with
// either implementation, so it guards the rule rather than the width.
func TestTenthsDoesNotOverflowOnLargeCounts(t *testing.T) {
	for _, c := range []struct {
		n, d int
		want int
	}{
		{3_000_000, 3_000_000, 1000},
		{2_500_000, 3_000_000, 833},
		{2_147_483_647, 2_147_483_647, 1000},
	} {
		if got := Tenths(c.n, c.d); got != c.want {
			t.Errorf("Tenths(%d, %d) = %d, want %d", c.n, c.d, got, c.want)
		}
	}
	if got := NewRate(2_500_000, 3_000_000).Text; got != "83.3%" {
		t.Errorf("NewRate(2500000, 3000000).Text = %q, want 83.3%%", got)
	}
}

// Issue #10 P1-1: a registered competitor has to count the subdomains its
// classification already counts, or the rate prints 0% for a site the report calls
// a competitor. The share table stays per host (D23).
func TestCompetitorRateCountsSubdomainsOfARegisteredDomain(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(2, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(3, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(4, "openai", "m", "smoke", "Q02", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(5, "openai", "m", "smoke", "Q02", false, "observed", "success", "2026-11-12T01:00:00Z"),
	}
	citations := []sqlite.CitationRow{
		{RunID: 1, Position: 1, Hostname: host("www.example.com")},
		{RunID: 1, Position: 2, Hostname: host("example.com")},
		{RunID: 2, Position: 1, Hostname: host("blog.example.com")},
		{RunID: 3, Position: 1, Hostname: host("localhost")},
		{RunID: 4, Position: 1, Hostname: nil},
		{RunID: 5, Position: 1, Hostname: host("notexample.com")},
	}
	group := Build(runs, citations, Options{
		OwnDomain: "example.shop", Subdomains: true, Competitors: []string{"example.com"},
	})[0]

	rows := map[string]DomainRate{}
	for _, row := range group.CompetitorRate {
		rows[row.Domain] = row
	}
	example, ok := rows["example.com"]
	if !ok {
		t.Fatalf("no example.com row: %+v", group.CompetitorRate)
	}
	// Run 1 cited the apex and www., run 2 cited blog.: three citations, two runs.
	if example.Runs != 2 {
		t.Errorf("example.com runs = %d, want 2 (one per run, subdomains included)", example.Runs)
	}
	if got := example.Rate.Display(i18n.EN); got != "40.0% (n=2/5)" {
		t.Errorf("example.com rate = %q, want 40.0%% of the group's judgeable runs", got)
	}
	if example.Classification != "competitor" || !example.Registered {
		t.Errorf("example.com row = %+v, want a registered competitor", example)
	}
	// The classification of a row agrees with the classification of its hosts.
	if _, ok := rows["www.example.com"]; ok {
		t.Error("the rate table must not keep a separate www.example.com row")
	}
	if len(rows) != 3 {
		t.Errorf("rate rows = %d (%v), want example.com, localhost and notexample.com", len(rows), rows)
	}

	// The share table is the per-host view of the same citations.
	shareByHost := map[string]DomainShare{}
	for _, row := range group.DomainShare {
		shareByHost[row.Domain] = row
	}
	for _, fakeHost := range []string{"www.example.com", "example.com", "blog.example.com", "localhost", "(no host)"} {
		if _, ok := shareByHost[fakeHost]; !ok {
			t.Errorf("the share table is missing the host %q: %+v", fakeHost, group.DomainShare)
		}
	}
	if shareByHost["blog.example.com"].Classification != "competitor" {
		t.Errorf("share classification = %q, want competitor", shareByHost["blog.example.com"].Classification)
	}
	if group.CitationTotal != 6 {
		t.Errorf("citation total = %d, want 6 (two from run 1, one each from the rest)", group.CitationTotal)
	}
}

// With subdomains off the unit is the exact host, so the rate table must not roll
// a host up into its registrable domain.
func TestCompetitorRateKeepsExactHostsWithoutSubdomains(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(2, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
	}
	citations := []sqlite.CitationRow{
		{RunID: 1, Position: 1, Hostname: host("www.example.com")},
		{RunID: 2, Position: 1, Hostname: host("example.com")},
	}
	group := Build(runs, citations, Options{
		OwnDomain: "example.shop", Subdomains: false, Competitors: []string{"example.com"},
	})[0]
	rows := map[string]DomainRate{}
	for _, row := range group.CompetitorRate {
		rows[row.Domain] = row
	}
	if rows["example.com"].Runs != 1 {
		t.Errorf("example.com runs = %d, want 1 (exact match only)", rows["example.com"].Runs)
	}
	www, ok := rows["www.example.com"]
	if !ok {
		t.Fatalf("a host with subdomains off must keep its own row: %+v", group.CompetitorRate)
	}
	if www.Classification != "other" || www.Registered {
		t.Errorf("www.example.com row = %+v, want an unregistered other", www)
	}
}

// A registered competitor that was never cited is a 0% rate over a real
// denominator, not "not measured".
func TestRegisteredButUncitedCompetitorKeepsTheDenominator(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
	}
	group := Build(runs, nil, Options{
		OwnDomain: "example.shop", Subdomains: true, Competitors: []string{"unused.example"},
	})[0]
	if len(group.CompetitorRate) != 1 {
		t.Fatalf("rows = %+v, want the registered competitor", group.CompetitorRate)
	}
	row := group.CompetitorRate[0]
	if row.Runs != 0 || row.Rate.D != 1 || row.Rate.Text != "0.0%" {
		t.Errorf("row = %+v, want 0.0%% (n=0/1), not an unmeasured rate", row)
	}
}

// A competitor registered deeper than its registrable domain stays its own row and
// does not pull in its registrable siblings (D23). The row's classification comes
// from the same key, so it cannot disagree with the host it stands for.
func TestDeeperCompetitorRegistrationDoesNotOverMatch(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(2, "openai", "m", "smoke", "Q02", false, "observed", "success", "2026-11-12T01:00:00Z"),
	}
	citations := []sqlite.CitationRow{
		{RunID: 1, Position: 1, Hostname: host("blog.example.com")},
		{RunID: 2, Position: 1, Hostname: host("shop.example.com")},
	}
	group := Build(runs, citations, Options{
		OwnDomain: "example.shop", Subdomains: true, Competitors: []string{"blog.example.com"},
	})[0]
	rows := map[string]DomainRate{}
	for _, row := range group.CompetitorRate {
		rows[row.Domain] = row
	}
	blog, ok := rows["blog.example.com"]
	if !ok || blog.Runs != 1 || blog.Classification != "competitor" {
		t.Fatalf("blog.example.com row = %+v (ok=%v), want the registered competitor", blog, ok)
	}
	sibling, ok := rows["example.com"]
	if !ok {
		t.Fatalf("no example.com row for the registrable sibling: %+v", group.CompetitorRate)
	}
	if sibling.Runs != 1 || sibling.Classification != "other" || sibling.Registered {
		t.Errorf("example.com row = %+v, want the shop.example.com citation only", sibling)
	}
	if len(rows) != 2 {
		t.Errorf("rate rows = %d, want 2 (no roll-up of the registrable domain)", len(rows))
	}
}

// A domain that is ours is not a competitor row even when the same name is also in
// the competitor list: `registered` must not contradict `classification` (D23 §7).
func TestOwnDomainRegisteredAsCompetitorStaysOwn(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
	}
	citations := []sqlite.CitationRow{
		{RunID: 1, Position: 1, Hostname: host("www.rival.example")},
	}
	group := Build(runs, citations, Options{
		OwnDomain: "rival.example", Subdomains: true, Competitors: []string{"rival.example"},
	})[0]
	rows := map[string]DomainRate{}
	for _, row := range group.CompetitorRate {
		rows[row.Domain] = row
	}
	row, ok := rows["rival.example"]
	if !ok {
		t.Fatalf("rows = %+v, want the own domain's row", group.CompetitorRate)
	}
	if row.Classification != "own" || row.Registered {
		t.Errorf("row = %+v, want own and not registered", row)
	}
	if row.Runs != 1 {
		t.Errorf("runs = %d, want the www. citation counted for the own site", row.Runs)
	}
}

// The registered value is canonicalised before it becomes a key: case, whitespace
// and a trailing dot used to split the row from the host it names.
func TestCompetitorRegistrationIsCanonicalised(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
	}
	citations := []sqlite.CitationRow{
		{RunID: 1, Position: 1, Hostname: host("example.com")},
	}
	group := Build(runs, citations, Options{
		OwnDomain: "example.shop", Subdomains: true, Competitors: []string{" Example.COM. "},
	})[0]
	if len(group.CompetitorRate) != 1 {
		t.Fatalf("rows = %+v, want the canonicalised competitor only", group.CompetitorRate)
	}
	row := group.CompetitorRate[0]
	if row.Domain != "example.com" || row.Runs != 1 || !row.Registered {
		t.Errorf("row = %+v, want example.com with one run", row)
	}
}

// exactTenths rounds 1000*n/d with big.Rat: the same rule, computed a different way.
func exactTenths(n, d int) int {
	r := new(big.Rat).SetFrac64(1000*int64(n), int64(d))
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
	// The reason code keeps its existing name, but the sentence does not claim the
	// earlier bucket was the unmeasured one: here it is this bucket.
	if points[2].Note != "one side has no measurement" {
		t.Errorf("note = %q, want the side-neutral wording", points[2].Note)
	}

	ja := TrendPoint{Reason: "n_differs", Detail: "2 vs 1"}.NoteFor(i18n.JA)
	if ja != "n が違う（2 vs 1）" {
		t.Errorf("ja note = %q, want the compare wording", ja)
	}
	// The wording comes from compare's helper, in both languages: the same reason
	// must not read differently in `aicm compare` and `aicm trends` (D27).
	for _, lang := range []i18n.Lang{i18n.EN, i18n.JA} {
		row := CompareRow{Reason: "n_differs", Detail: "2 vs 1"}.NoteFor(lang)
		point := TrendPoint{Reason: "n_differs", Detail: "2 vs 1"}.NoteFor(lang)
		if row != point {
			t.Errorf("%s: compare note %q != trends note %q", lang, row, point)
		}
	}
}

// A bucket that changed tool_version is not the same measurement as the bucket before
// it: the rate is still printed for that bucket (it describes that bucket's runs), but
// the change from the previous bucket is not a change in the site's visibility (D27).
// The wording is compare's, because it is the same rule.
func TestTrendsSuppressesTheDeltaAcrossConditions(t *testing.T) {
	first := run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-01T01:00:00Z")
	second := run(2, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-08T01:00:00Z")
	second.ToolVersion = "0.2.0+norm3"

	series, err := Trends([]sqlite.Run{first, second}, "weekly")
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	point := series[0].Points[1]
	if point.Reason != "conditions_differ" {
		t.Fatalf("reason = %q, want conditions_differ: %+v", point.Reason, point)
	}
	if point.Detail != "tool_version: 0.1.0+norm1 -> 0.2.0+norm3" {
		t.Errorf("detail = %q", point.Detail)
	}
	if point.CitedDelta != nil || point.BrandDelta != nil {
		t.Errorf("a delta across conditions must not be computed: %+v", point)
	}
	if point.CitedDeltaText != "— (not measured)" || point.BrandDeltaText != "— (not measured)" {
		t.Errorf("delta texts = %q / %q, want the unmeasured mark", point.CitedDeltaText, point.BrandDeltaText)
	}
	if point.CitedOwn.Text != "0.0%" || point.CitedOwn.D != 1 {
		t.Errorf("the bucket's own rate must still be printed: %+v", point.CitedOwn)
	}
	compareNote := CompareRow{Reason: "conditions_differ", Detail: point.Detail}.NoteFor(i18n.EN)
	if got := point.NoteFor(i18n.EN); got != compareNote {
		t.Errorf("trends note %q must be compare's note %q", got, compareNote)
	}
	if got := point.NoteFor(i18n.JA); got != "条件が違うため差を出さない（tool_version: 0.1.0+norm1 -> 0.2.0+norm3）" {
		t.Errorf("ja note = %q", got)
	}
}

// Only the fields compare looks at are compared between buckets: a different
// question_set_version label alone is not a mismatch (the set hash identifies the
// questions), and an empty recorded value is absent on both sides, not a change.
func TestTrendsConditionsUseTheCompareFields(t *testing.T) {
	for _, c := range []struct {
		name   string
		mutate func(*sqlite.Run)
		differ bool
	}{
		{"api_endpoint_version", func(r *sqlite.Run) { r.APIEndpointVersion = "responses/v2" }, true},
		{"question_set_sha256", func(r *sqlite.Run) { r.QuestionSetSHA256 = "set-b" }, true},
		{"tool_version", func(r *sqlite.Run) { r.ToolVersion = "0.2.0+norm3" }, true},
		{"question_set_version", func(r *sqlite.Run) { r.QuestionSetVersion = "2" }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			first := run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-01T01:00:00Z")
			second := run(2, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-08T01:00:00Z")
			c.mutate(&second)
			series, err := Trends([]sqlite.Run{first, second}, "weekly")
			if err != nil {
				t.Fatalf("Trends: %v", err)
			}
			point := series[0].Points[1]
			if got := point.Reason == "conditions_differ"; got != c.differ {
				t.Errorf("conditions differ = %t, want %t: %+v", got, c.differ, point)
			}
			if !c.differ && point.CitedDelta == nil {
				t.Errorf("a delta must be printed when the conditions agree: %+v", point)
			}
		})
	}
}

// Empty identities on both sides are "absent", exactly as compare treats them: two
// records that name no condition can still be the same measurement.
func TestTrendsTreatsEmptyConditionsAsAbsent(t *testing.T) {
	blank := func(r *sqlite.Run) {
		r.APIEndpointVersion, r.ToolVersion, r.QuestionSetVersion, r.QuestionSetSHA256 = "", "", "", ""
	}
	first := run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-01T01:00:00Z")
	second := run(2, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-08T01:00:00Z")
	blank(&first)
	blank(&second)
	series, err := Trends([]sqlite.Run{first, second}, "weekly")
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	point := series[0].Points[1]
	if point.Reason == "conditions_differ" || point.CitedDelta == nil {
		t.Errorf("an absent value on both sides is not a difference: %+v", point)
	}
}

// A bucket can hold records of two measurement conditions (a tool_version change
// inside one week). The rate is still an observation of that bucket, but the bucket
// says that it is not one measurement (D24 vocabulary), in the note too.
func TestTrendsReportsMixedConditionsInABucket(t *testing.T) {
	// 2026-11-02 is a Monday, so both runs are in one ISO week.
	first := run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-02T01:00:00Z")
	second := run(2, "openai", "m", "smoke", "Q02", false, "observed", "success", "2026-11-03T01:00:00Z")
	second.ToolVersion = "0.2.0+norm3"

	series, err := Trends([]sqlite.Run{first, second}, "weekly")
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	point := series[0].Points[0]
	if want := []string{"tool_version: 0.1.0+norm1, 0.2.0+norm3"}; !reflect.DeepEqual(point.MixedConditions, want) {
		t.Errorf("mixed_conditions = %v, want %v", point.MixedConditions, want)
	}
	if len(point.Identities) != 2 {
		t.Errorf("identities = %v, want the two recorded identities", point.Identities)
	}
	if point.CitedOwn.D != 2 || point.CitedOwn.Text != "50.0%" {
		t.Errorf("the rate is still computed over the bucket: %+v", point.CitedOwn)
	}
	// The note is the reason (here: no history) followed by the mix, in the report's
	// language, so a reader of either language sees both facts.
	if got, want := point.NoteFor(i18n.EN), "no earlier bucket to compare with; mixes measurement conditions (tool_version: 0.1.0+norm1, 0.2.0+norm3)"; got != want {
		t.Errorf("en note = %q, want %q", got, want)
	}
	if got, want := point.NoteFor(i18n.JA), "履歴不足；計測条件が混在（tool_version: 0.1.0+norm1, 0.2.0+norm3）"; got != want {
		t.Errorf("ja note = %q, want %q", got, want)
	}
}

// Two adjacent buckets that mix the same identities are still one comparable pair:
// the set of conditions is the same on both sides, so the delta is printed and both
// buckets carry mixed_conditions (the rule compare uses for a group).
func TestTrendsComparesBucketsThatMixTheSameConditions(t *testing.T) {
	week1 := run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-02T01:00:00Z")
	week1Other := run(2, "openai", "m", "smoke", "Q02", false, "observed", "success", "2026-11-02T01:00:00Z")
	week1Other.ToolVersion = "0.2.0+norm3"
	week2 := run(3, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-09T01:00:00Z")
	week2Other := run(4, "openai", "m", "smoke", "Q02", true, "observed", "success", "2026-11-09T01:00:00Z")
	week2Other.ToolVersion = "0.2.0+norm3"

	series, err := Trends([]sqlite.Run{week1, week1Other, week2, week2Other}, "weekly")
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	first, second := series[0].Points[0], series[0].Points[1]
	if len(first.MixedConditions) != 1 || len(second.MixedConditions) != 1 {
		t.Fatalf("both buckets mix the same conditions: %+v / %+v", first, second)
	}
	if second.Reason != "" || second.CitedDelta == nil {
		t.Errorf("the same set of conditions is comparable: %+v", second)
	}
	if second.CitedDeltaText != "+50.0pt" {
		t.Errorf("delta = %q, want +50.0pt", second.CitedDeltaText)
	}
}

// The identity list is distinct and ordered by the identity fields, not by the order
// the runs were read: two renders of one database have to agree.
func TestTrendIdentitiesAreDistinctAndOrdered(t *testing.T) {
	late := run(1, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-01T01:00:00Z")
	late.ToolVersion = "0.2.0+norm3"
	early := run(2, "openai", "m", "smoke", "Q02", false, "observed", "success", "2026-11-01T01:00:00Z")
	early.ToolVersion = "0.1.0+norm1"
	again := run(3, "openai", "m", "smoke", "Q03", false, "observed", "success", "2026-11-01T01:00:00Z")
	again.ToolVersion = "0.2.0+norm3"

	series, err := Trends([]sqlite.Run{late, early, again}, "weekly")
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	identities := series[0].Points[0].Identities
	if len(identities) != 2 {
		t.Fatalf("identities = %v, want 2 distinct values", identities)
	}
	if identities[0].ToolVersion != "0.1.0+norm1" || identities[1].ToolVersion != "0.2.0+norm3" {
		t.Errorf("identities must be sorted by the identity fields: %v", identities)
	}
}

// A group is provider x model x stage, so a period can put records of different
// measurement conditions in it (D24). The rate is still computed, and the group
// says which fields differ: the mix is never silent.
func TestBuildReportsMixedMeasurementConditions(t *testing.T) {
	first := run(1, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z")
	first.ToolVersion = "0.1.0+norm3"
	first.QuestionSetVersion = "1"
	first.QuestionSetSHA256 = "set-a"
	second := run(2, "openai", "m", "smoke", "Q02", false, "observed", "success", "2026-11-12T01:00:00Z")
	second.ToolVersion = "0.2.0+norm3"
	second.QuestionSetVersion = "1"
	second.QuestionSetSHA256 = "set-a"
	third := run(3, "openai", "m", "smoke", "Q03", false, "observed", "success", "2026-11-12T01:00:00Z")
	third.ToolVersion = "0.2.0+norm3"
	third.QuestionSetVersion = "2"
	third.QuestionSetSHA256 = "set-b"

	group := Build([]sqlite.Run{first, second, third}, nil, Options{})[0]
	want := []string{
		"tool_version: 0.1.0+norm3, 0.2.0+norm3",
		"question_set_version: 1, 2",
		"question_set_sha256: set-a, set-b",
	}
	if len(group.MixedConditions) != len(want) {
		t.Fatalf("mixed = %v, want %v", group.MixedConditions, want)
	}
	for index := range want {
		if group.MixedConditions[index] != want[index] {
			t.Errorf("mixed[%d] = %q, want %q", index, group.MixedConditions[index], want[index])
		}
	}
	if group.CitedOwn.D != 3 {
		t.Errorf("denominator = %d, want 3 (the rate is still computed)", group.CitedOwn.D)
	}

	// One measurement: nothing to report.
	sameA := first
	sameB := run(4, "openai", "m", "smoke", "Q04", false, "observed", "success", "2026-11-12T01:00:00Z")
	sameB.ToolVersion = first.ToolVersion
	sameB.QuestionSetVersion = first.QuestionSetVersion
	sameB.QuestionSetSHA256 = first.QuestionSetSHA256
	if got := Build([]sqlite.Run{sameA, sameB}, nil, Options{})[0].MixedConditions; len(got) != 0 {
		t.Errorf("a group of one measurement reported %v", got)
	}
	// An empty recorded condition is a condition of its own, not a missing detail.
	setless := run(9, "openai", "m", "smoke", "Q04", false, "observed", "success", "2026-11-12T01:00:00Z")
	setless.QuestionSetSHA256 = ""
	setless.ToolVersion = "0.1.0+norm3"
	withSet := run(10, "openai", "m", "smoke", "Q05", false, "observed", "success", "2026-11-12T01:00:00Z")
	withSet.QuestionSetSHA256 = "set-a"
	withSet.ToolVersion = "0.1.0+norm3"
	mixed := Build([]sqlite.Run{setless, withSet}, nil, Options{})[0].MixedConditions
	if len(mixed) != 1 || mixed[0] != "question_set_sha256: set-a, —" {
		t.Errorf("mixed = %v, want the empty set name shown as —", mixed)
	}
}

// The review's Comparability invariant: the input order must not change the
// result. Group order is sorted and every list inside a group is sorted or
// accumulated from the same set, so a reordered slice has to produce the same
// groups. questionMeta keeps the first text seen for a question key (existing
// behaviour), so the texts are identical in this fixture.
func TestBuildIsOrderInvariant(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "a", "smoke", "Q01", true, "observed", "success", "2026-11-12T01:00:00Z"),
		run(2, "openai", "a", "smoke", "Q02", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(3, "openai", "b", "smoke", "Q01", false, "unknown", "success", "2026-11-12T01:00:00Z"),
		run(4, "openai", "b", "smoke", "Q02", false, "unknown", "error", "2026-11-12T01:00:00Z"),
		run(5, "gemini", "a", "smoke", "Q01", true, "observed", "success", "2026-11-12T01:00:00Z"),
	}
	citations := []sqlite.CitationRow{
		{RunID: 1, Position: 1, Hostname: host("www.example.shop")},
		{RunID: 1, Position: 2, Hostname: host("rival.example")},
		{RunID: 2, Position: 1, Hostname: host("blog.rival.example")},
		{RunID: 5, Position: 1, Hostname: host("example.shop")},
	}
	opts := Options{OwnDomain: "example.shop", Subdomains: true, Competitors: []string{"rival.example"}}
	want := Build(runs, citations, opts)

	reorders := [][]int{
		{4, 3, 2, 1, 0},
		{2, 0, 4, 1, 3},
		{1, 2, 3, 4, 0},
	}
	for _, order := range reorders {
		shuffledRuns := make([]sqlite.Run, 0, len(runs))
		for _, index := range order {
			shuffledRuns = append(shuffledRuns, runs[index])
		}
		shuffledCitations := make([]sqlite.CitationRow, 0, len(citations))
		for index := len(citations) - 1; index >= 0; index-- {
			shuffledCitations = append(shuffledCitations, citations[index])
		}
		if got := Build(shuffledRuns, shuffledCitations, opts); !reflect.DeepEqual(got, want) {
			t.Errorf("order %v changed the result:\n got %+v\nwant %+v", order, got, want)
		}
	}
}

// The review's Aggregation invariants, as the Go tests D21 says are the right
// tool for "which set is counted": a rate never exceeds 1, the per-question
// rates add up to the group rate, the share column adds up to the citation
// total, and a failed run is never in the citation denominator.
func TestAggregationInvariants(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-12T01:00:00Z"),
		run(2, "openai", "m", "smoke", "Q01", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(3, "openai", "m", "smoke", "Q02", false, "observed", "success", "2026-11-12T01:00:00Z"),
		run(4, "openai", "m", "smoke", "Q02", false, "unknown", "success", "2026-11-12T01:00:00Z"),
		run(5, "openai", "m", "smoke", "Q03", false, "unknown", "error", "2026-11-12T01:00:00Z"),
		run(6, "openai", "m", "smoke", "Q03", false, "observed", "cancelled", "2026-11-12T01:00:00Z"),
	}
	citations := []sqlite.CitationRow{
		{RunID: 1, Position: 1, Hostname: host("example.shop")},
		{RunID: 1, Position: 2, Hostname: host("rival.example")},
		{RunID: 2, Position: 1, Hostname: nil},
		{RunID: 5, Position: 1, Hostname: host("rival.example")},
	}
	for _, group := range Build(runs, citations, Options{OwnDomain: "example.shop", Subdomains: true}) {
		for _, rate := range []Rate{group.CitedOwn, group.BrandMentioned, group.Coverage.Rate} {
			if rate.Measured() && (rate.N < 0 || rate.N > rate.D) {
				t.Errorf("%s: %+v violates 0 <= N <= D", group.Label(), rate)
			}
		}
		questionD, questionN := 0, 0
		for _, row := range group.PerQuestion {
			questionD += row.Rate.D
			questionN += row.Rate.N
		}
		if questionD != group.CitedOwn.D || questionN != group.CitedOwn.N {
			t.Errorf("%s: per-question totals n=%d/%d, group n=%d/%d", group.Label(),
				questionN, questionD, group.CitedOwn.N, group.CitedOwn.D)
		}
		shareCount := 0
		for _, row := range group.DomainShare {
			shareCount += row.Count
		}
		if shareCount != group.CitationTotal {
			t.Errorf("%s: share counts %d, citation total %d", group.Label(), shareCount, group.CitationTotal)
		}
		// Judgeable runs only: 3 successes with metadata out of 6 runs.
		if group.CitedOwn.D != 3 {
			t.Errorf("%s: citation denominator = %d, want 3 (failed and unknown runs excluded)", group.Label(), group.CitedOwn.D)
		}
	}
}

// The trend denominator is the number of judgeable runs, not `successes -
// unknown`: unknown is status-independent, so subtracting it dropped a valid
// bucket below zero and printed "not measured".
func TestTrendsCountsJudgeableRuns(t *testing.T) {
	runs := []sqlite.Run{
		run(1, "openai", "m", "smoke", "Q01", true, "observed", "success", "2026-11-01T01:00:00Z"),
		run(2, "openai", "m", "smoke", "Q02", false, "unknown", "success", "2026-11-01T01:00:00Z"),
		run(3, "openai", "m", "smoke", "Q03", false, "unknown", "error", "2026-11-01T01:00:00Z"),
	}
	series, err := Trends(runs, "weekly")
	if err != nil {
		t.Fatalf("Trends: %v", err)
	}
	if len(series) != 1 || len(series[0].Points) != 1 {
		t.Fatalf("series = %+v", series)
	}
	point := series[0].Points[0]
	if point.CitedOwn.D != 1 || point.CitedOwn.Text != "100.0%" {
		t.Errorf("cited rate = %+v, want 1/1: the one observed success is measured", point.CitedOwn)
	}
	if point.RunsValid != 2 || point.RunsError != 1 || point.RunsUnknown != 2 {
		t.Errorf("counts = valid %d, error %d, unknown %d", point.RunsValid, point.RunsError, point.RunsUnknown)
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
