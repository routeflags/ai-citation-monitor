package analysis

import (
	"fmt"
	"sort"
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
	// Reason explains a missing delta, or a delta that must be read with care:
	// "no_history", "earlier_unmeasured" or "n_differs" (the same vocabulary as
	// compare). Detail carries the two denominators for "n_differs".
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	Note   string `json:"note"`
}

// NoteFor renders the explanation in the requested language. The wording comes
// from compare, so the two commands cannot drift apart.
func (p TrendPoint) NoteFor(lang i18n.Lang) string {
	switch p.Reason {
	case "no_history":
		return lang.Pick("no earlier bucket to compare with", "履歴不足")
	case "earlier_unmeasured":
		return lang.Pick("the earlier bucket has no measurement", "前期間が未測定")
	case "n_differs":
		return lang.Pick("n differs (", "n が違う（") + p.Detail + lang.Pick(")", "）")
	default:
		return ""
	}
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
// there is nothing to compare it with. A bucket whose denominator differs from
// the previous one keeps its delta but is marked n_differs, because a difference
// of percentage points over a different n is not the same comparison.
func Trends(runs []sqlite.Run, interval string) ([]TrendSeries, error) {
	type bucket struct {
		total, valid, errored, unknown int
		cited, branded, success        int
		judged                         int
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
		for _, label := range labels {
			entry := buckets[index][label]
			point := TrendPoint{
				Bucket:         label,
				RunsTotal:      entry.total,
				RunsValid:      entry.valid,
				RunsError:      entry.errored,
				RunsUnknown:    entry.unknown,
				CitedOwn:       NewRate(entry.cited, entry.judged),
				BrandMentioned: NewRate(entry.branded, entry.valid),
			}
			if previous == nil {
				point.Reason = "no_history"
				point.Note = point.NoteFor(i18n.EN)
				point.CitedDeltaText = i18n.EN.HistoryShort()
				point.BrandDeltaText = i18n.EN.HistoryShort()
			} else {
				point.CitedDelta = Delta(previous.CitedOwn, point.CitedOwn)
				point.CitedDeltaText = DeltaText(previous.CitedOwn, point.CitedOwn, i18n.EN)
				point.BrandDelta = Delta(previous.BrandMentioned, point.BrandMentioned)
				point.BrandDeltaText = DeltaText(previous.BrandMentioned, point.BrandMentioned, i18n.EN)
				if point.CitedDelta == nil {
					point.Reason = "earlier_unmeasured"
					point.Note = point.NoteFor(i18n.EN)
				} else if previous.CitedOwn.D != point.CitedOwn.D {
					// The delta is still printed (unlike compare, which can hide a whole
					// row), but it is read next to the note: the two buckets are not the
					// same sample size.
					point.Reason = "n_differs"
					point.Detail = itoa(previous.CitedOwn.D) + " vs " + itoa(point.CitedOwn.D)
					point.Note = point.NoteFor(i18n.EN)
				}
			}
			points = append(points, point)
			previous = &points[len(points)-1]
		}
		series[index].Points = points
	}
	return series, nil
}
