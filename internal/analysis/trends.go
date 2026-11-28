package analysis

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/routeflags/ai-citation-monitor/internal/i18n"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

// TrendPoint is one time bucket of one group.
type TrendPoint struct {
	Bucket         string   `json:"bucket"`
	RunsTotal      int      `json:"runs_total"`
	RunsValid      int      `json:"runs_valid"`
	RunsError      int      `json:"runs_error"`
	RunsUnknown    int      `json:"runs_unknown"`
	CitedOwn       Rate     `json:"cited_own"`
	BrandMentioned Rate     `json:"brand_mentioned"`
	CitedDelta     *float64 `json:"cited_delta"`
	CitedDeltaText string   `json:"cited_delta_text"`
	BrandDelta     *float64 `json:"brand_delta"`
	BrandDeltaText string   `json:"brand_delta_text"`
	// Identities are the measurement identities recorded in this bucket, distinct
	// and in a fixed order. A bucket with more than one holds records of different
	// measurement conditions; MixedConditions names the fields that differ, as a
	// Group does (D24). The delta is what needs two comparable sides; the rate and
	// its n always describe this bucket's runs.
	Identities []MeasurementIdentity `json:"identities"`
	// MixedConditions names the identity fields that are not the same inside this
	// bucket, e.g. "tool_version: 0.1.0+norm3, 0.2.0+norm4". Empty means the bucket
	// is one measurement.
	MixedConditions []string `json:"mixed_conditions"`
	// Reason explains a missing delta, or a delta that must be read with care:
	// "no_history", "earlier_unmeasured", "conditions_differ" or "n_differs" (the
	// same vocabulary as compare). Detail carries the differing conditions for
	// "conditions_differ" and the two denominators for "n_differs".
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	Note   string `json:"note"`
}

// NoteFor renders the explanation in the requested language. The wording of the two
// reasons shared with compare comes from compare's helpers, so the commands cannot
// drift apart. A bucket that mixes conditions says so as well: its rate is an average
// over them, not one measurement (D24).
func (p TrendPoint) NoteFor(lang i18n.Lang) string {
	var note string
	switch p.Reason {
	case "no_history":
		note = lang.Pick("no earlier bucket to compare with", "履歴不足")
	case "earlier_unmeasured":
		// The reason code is the existing name for a missing delta; either side can be
		// the one without a measurement, so the sentence does not claim it was the
		// earlier bucket (D27).
		note = lang.Pick("one side has no measurement", "片側が未測定")
	case "conditions_differ":
		note = conditionsDifferNote(p.Detail, lang)
	case "n_differs":
		note = nDiffersNote(p.Detail, lang)
	}
	if len(p.MixedConditions) == 0 {
		return note
	}
	mixed := lang.Pick("mixes measurement conditions (", "計測条件が混在（") +
		strings.Join(p.MixedConditions, "; ") + lang.Pick(")", "）")
	if note == "" {
		return mixed
	}
	return note + lang.Pick("; ", "；") + mixed
}

// TrendSeries is one group's time series.
type TrendSeries struct {
	Provider string       `json:"provider"`
	Model    string       `json:"model"`
	Stage    string       `json:"stage"`
	Interval string       `json:"interval"`
	Points   []TrendPoint `json:"points"`
}

// BucketLabel maps a UTC timestamp to a JST bucket label.
func BucketLabel(startedAtUTC string, interval string) (string, error) {
	parsed, err := time.Parse("2006-01-02T15:04:05Z", startedAtUTC)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, startedAtUTC)
		if err != nil {
			return "", fmt.Errorf("timestamp %q: %w", startedAtUTC, err)
		}
	}
	local := parsed.In(JST)
	switch interval {
	case "daily":
		return local.Format("2006-01-02"), nil
	case "weekly":
		year, week := local.ISOWeek()
		return fmt.Sprintf("%d-W%02d", year, week), nil
	case "monthly":
		return local.Format("2006-01"), nil
	default:
		return "", fmt.Errorf("interval %q: expected daily, weekly or monthly", interval)
	}
}

