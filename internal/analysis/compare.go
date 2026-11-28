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
		return lang.Pick("conditions differ, so no delta (", "条件が違うため差を出さない（") + r.Detail + lang.Pick(")", "）")
	case "n_differs":
		return lang.Pick("n differs (", "n が違う（") + r.Detail + lang.Pick(")", "）")
	default:
		return ""
	}
}

// Compare aligns two periods on the group key. It refuses to subtract when the
// two sides are not the same measurement (different endpoint version, question
// set hash or tool version) and says why instead.
func Compare(a, b *Result) []CompareRow {
	type pair struct {
		left  *Group
		right *Group
	}
	keys := map[string]*pair{}
	order := []string{}
	add := func(groups []Group, left bool) {
		for index := range groups {
			group := &groups[index]
			key := group.Provider + "\x00" + group.Model + "\x00" + group.Stage
			if _, ok := keys[key]; !ok {
				keys[key] = &pair{}
				order = append(order, key)
			}
			if left {
				keys[key].left = group
			} else {
				keys[key].right = group
			}
		}
	}
	add(a.Groups, true)
	add(b.Groups, false)
	sort.Strings(order)

	groupRuns := func(result *Result, provider, model, stage string) []MeasurementIdentity {
		identities := make([]MeasurementIdentity, 0, 8)
		for _, run := range result.Runs {
			if run.Provider == provider && run.Model == model && run.Stage == stage {
				identities = append(identities, IdentityOf(run))
			}
		}
		return identities
	}

	rows := make([]CompareRow, 0, len(order))
	for _, key := range order {
		part := keys[key]
		left := part.left
		right := part.right
		row := CompareRow{}
		if left != nil {
			row.Provider, row.Model, row.Stage = left.Provider, left.Model, left.Stage
			row.CitedA = left.CitedOwn
			row.BrandA = left.BrandMentioned
		} else if right != nil {
			row.Provider, row.Model, row.Stage = right.Provider, right.Model, right.Stage
		}
		if right != nil {
			row.CitedB = right.CitedOwn
			row.BrandB = right.BrandMentioned
		}
		if left == nil || right == nil {
			row.Reason = "unmeasured"
			row.Note = row.NoteFor(i18n.EN)
			row.CitedDeltaText = i18n.EN.Unmeasured()
			row.BrandDeltaText = i18n.EN.Unmeasured()
			rows = append(rows, row)
			continue
		}

		mismatch := conditionMismatch(
			groupRuns(a, row.Provider, row.Model, row.Stage),
			groupRuns(b, row.Provider, row.Model, row.Stage),
		)
		if len(mismatch) > 0 {
			row.Reason = "conditions_differ"
			row.Detail = strings.Join(mismatch, " / ")
			row.Note = row.NoteFor(i18n.EN)
			row.CitedDeltaText = i18n.EN.Unmeasured()
			row.BrandDeltaText = i18n.EN.Unmeasured()
			rows = append(rows, row)
			continue
		}

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
		rows = append(rows, row)
	}
	return rows
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
		if strings.Join(left, ",") != strings.Join(right, ",") {
			mismatches = append(mismatches, label+": "+strings.Join(left, ",")+" -> "+strings.Join(right, ","))
		}
	}
	compare("api_endpoint_version", func(v MeasurementIdentity) string { return v.EndpointVersion })
	compare("question_set_sha256", func(v MeasurementIdentity) string { return v.QuestionSetSHA256 })
	compare("tool_version", func(v MeasurementIdentity) string { return v.ToolVersion })
	return mismatches
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
