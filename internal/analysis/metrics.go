// Package analysis holds the only definition of every rate in this tool.
//
// Rules that the rest of the code must not re-derive:
//   - the denominator of a citation rate is the set of runs that succeeded AND
//     produced citation metadata (observability = observed)
//   - a brand-mention rate divides by successful runs (the answer text exists)
//   - n = 0 is "—（未測定）", never 0.0%
//   - groups are provider x model x stage: there is no cross-group total
//   - a group whose records do not share one measurement identity lists the fields
//     that differ (mixed_conditions): the rate is still computed, never silently
//     presented as one measurement
//   - failure and unknown are counted and reported, never divided into a rate
package analysis

import (
	"fmt"
	"sort"
	"strings"

	"github.com/routeflags/ai-citation-monitor/internal/domain"
	"github.com/routeflags/ai-citation-monitor/internal/i18n"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

// UnmeasuredMark is the language neutral marker stored in Rate.Text when there is
// no denominator. Presentation layers replace it with i18n.Lang.Unmeasured().

// Rate is a ratio that always carries its numerator and denominator.
type Rate struct {
	N     int      `json:"n"`
	D     int      `json:"d"`
	Value *float64 `json:"value"`
	Text  string   `json:"text"`
}

// UnmeasuredMark is stored in Rate.Text when the denominator is zero.
const UnmeasuredMark = "\u2014"

// NewRate builds a rate. d = 0 returns the unmeasured form with value null.
//
// Text comes from the integers, not from Value: rounding the float64 would make the
// printed percentage depend on whether the exact ratio happens to be representable
// (23/80 rounds down as a float64 and up as a ratio), so the same n and d could print
// different tenths on different machines. proofs/Rate.lean states this rule and proves
// the bounds around it; proofs/rate-vectors.csv pins the boundary cases.
func NewRate(n, d int) Rate {
	if d <= 0 {
		return Rate{N: n, D: 0, Value: nil, Text: UnmeasuredMark}
	}
	value := float64(n) / float64(d)
	return Rate{N: n, D: d, Value: &value, Text: formatTenths(Tenths(n, d))}
}

// Tenths is the percentage of n / d in tenths of a percent: "20.0%" is 200.
//
// The value is rounded to the nearest tenth, and an exact halfway value goes to the even
// tenth. n is a numerator — a count of runs or citations, so it is not negative — and d
// is positive; NewRate is the only caller and it establishes both.
func Tenths(n, d int) int {
	// The product is computed in int64 so the result does not depend on the width of
	// int: `1000*n` overflows a 32-bit int above 2.1 million (the first failure is
	// n = 2,147,484) and the report would print a negative percentage. The release
	// matrix is amd64/arm64, so this is hygiene rather than a shipped defect; on a
	// 64-bit build the product is exact for every count below MaxInt64/1000
	// (~9.2e15), and a count comes from the length of a slice. The rounding rule is
	// unchanged; proofs/Rate.lean states it over Nat, so the Go side has to stay
	// inside the width it can hold.
	n64, d64 := int64(n), int64(d)
	q, r := 1000*n64/d64, 1000*n64%d64
	switch {
	case 2*r < d64:
		return int(q)
	case 2*r > d64:
		return int(q + 1)
	case q%2 == 0:
		return int(q)
	default:
		return int(q + 1)
	}
}

// formatTenths renders tenths of a percent as the report prints it: "20.0%".
func formatTenths(tenths int) string {
	return fmt.Sprintf("%d.%d%%", tenths/10, tenths%10)
}

// Measured reports whether the rate has a denominator.
func (r Rate) Measured() bool { return r.Value != nil }

// Display renders "40.0% (n=2/5)", or "— (not measured) (n=0/0)".
func (r Rate) Display(lang i18n.Lang) string {
	if !r.Measured() {
		return fmt.Sprintf(lang.RateFormat(), lang.Unmeasured(), 0, 0)
	}
	return fmt.Sprintf(lang.RateFormat(), r.Text, r.N, r.D)
}

// Delta is the difference b - a in percentage points, or nil when either side is
// unmeasured (comparing against nothing is not a change).
//
// It is the same number DeltaText prints, so a JSON consumer and a reader of the table
// see one value: `DeltaTenths` divided by ten, the tenths of a point in the table.
func Delta(a, b Rate) *float64 {
	tenths, ok := DeltaTenths(a, b)
	if !ok {
		return nil
	}
	delta := float64(tenths) / 10
	return &delta
}

// DeltaTenths is the difference b - a in tenths of a percentage point: "+61.3pt" is 613.
//
// It is exactly `Tenths(b) - Tenths(a)`, the two numbers the table prints, so a reader who
// subtracts the rows gets the same answer as the delta column. Rounding the exact
// difference instead would disagree with the printed rates in either direction.
// ok is false when either side is unmeasured.
func DeltaTenths(a, b Rate) (int, bool) {
	if a.Value == nil || b.Value == nil {
		return 0, false
	}
	return Tenths(b.N, b.D) - Tenths(a.N, a.D), true
}

// DeltaText renders a delta in percentage points: "+61.3pt", or the unmeasured mark.
func DeltaText(a, b Rate, lang i18n.Lang) string {
	tenths, ok := DeltaTenths(a, b)
	if !ok {
		return lang.Unmeasured()
	}
	sign := "+"
	if tenths < 0 {
		sign = "-"
		tenths = -tenths
	}
	return fmt.Sprintf("%s%d.%dpt", sign, tenths/10, tenths%10)
}

// QuestionRate is one question inside a group.
type QuestionRate struct {
	QuestionKey  string `json:"question_key"`
	QuestionText string `json:"question_text"`
	Category     string `json:"category"`
	Rate         Rate   `json:"rate"`
}

// Coverage is the AEO question coverage of a group.
type Coverage struct {
	Rate                 Rate     `json:"rate"`
	CoveredQuestions     []string `json:"covered_questions"`
	UnmeasuredQuestions  []string `json:"unmeasured_questions"`
	UnknownOnlyQuestions []string `json:"unknown_only_questions"`
	Denominator          string   `json:"denominator"`
}

// DomainShare is one row of the citation share table.
type DomainShare struct {
	Domain         string `json:"domain"`
	Classification string `json:"classification"`
	Count          int    `json:"count"`
	Share          Rate   `json:"share"`
}

// DomainRate is the competitor citation rate of one domain.
type DomainRate struct {
	Domain         string `json:"domain"`
	Classification string `json:"classification"`
	Registered     bool   `json:"registered"`
	Runs           int    `json:"runs"`
	Rate           Rate   `json:"rate"`
}

// Group is one provider x model x stage measurement. Rates never cross groups.
type Group struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Stage         string `json:"stage"`
	RunsTotal     int    `json:"runs_total"`
	RunsSuccess   int    `json:"runs_success"`
	RunsError     int    `json:"runs_error"`
	RunsCancelled int    `json:"runs_cancelled"`
	RunsUnknown   int    `json:"runs_unknown"`

	CitedOwn        Rate           `json:"cited_own"`
	BrandMentioned  Rate           `json:"brand_mentioned"`
	PerQuestion     []QuestionRate `json:"per_question"`
	Coverage        Coverage       `json:"coverage"`
	DomainShare     []DomainShare  `json:"domain_share"`
	CompetitorRate  []DomainRate   `json:"competitor_rate"`
	CitationTotal   int            `json:"citation_total"`
	UnknownMentions int            `json:"unknown_mentions"`

	// MixedConditions names the measurement conditions that are not the same inside
	// this group, e.g. "tool_version: 0.1.0+norm3, 0.2.0+norm4". Empty means the
	// group is one measurement. The rates are still computed — a group is provider
	// x model x stage by design — so this list is what keeps a mix from being
	// silent: the report prints it and a consumer can refuse to read the rate.
	MixedConditions []string `json:"mixed_conditions"`
}