// Trends builds one series per group. The first bucket carries "履歴不足" because
// there is nothing to compare it with. A bucket whose measurement conditions differ
// from the previous one gets no delta (conditions_differ), because a change of
// tool_version or question set is not a change in the site's visibility. A bucket
// whose denominator differs keeps its delta but is marked n_differs, because a
// difference of percentage points over a different n is not the same comparison.
func Trends(runs []sqlite.Run, interval string) ([]TrendSeries, error) {
	type bucket struct {
		total, valid, errored, unknown int
		cited, branded, success        int
		judged                         int
		identities                     []MeasurementIdentity
	}
	seriesIndex := map[string]int{}
	series := []TrendSeries{}
	buckets := []map[string]*bucket{}

	for _, run := range runs {
		label, err := BucketLabel(run.StartedAtUTC, interval)
		if err != nil {
			return nil, err
		}
		key := run.Provider + "\x00" + run.Model + "\x00" + run.Stage
		index, ok := seriesIndex[key]
		if !ok {
			index = len(series)
			seriesIndex[key] = index
			series = append(series, TrendSeries{
				Provider: run.Provider, Model: run.Model, Stage: run.Stage, Interval: interval,
			})
			buckets = append(buckets, map[string]*bucket{})
		}
		if buckets[index][label] == nil {
			buckets[index][label] = &bucket{}
		}
		entry := buckets[index][label]
		entry.total++
		entry.identities = append(entry.identities, IdentityOf(run))
		switch run.Status {
		case "success":
			entry.valid++
			entry.success++
			if run.BrandMentioned {
				entry.branded++
			}
		case "error":
			entry.errored++
		case "cancelled":
			entry.errored++
		}
		if run.CitationObservability != "observed" {
			entry.unknown++
			continue
		}
		if run.Status != "success" {
			continue
		}
		// judged counts the citation-rate denominator directly. It used to be
		// `valid - unknown`, but unknown is status-independent (it includes failed
		// runs, docs/metrics.md), so a bucket with one unknown success and one
		// failure produced a negative denominator and printed "not measured".
		entry.judged++
		if run.CitedOwn {
			entry.cited++
		}
	}

	for index := range series {
		labels := make([]string, 0, len(buckets[index]))
		for label := range buckets[index] {
			labels = append(labels, label)
		}
		sort.Strings(labels)
		points := make([]TrendPoint, 0, len(labels))
		var previous *TrendPoint
		var previousIdentities []MeasurementIdentity
		for _, label := range labels {
			entry := buckets[index][label]
			point := TrendPoint{
				Bucket:          label,
				RunsTotal:       entry.total,
				RunsValid:       entry.valid,
				RunsError:       entry.errored,
				RunsUnknown:     entry.unknown,
				CitedOwn:        NewRate(entry.cited, entry.judged),
				BrandMentioned:  NewRate(entry.branded, entry.valid),
				Identities:      uniqueIdentities(entry.identities),
				MixedConditions: MixedConditions(entry.identities),
			}
			if previous == nil {
				point.Reason = "no_history"
				point.CitedDeltaText = i18n.EN.HistoryShort()
				point.BrandDeltaText = i18n.EN.HistoryShort()
			} else {
				point.CitedDelta = Delta(previous.CitedOwn, point.CitedOwn)
				point.CitedDeltaText = DeltaText(previous.CitedOwn, point.CitedOwn, i18n.EN)
				point.BrandDelta = Delta(previous.BrandMentioned, point.BrandMentioned)
				point.BrandDeltaText = DeltaText(previous.BrandMentioned, point.BrandMentioned, i18n.EN)
				mismatch := conditionMismatch(previousIdentities, entry.identities)
				switch {
				case point.CitedDelta == nil:
					// Nothing to subtract: the delta is missing regardless of conditions.
					point.Reason = "earlier_unmeasured"
				case len(mismatch) > 0:
					// The rate is still printed (unlike compare, which can hide a whole
					// row), but the change from the previous bucket is not a change in the
					// site's visibility: a tool_version or question-set change is a
					// different measurement (D27).
					point.Reason = "conditions_differ"
					point.Detail = strings.Join(mismatch, " / ")
					point.CitedDelta = nil
					point.CitedDeltaText = i18n.EN.Unmeasured()
					point.BrandDelta = nil
					point.BrandDeltaText = i18n.EN.Unmeasured()
				case previous.CitedOwn.D != point.CitedOwn.D:
					// The delta is still printed (unlike compare, which can hide a whole
					// row), but it is read next to the note: the two buckets are not the
					// same sample size.
					point.Reason = "n_differs"
					point.Detail = itoa(previous.CitedOwn.D) + " vs " + itoa(point.CitedOwn.D)
				}
			}
			point.Note = point.NoteFor(i18n.EN)
			points = append(points, point)
			previous = &points[len(points)-1]
			previousIdentities = entry.identities
		}
		series[index].Points = points
	}
	return series, nil
}

// uniqueIdentities returns the distinct measurement identities in a bucket, in the
// order identityFields lists the fields: the same two runs always produce the same
// list, so two renders of one database are byte-identical.
func uniqueIdentities(identities []MeasurementIdentity) []MeasurementIdentity {
	seen := map[MeasurementIdentity]bool{}
	unique := make([]MeasurementIdentity, 0, 1)
	for _, identity := range identities {
		if seen[identity] {
			continue
		}
		seen[identity] = true
		unique = append(unique, identity)
	}
	sort.Slice(unique, func(i, j int) bool {
		for _, field := range identityFields {
			left, right := field.pick(unique[i]), field.pick(unique[j])
			if left != right {
				return left < right
			}
		}
		return false
	})
	return unique
}
