package analysis

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

// JST is the reporting timezone. Records are stored in UTC; period boundaries and
// trend buckets are JST days so a late-night run does not fall into another week
// on the calendar the reader uses.
var JST = time.FixedZone("JST", 9*60*60)

// Period is an inclusive JST date range expressed as a UTC half-open range.
type Period struct {
	Label    string `json:"label"`
	StartUTC string `json:"start_utc"`
	EndUTC   string `json:"end_utc"`
}

// ParsePeriod accepts "30d", "90d", "2026-11-02:2026-11-08", "2026-11-12" or
// "all". An empty spec means the last 30 days.
func ParsePeriod(spec string, now time.Time) (Period, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		spec = "30d"
	}
	if strings.EqualFold(spec, "all") {
		return Period{Label: "all"}, nil
	}

	today := now.In(JST)
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, JST)

	if strings.HasSuffix(strings.ToLower(spec), "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(strings.ToLower(spec), "d"))
		if err != nil || days <= 0 {
			return Period{}, fmt.Errorf("period %q: expected a day count like 30d", spec)
		}
		start := today.AddDate(0, 0, -(days - 1))
		return periodFromDates(start, today), nil
	}

	if strings.Contains(spec, ":") {
		parts := strings.SplitN(spec, ":", 2)
		start, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(parts[0]), JST)
		if err != nil {
			return Period{}, fmt.Errorf("period %q: start date must be YYYY-MM-DD", spec)
		}
		end, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(parts[1]), JST)
		if err != nil {
			return Period{}, fmt.Errorf("period %q: end date must be YYYY-MM-DD", spec)
		}
		if end.Before(start) {
			return Period{}, fmt.Errorf("period %q: end is before start", spec)
		}
		return periodFromDates(start, end), nil
	}

	day, err := time.ParseInLocation("2006-01-02", spec, JST)
	if err != nil {
		return Period{}, fmt.Errorf("period %q: expected 30d, 2026-11-02:2026-11-08 or 2026-11-12", spec)
	}
	return periodFromDates(day, day), nil
}

func periodFromDates(start, end time.Time) Period {
	startUTC := start.UTC()
	endUTC := end.AddDate(0, 0, 1).UTC()
	return Period{
		Label:    start.Format("2006-01-02") + ":" + end.Format("2006-01-02"),
		StartUTC: startUTC.Format("2006-01-02T15:04:05Z"),
		EndUTC:   endUTC.Format("2006-01-02T15:04:05Z"),
	}
}

// PreviousPeriod returns the equally long window immediately before p. It is
// false for an unbounded period ("all"), which has nothing before it.
func PreviousPeriod(p Period) (Period, bool) {
	if p.StartUTC == "" || p.EndUTC == "" || p.Label == "all" {
		return Period{}, false
	}
	parts := strings.SplitN(p.Label, ":", 2)
	if len(parts) != 2 {
		return Period{}, false
	}
	start, err := time.ParseInLocation("2006-01-02", parts[0], JST)
	if err != nil {
		return Period{}, false
	}
	end, err := time.ParseInLocation("2006-01-02", parts[1], JST)
	if err != nil {
		return Period{}, false
	}
	days := int(end.Sub(start).Hours()/24) + 1
	previousEnd := start.AddDate(0, 0, -1)
	previousStart := previousEnd.AddDate(0, 0, -(days - 1))
	return periodFromDates(previousStart, previousEnd), true
}

// Conditions are the measurement conditions actually present in the records.
type Conditions struct {
	Providers           []string `json:"providers"`
	Models              []string `json:"models"`
	Stages              []string `json:"stages"`
	EndpointVersions    []string `json:"endpoint_versions"`
	ToolVersions        []string `json:"tool_versions"`
	QuestionSetVersions []string `json:"question_set_versions"`
	QuestionSetSHA256s  []string `json:"question_set_sha256s"`
	MeasurementMethods  []string `json:"measurement_methods"`
	SearchEnabled       bool     `json:"search_enabled"`
	ObservedFirst       string   `json:"observed_first"`
	ObservedLast        string   `json:"observed_last"`
}

