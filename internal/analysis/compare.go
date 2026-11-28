package analysis

import (
	"sort"
	"strconv"
	"strings"

	"github.com/routeflags/ai-citation-monitor/internal/i18n"
)

// CompareRow is one provider x model x stage compared across two periods.
type CompareRow struct {
	Provider       string   `json:"provider"`
	Model          string   `json:"model"`
	Stage          string   `json:"stage"`
	CitedA         Rate     `json:"cited_a"`
	CitedB         Rate     `json:"cited_b"`
	BrandA         Rate     `json:"brand_a"`
	BrandB         Rate     `json:"brand_b"`
	CitedDelta     *float64 `json:"cited_delta"`
	CitedDeltaText string   `json:"cited_delta_text"`
	BrandDelta     *float64 `json:"brand_delta"`
	BrandDeltaText string   `json:"brand_delta_text"`
	Comparable     bool     `json:"comparable"`
	// Reason explains a missing delta: "unmeasured", "conditions_differ" or
	// "n_differs". Detail carries the condition list or the two denominators.
	Reason string `json:"reason"`
	Detail string `json:"detail"`
	Note   string `json:"note"`
}

// NoteFor renders the explanation in the requested language.
func (r CompareRow) NoteFor(lang i18n.Lang) string {
	switch r.Reason {
	case "unmeasured":
		return lang.Pick("not measured (one side has no runs)", "未測定（片方の期間に run が無い）")
	case "conditions_differ":
		return conditionsDifferNote(r.Detail, lang)
	case "n_differs":
		return nDiffersNote(r.Detail, lang)
	default:
		return ""
	}
}

// conditionsDifferNote is the one wording for "the two sides are not the same
// measurement", used by compare (two periods) and trends (two buckets), so the
// reason code and the sentence cannot drift between the commands (D27).
func conditionsDifferNote(detail string, lang i18n.Lang) string {
	return lang.Pick("conditions differ, so no delta (", "条件が違うため差を出さない（") + detail + lang.Pick(")", "）")
}

// nDiffersNote is the one wording for "the delta survives, but the two sides are not
// the same sample size".
func nDiffersNote(detail string, lang i18n.Lang) string {
	return lang.Pick("n differs (", "n が違う（") + detail + lang.Pick(")", "）")
}

// Compare aligns two periods on the group key. It refuses to subtract when the
// two sides are not the same measurement (different endpoint version, question
// set hash or tool version) and says why instead.
func Compare(a, b *Result) []CompareRow {
	pairs := alignedGroups(a, b)
	rows := make([]CompareRow, 0, len(pairs))
	for _, pair := range pairs {
		rows = append(rows, comparePair(a, b, pair))
	}
	return rows
}

// groupPair is one group key with the side of each period that recorded it. A key
// only one period measured has one side nil.
type groupPair struct {
	left  *Group
	right *Group
}

// alignedGroups joins the two results on provider, model and stage. The pairs come
// back in key order, so the rows do not depend on the order either side was loaded
// in.
func alignedGroups(a, b *Result) []groupPair {
	byKey := map[string]*groupPair{}
	keys := []string{}
	ensure := func(group *Group) *groupPair {
		key := groupKey(group.Provider, group.Model, group.Stage)
		pair, ok := byKey[key]
		if !ok {
			pair = &groupPair{}
			byKey[key] = pair
			keys = append(keys, key)
		}
		return pair
	}
	for index := range a.Groups {
		ensure(&a.Groups[index]).left = &a.Groups[index]
	}
	for index := range b.Groups {
		ensure(&b.Groups[index]).right = &b.Groups[index]
	}
	sort.Strings(keys)
	pairs := make([]groupPair, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, *byKey[key])
	}
	return pairs
}

// comparePair fills one row: the rates always, the delta only when both sides are
// the same measurement.
func comparePair(a, b *Result, pair groupPair) CompareRow {
	row := rowFor(pair)
	if pair.left == nil || pair.right == nil {
		return noDelta(row, "unmeasured", "")
	}
	mismatch := conditionMismatch(
		groupRuns(a, row.Provider, row.Model, row.Stage),
		groupRuns(b, row.Provider, row.Model, row.Stage),
	)
	if len(mismatch) > 0 {
		return noDelta(row, "conditions_differ", strings.Join(mismatch, " / "))
	}
	return deltaRow(row)
}