// MeasurementIdentity is what has to be equal before two rates can be read as the
// same measurement: the API surface, the code (and matching rules) that produced
// the record, and the question set it was measured with.
//
// Compare refuses a delta when two sides differ; Build reports a group that mixes
// them. Both use this type and the same field names, so the vocabulary cannot
// drift between the two.
type MeasurementIdentity struct {
	EndpointVersion    string `json:"api_endpoint_version"`
	ToolVersion        string `json:"tool_version"`
	QuestionSetVersion string `json:"question_set_version"`
	QuestionSetSHA256  string `json:"question_set_sha256"`
}

// IdentityOf reads the measurement identity a run recorded.
func IdentityOf(run sqlite.Run) MeasurementIdentity {
	return MeasurementIdentity{
		EndpointVersion:    run.APIEndpointVersion,
		ToolVersion:        run.ToolVersion,
		QuestionSetVersion: run.QuestionSetVersion,
		QuestionSetSHA256:  run.QuestionSetSHA256,
	}
}

// identityFields is the fixed order in which the fields are compared and
// reported, so two runs of the same data print the same line.
var identityFields = []struct {
	label string
	pick  func(MeasurementIdentity) string
}{
	{"api_endpoint_version", func(id MeasurementIdentity) string { return id.EndpointVersion }},
	{"tool_version", func(id MeasurementIdentity) string { return id.ToolVersion }},
	{"question_set_version", func(id MeasurementIdentity) string { return id.QuestionSetVersion }},
	{"question_set_sha256", func(id MeasurementIdentity) string { return id.QuestionSetSHA256 }},
}