// Totals are the counts behind every rate in the period.
type Totals struct {
	RunsTotal     int `json:"runs_total"`
	RunsSuccess   int `json:"runs_success"`
	RunsError     int `json:"runs_error"`
	RunsCancelled int `json:"runs_cancelled"`
	RunsUnknown   int `json:"runs_unknown"`
	Groups        int `json:"groups"`
}

// Result is one period loaded from the database and grouped.
type Result struct {
	Period     Period               `json:"period"`
	Runs       []sqlite.Run         `json:"-"`
	Citations  []sqlite.CitationRow `json:"-"`
	Groups     []Group              `json:"groups"`
	Conditions Conditions           `json:"conditions"`
	Totals     Totals               `json:"totals"`
}

// LoadPeriod reads a period and groups it. The filter's provider/model/stage
// narrowing is applied in SQL; the period bounds always come from Period.
func LoadPeriod(ctx context.Context, db *sqlite.DB, period Period, filter sqlite.RunFilter, opts Options) (*Result, error) {
	filter.StartUTC = period.StartUTC
	filter.EndUTC = period.EndUTC
	runs, err := db.Runs(ctx, filter)
	if err != nil {
		return nil, err
	}
	runIDs := make([]int64, 0, len(runs))
	for _, run := range runs {
		runIDs = append(runIDs, run.ID)
	}
	citations, err := db.Citations(ctx, runIDs)
	if err != nil {
		return nil, err
	}

	result := &Result{
		Period:    period,
		Runs:      runs,
		Citations: citations,
		Groups:    Build(runs, citations, opts),
	}
	result.Conditions = buildConditions(runs)
	result.Totals = buildTotals(runs)
	result.Totals.Groups = len(result.Groups)
	return result, nil
}

func buildTotals(runs []sqlite.Run) Totals {
	totals := Totals{RunsTotal: len(runs)}
	for _, run := range runs {
		switch run.Status {
		case "success":
			totals.RunsSuccess++
		case "error":
			totals.RunsError++
		case "cancelled":
			totals.RunsCancelled++
		}
		if run.CitationObservability != "observed" {
			totals.RunsUnknown++
		}
	}
	return totals
}

func buildConditions(runs []sqlite.Run) Conditions {
	providers := map[string]bool{}
	models := map[string]bool{}
	stages := map[string]bool{}
	endpoints := map[string]bool{}
	tools := map[string]bool{}
	sets := map[string]bool{}
	shas := map[string]bool{}
	methods := map[string]bool{}
	searchEnabled := true
	first, last := "", ""
	for _, run := range runs {
		providers[run.Provider] = true
		models[run.Model] = true
		if run.Stage != "" {
			stages[run.Stage] = true
		}
		endpoints[run.APIEndpointVersion] = true
		tools[run.ToolVersion] = true
		if run.QuestionSetVersion != "" {
			sets[run.QuestionSetVersion] = true
		}
		if run.QuestionSetSHA256 != "" {
			shas[run.QuestionSetSHA256] = true
		}
		methods[run.MeasurementMethod] = true
		if !run.SearchEnabled {
			searchEnabled = false
		}
		if first == "" || run.StartedAtUTC < first {
			first = run.StartedAtUTC
		}
		if run.StartedAtUTC > last {
			last = run.StartedAtUTC
		}
	}
	return Conditions{
		Providers:           sortedKeys(providers),
		Models:              sortedKeys(models),
		Stages:              sortedKeys(stages),
		EndpointVersions:    sortedKeys(endpoints),
		ToolVersions:        sortedKeys(tools),
		QuestionSetVersions: sortedKeys(sets),
		QuestionSetSHA256s:  sortedKeys(shas),
		MeasurementMethods:  sortedKeys(methods),
		SearchEnabled:       searchEnabled && len(runs) > 0,
		ObservedFirst:       first,
		ObservedLast:        last,
	}
}

func sortedKeys(set map[string]bool) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		if strings.TrimSpace(value) == "" {
			continue
		}
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}
