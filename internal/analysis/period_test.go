package analysis

import (
	"reflect"
	"testing"

	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

func runWithQuestionSet(id int64, sha string) sqlite.Run {
	return sqlite.Run{ID: id, QuestionSetSHA256: sha}
}

// The report has to say how settled the brand term list is, and it may only say what
// the records say: the status is resolved from the question_sets rows the runs name.
func TestBrandTermsStatusesReadsOnlyWhatTheRecordsSay(t *testing.T) {
	cases := []struct {
		name     string
		runs     []sqlite.Run
		bySHA    map[string]string
		want     []string
		proposed bool
	}{
		{
			name:  "no runs records nothing, so there is no status line",
			runs:  nil,
			bySHA: map[string]string{"sha-a": "proposed"},
			want:  nil,
		},
		{
			name:     "a proposed set",
			runs:     []sqlite.Run{runWithQuestionSet(1, "sha-a")},
			bySHA:    map[string]string{"sha-a": "proposed"},
			want:     []string{BrandTermsStatusProposed},
			proposed: true,
		},
		{
			name:  "a confirmed set",
			runs:  []sqlite.Run{runWithQuestionSet(1, "sha-a"), runWithQuestionSet(2, "sha-a")},
			bySHA: map[string]string{"sha-a": "confirmed"},
			want:  []string{BrandTermsStatusConfirmed},
		},
		{
			name: "a mixed period lists both, proposed first",
			runs: []sqlite.Run{
				runWithQuestionSet(1, "sha-confirmed"),
				runWithQuestionSet(2, "sha-proposed"),
			},
			bySHA:    map[string]string{"sha-confirmed": "confirmed", "sha-proposed": "proposed"},
			want:     []string{BrandTermsStatusProposed, BrandTermsStatusConfirmed},
			proposed: true,
		},
		{
			name:  "a set that declared nothing is not declared, never confirmed",
			runs:  []sqlite.Run{runWithQuestionSet(1, "sha-a")},
			bySHA: map[string]string{"sha-a": ""},
			want:  []string{BrandTermsNotDeclared},
		},
		{
			name:  "an unknown hash (a run recorded by hand) is not declared",
			runs:  []sqlite.Run{runWithQuestionSet(1, "sha-unknown")},
			bySHA: map[string]string{},
			want:  []string{BrandTermsNotDeclared},
		},
		{
			name:  "a run without a question set (a CSV list) is not declared",
			runs:  []sqlite.Run{runWithQuestionSet(1, "")},
			bySHA: map[string]string{},
			want:  []string{BrandTermsNotDeclared},
		},
		{
			name:  "an old database row (empty column) is not declared",
			runs:  []sqlite.Run{runWithQuestionSet(1, "sha-old")},
			bySHA: map[string]string{"sha-old": ""},
			want:  []string{BrandTermsNotDeclared},
		},
		{
			name:  "a hand-edited value falls back to not declared, not confirmed",
			runs:  []sqlite.Run{runWithQuestionSet(1, "sha-a")},
			bySHA: map[string]string{"sha-a": "probably fine"},
			want:  []string{BrandTermsNotDeclared},
		},
		{
			name: "not declared and proposed both appear when both are recorded",
			runs: []sqlite.Run{
				runWithQuestionSet(1, "sha-proposed"),
				runWithQuestionSet(2, "sha-csv"),
			},
			bySHA:    map[string]string{"sha-proposed": "proposed"},
			want:     []string{BrandTermsStatusProposed, BrandTermsNotDeclared},
			proposed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := brandTermsStatuses(tc.runs, tc.bySHA)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("brandTermsStatuses = %v, want %v", got, tc.want)
			}
			conditions := Conditions{BrandTermsStatuses: got}
			if conditions.HasBrandTermsProposed() != tc.proposed {
				t.Errorf("HasBrandTermsProposed = %v, want %v", !tc.proposed, tc.proposed)
			}
		})
	}
}

func TestQuestionSetHashesSkipsEmptyAndDuplicates(t *testing.T) {
	runs := []sqlite.Run{
		runWithQuestionSet(1, "sha-a"),
		runWithQuestionSet(2, "sha-a"),
		runWithQuestionSet(3, ""),
		runWithQuestionSet(4, "sha-b"),
	}
	got := questionSetHashes(runs)
	want := []string{"sha-a", "sha-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("questionSetHashes = %v, want %v", got, want)
	}
}