// rowFor starts the row for one pair. The key comes from whichever side recorded the
// group (a pair exists only because one did), and each side fills its own rates.
func rowFor(pair groupPair) CompareRow {
	source := pair.left
	if source == nil {
		source = pair.right
	}
	row := CompareRow{Provider: source.Provider, Model: source.Model, Stage: source.Stage}
	if pair.left != nil {
		row.CitedA = pair.left.CitedOwn
		row.BrandA = pair.left.BrandMentioned
	}
	if pair.right != nil {
		row.CitedB = pair.right.CitedOwn
		row.BrandB = pair.right.BrandMentioned
	}
	return row
}

// noDelta is the row for a comparison that must not subtract: the reason says why
// ("unmeasured", "conditions_differ") and both delta texts read as unmeasured
// rather than as a change of zero.
func noDelta(row CompareRow, reason, detail string) CompareRow {
	row.Reason = reason
	row.Detail = detail
	row.Note = row.NoteFor(i18n.EN)
	row.CitedDeltaText = i18n.EN.Unmeasured()
	row.BrandDeltaText = i18n.EN.Unmeasured()
	return row
}

// deltaRow is the comparable row: both deltas are printed, and a difference in n is
// stated next to them.
func deltaRow(row CompareRow) CompareRow {
	row.Comparable = true
	row.CitedDelta = Delta(row.CitedA, row.CitedB)
	row.CitedDeltaText = DeltaText(row.CitedA, row.CitedB, i18n.EN)
	row.BrandDelta = Delta(row.BrandA, row.BrandB)
	row.BrandDeltaText = DeltaText(row.BrandA, row.BrandB, i18n.EN)
	if row.CitedA.D != row.CitedB.D {
		row.Reason = "n_differs"
		row.Detail = itoa(row.CitedA.D) + " vs " + itoa(row.CitedB.D)
		row.Note = row.NoteFor(i18n.EN)
	}
	return row
}

// groupRuns lists the measurement identities recorded for one group, which is what
// conditionMismatch compares.
func groupRuns(result *Result, provider, model, stage string) []MeasurementIdentity {
	identities := make([]MeasurementIdentity, 0, 8)
	for _, run := range result.Runs {
		if run.Provider == provider && run.Model == model && run.Stage == stage {
			identities = append(identities, IdentityOf(run))
		}
	}
	return identities
}

// conditionMismatch compares the conditions docs/metrics.md promises to check
// before subtracting two periods: the API surface, the question set hash and the
// code version. A recorded value that is empty on both sides is "absent", not a
// difference, which is why uniqueValues skips it. Build's MixedConditions uses the
// same field names plus the question-set version label, because a group that mixes
// two set labels is not one measurement.
func conditionMismatch(a, b []MeasurementIdentity) []string {
	mismatches := []string{}
	compare := func(label string, pick func(MeasurementIdentity) string) {
		left := uniqueValues(a, pick)
		right := uniqueValues(b, pick)
		if !sameValues(left, right) {
			mismatches = append(mismatches, label+": "+conditionValues(left)+" -> "+conditionValues(right))
		}
	}
	compare("api_endpoint_version", func(v MeasurementIdentity) string { return v.EndpointVersion })
	compare("question_set_sha256", func(v MeasurementIdentity) string { return v.QuestionSetSHA256 })
	compare("tool_version", func(v MeasurementIdentity) string { return v.ToolVersion })
	return mismatches
}

// sameValues compares two sorted value lists element by element. Joining them with a
// separator would make ["a,b"] and ["a", "b"] compare equal.
func sameValues(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// conditionValues renders one side of a condition mismatch. A side that recorded no
// value prints as the neutral mark, the same way MixedConditions prints an empty
// value, so "tool_version: — -> 0.2.0" cannot be read as a broken line.
func conditionValues(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	rendered := make([]string, 0, len(values))
	for _, value := range values {
		rendered = append(rendered, conditionValue(value))
	}
	return strings.Join(rendered, ", ")
}

func uniqueValues(identities []MeasurementIdentity, pick func(MeasurementIdentity) string) []string {
	set := map[string]bool{}
	for _, identity := range identities {
		value := pick(identity)
		if value == "" {
			continue
		}
		set[value] = true
	}
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