// MixedConditions lists the identity fields whose values differ inside one group.
//
// An empty recorded value counts as a value of its own: a run made without a
// question set was not measured with the set the other runs name. It is printed as
// "—" so the line never looks like a separator.
func MixedConditions(identities []MeasurementIdentity) []string {
	mixed := make([]string, 0, len(identityFields))
	for _, field := range identityFields {
		values := map[string]bool{}
		for _, identity := range identities {
			values[field.pick(identity)] = true
		}
		if len(values) < 2 {
			continue
		}
		list := make([]string, 0, len(values))
		for value := range values {
			list = append(list, conditionValue(value))
		}
		sort.Strings(list)
		mixed = append(mixed, field.label+": "+strings.Join(list, ", "))
	}
	return mixed
}

// conditionValue renders an empty recorded condition in a mixed-condition line.
func conditionValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

// Label is the group identity used in tables and file names.
func (g Group) Label() string {
	return g.Provider + "/" + g.Model + "/" + g.Stage
}

// Options are the report-time rules: which domain is ours, who are competitors,
// and which questions exist (for the "unmeasured" count).
type Options struct {
	OwnDomain   string
	Subdomains  bool
	Competitors []string
	Roster      []string
}

func groupKey(run sqlite.Run) string {
	return run.Provider + "\x00" + run.Model + "\x00" + run.Stage
}

// Judgeable reports whether a run can be divided into a citation rate: it must
// have succeeded and the provider must have returned citation metadata.
func Judgeable(run sqlite.Run) bool {
	return run.Status == sqliteStatusSuccess && run.CitationObservability == observabilityObserved
}

const (
	sqliteStatusSuccess   = "success"
	observabilityObserved = "observed"
)

