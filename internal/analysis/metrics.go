// Package analysis holds the only definition of every rate in this tool.
//
// Rules that the rest of the code must not re-derive:
//   - the denominator of a citation rate is the set of runs that succeeded AND
//     produced citation metadata (observability = observed)
//   - a brand-mention rate divides by successful runs (the answer text exists)
//   - n = 0 is "—（未測定）", never 0.0%
//   - groups are provider x model x stage: there is no cross-group total
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
func NewRate(n, d int) Rate {
	if d <= 0 {
		return Rate{N: n, D: 0, Value: nil, Text: UnmeasuredMark}
	}
	value := float64(n) / float64(d)
	return Rate{N: n, D: d, Value: &value, Text: fmt.Sprintf("%.1f%%", value*100)}
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

// Delta returns the difference in percentage points, or nil when either side is
// unmeasured (comparing against nothing is not a change).
func Delta(a, b Rate) *float64 {
	if a.Value == nil || b.Value == nil {
		return nil
	}
	delta := (*b.Value - *a.Value) * 100
	return &delta
}

// DeltaText renders a delta in percentage points.
func DeltaText(delta *float64, lang i18n.Lang) string {
	if delta == nil {
		return lang.Unmeasured()
	}
	return fmt.Sprintf("%+.1fpt", *delta)
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
		runs      []sqlite.Run
		citations []sqlite.CitationRow
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
		classification := "other"
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
	for _, citation := range citations {
		if citation.Hostname == nil || *citation.Hostname == "" {
			continue
		}
		if perRunDomains[citation.RunID] == nil {
			perRunDomains[citation.RunID] = map[string]bool{}
		}
		perRunDomains[citation.RunID][*citation.Hostname] = true
	}

	candidates := map[string]bool{}
	for name := range shareCounts {
		if name != "(no host)" {
			candidates[name] = true
		}
	}
	registered := map[string]bool{}
	for _, competitor := range opts.Competitors {
		normalized := strings.ToLower(strings.TrimSpace(competitor))
		if normalized == "" {
			continue
		}
		registered[normalized] = true
		candidates[normalized] = true
	}

	rates := make([]DomainRate, 0, len(candidates))
	for name := range candidates {
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