// Build groups runs and citations. It never aggregates across groups.
func Build(runs []sqlite.Run, citations []sqlite.CitationRow, opts Options) []Group {
	type acc struct {
		runs       []sqlite.Run
		identities []MeasurementIdentity
	}
	buckets := map[string]*acc{}
	order := []string{}
	for _, run := range runs {
		key := groupKey(run)
		if _, ok := buckets[key]; !ok {
			buckets[key] = &acc{}
			order = append(order, key)
		}
		buckets[key].runs = append(buckets[key].runs, run)
		buckets[key].identities = append(buckets[key].identities, IdentityOf(run))
	}
	byRun := map[int64][]sqlite.CitationRow{}
	for _, citation := range citations {
		byRun[citation.RunID] = append(byRun[citation.RunID], citation)
	}

	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	groups := make([]Group, 0, len(order))
	for _, key := range order {
		bucket := buckets[key]
		first := bucket.runs[0]
		group := Group{Provider: first.Provider, Model: first.Model, Stage: first.Stage}
		// A group is provider x model x stage by design (invariant 2), so records of
		// different measurement conditions can land in it. The rates stay as they are;
		// this list is what stops the mix from being invisible (D24).
		group.MixedConditions = MixedConditions(bucket.identities)

		judgedRuns := make([]sqlite.Run, 0, len(bucket.runs))
		groupCitations := make([]sqlite.CitationRow, 0, 16)
		perQuestion := map[string][]sqlite.Run{}
		questionOrder := []string{}
		questionMeta := map[string]sqlite.Run{}
		unknownQuestions := map[string]bool{}
		for _, run := range bucket.runs {
			group.RunsTotal++
			switch run.Status {
			case sqliteStatusSuccess:
				group.RunsSuccess++
			case "error":
				group.RunsError++
			case "cancelled":
				group.RunsCancelled++
			}
			if run.CitationObservability != observabilityObserved {
				// unknown is status-independent: a failed call has no observed metadata either,
				// so one run can be counted here and in RunsError. Only a successful unknown
				// answer is an "unknown question" for coverage (docs/metrics.md).
				group.RunsUnknown++
				if run.Status == sqliteStatusSuccess {
					// A failed run is a failure, not an "unknown answer".
					unknownQuestions[run.QuestionKey] = true
				}
			}
			if !Judgeable(run) {
				continue
			}
			judgedRuns = append(judgedRuns, run)
			groupCitations = append(groupCitations, byRun[run.ID]...)
			if _, seen := perQuestion[run.QuestionKey]; !seen {
				questionOrder = append(questionOrder, run.QuestionKey)
				questionMeta[run.QuestionKey] = run
			}
			perQuestion[run.QuestionKey] = append(perQuestion[run.QuestionKey], run)
		}

		cited := 0
		for _, run := range judgedRuns {
			if run.CitedOwn {
				cited++
			}
		}
		group.CitedOwn = NewRate(cited, len(judgedRuns))

		branded := 0
		successRuns := 0
		for _, run := range bucket.runs {
			if run.Status != sqliteStatusSuccess {
				continue
			}
			successRuns++
			if run.BrandMentioned {
				branded++
			}
		}
		group.BrandMentioned = NewRate(branded, successRuns)

		sort.Strings(questionOrder)
		group.PerQuestion = make([]QuestionRate, 0, len(questionOrder))
		for _, key := range questionOrder {
			questionRuns := perQuestion[key]
			hits := 0
			for _, run := range questionRuns {
				if run.CitedOwn {
					hits++
				}
			}
			meta := questionMeta[key]
			group.PerQuestion = append(group.PerQuestion, QuestionRate{
				QuestionKey:  key,
				QuestionText: meta.QuestionText,
				Category:     meta.Category,
				Rate:         NewRate(hits, len(questionRuns)),
			})
		}

		group.Coverage = buildCoverage(judgedRuns, unknownQuestions, perQuestion, opts.Roster)
		group.DomainShare, group.CompetitorRate, group.CitationTotal = buildDomains(groupCitations, judgedRuns, opts)
		groups = append(groups, group)
	}
	return groups
}

func buildCoverage(judgedRuns []sqlite.Run, unknownOnly map[string]bool, perQuestion map[string][]sqlite.Run, roster []string) Coverage {
	covered := map[string]bool{}
	judged := map[string]bool{}
	for _, run := range judgedRuns {
		judged[run.QuestionKey] = true
		if run.CitedOwn {
			covered[run.QuestionKey] = true
		}
	}
	coveredList := make([]string, 0, len(covered))
	for key := range covered {
		coveredList = append(coveredList, key)
	}
	sort.Strings(coveredList)

	unknownList := make([]string, 0, len(unknownOnly))
	for key := range unknownOnly {
		if !judged[key] {
			unknownList = append(unknownList, key)
		}
	}
	sort.Strings(unknownList)

	unmeasured := make([]string, 0, len(roster))
	for _, key := range roster {
		if !judged[key] && !unknownOnly[key] {
			unmeasured = append(unmeasured, key)
		}
	}
	sort.Strings(unmeasured)

	denominator := "questions with at least one citation-judgeable answer"
	if len(judged) == 0 {
		return Coverage{
			Rate:                 NewRate(0, 0),
			CoveredQuestions:     coveredList,
			UnmeasuredQuestions:  unmeasured,
			UnknownOnlyQuestions: unknownList,
			Denominator:          denominator,
		}
	}
	return Coverage{
		Rate:                 NewRate(len(coveredList), len(judged)),
		CoveredQuestions:     coveredList,
		UnmeasuredQuestions:  unmeasured,
		UnknownOnlyQuestions: unknownList,
		Denominator:          denominator,
	}
}

func buildDomains(citations []sqlite.CitationRow, judgedRuns []sqlite.Run, opts Options) ([]DomainShare, []DomainRate, int) {
	shareCounts := map[string]int{}
	for _, citation := range citations {
		name := "(no host)"
		if citation.Hostname != nil && *citation.Hostname != "" {
			name = *citation.Hostname
		}
		shareCounts[name]++
	}
	total := len(citations)
	names := make([]string, 0, len(shareCounts))
	for name := range shareCounts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if shareCounts[names[i]] != shareCounts[names[j]] {
			return shareCounts[names[i]] > shareCounts[names[j]]
		}
		return names[i] < names[j]
	})
	shares := make([]DomainShare, 0, len(names))
	for _, name := range names {
		var classification string
		if name != "(no host)" {
			classification = domain.ClassifyDomain(name, opts.OwnDomain, opts.Subdomains, opts.Competitors)
		} else {
			classification = "unknown"
		}
		shares = append(shares, DomainShare{
			Domain:         name,
			Classification: classification,
			Count:          shareCounts[name],
			Share:          NewRate(shareCounts[name], total),
		})
	}

	perRunDomains := map[int64]map[string]bool{}
	aggregated := map[string]bool{}
	for _, citation := range citations {
		if citation.Hostname == nil || *citation.Hostname == "" {
			continue
		}
		// The rate is counted per site, while the share table above stays per host:
		// registering example.com has to count www.example.com and blog.example.com
		// (D23), and the key a citation falls under is the same key the report shows.
		key := domain.AggregationDomain(*citation.Hostname, opts.OwnDomain, opts.Subdomains, opts.Competitors)
		if key == "" {
			continue
		}
		if perRunDomains[citation.RunID] == nil {
			perRunDomains[citation.RunID] = map[string]bool{}
		}
		perRunDomains[citation.RunID][key] = true
		aggregated[key] = true
	}

	registered := map[string]bool{}
	for _, competitor := range opts.Competitors {
		key := domain.AggregationDomain(competitor, opts.OwnDomain, opts.Subdomains, opts.Competitors)
		if key == "" {
			continue
		}
		// A domain that is classified as ours is not a competitor row, even when the same
		// name also appears in the competitor list (D23 §7). The classification column
		// decides how the row is read, so `registered` must not contradict it.
		if domain.ClassifyDomain(key, opts.OwnDomain, opts.Subdomains, nil) == "own" {
			continue
		}
		registered[key] = true
		aggregated[key] = true
	}

	rates := make([]DomainRate, 0, len(aggregated))
	for name := range aggregated {
		hits := 0
		for _, run := range judgedRuns {
			if perRunDomains[run.ID][name] {
				hits++
			}
		}
		rates = append(rates, DomainRate{
			Domain:         name,
			Classification: domain.ClassifyDomain(name, opts.OwnDomain, opts.Subdomains, opts.Competitors),
			Registered:     registered[name],
			Runs:           hits,
			Rate:           NewRate(hits, len(judgedRuns)),
		})
	}
	sort.Slice(rates, func(i, j int) bool {
		if rates[i].Runs != rates[j].Runs {
			return rates[i].Runs > rates[j].Runs
		}
		return rates[i].Domain < rates[j].Domain
	})
	return shares, rates, total
}
