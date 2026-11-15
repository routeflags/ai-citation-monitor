package question

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validYAML = `set_version: "1.0.0"
site_id: demo
own_domain: example.shop
market: ja-JP
brand_terms:
  - Example
questions:
  - { id: Q01, text: "THCVとTHCの違いは？", category: comparison }
  - { id: Q02, text: "THCHとは何ですか？", category: definition }
stages:
  smoke:
    runs_per_question: 1
    question_ids: [Q01]
  initial:
    runs_per_question: 3
    question_ids: [Q01, Q02]
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "questions.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidQuestionSetPasses(t *testing.T) {
	set, err := LoadYAML(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	errorsOut, warnings := set.Validate(false)
	if len(errorsOut) != 0 {
		t.Fatalf("a valid set produced errors: %v", errorsOut)
	}
	for _, warning := range warnings {
		if strings.HasPrefix(warning, "E") {
			t.Errorf("a warning block contained an error: %s", warning)
		}
	}
	summary := set.Summarise()
	if summary.Questions != 2 {
		t.Errorf("questions = %d", summary.Questions)
	}
	if set.StageRoundTrips("initial") != 6 {
		t.Errorf("initial round trips = %d, want 2 questions x 3 runs", set.StageRoundTrips("initial"))
	}
	if len(set.StageQuestions("smoke")) != 1 {
		t.Errorf("smoke questions = %d", len(set.StageQuestions("smoke")))
	}
	if set.SHA256 == "" {
		t.Error("the file hash must be recorded")
	}
}

func TestQuestionSetErrors(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		wantCode string
	}{
		{
			name:     "unknown category",
			content:  strings.Replace(validYAML, "category: comparison", "category: nonsense", 1),
			wantCode: "E022",
		},
		{
			name:     "duplicate id",
			content:  strings.Replace(validYAML, "id: Q02", "id: Q01", 1),
			wantCode: "E021",
		},
		{
			name:     "missing own_domain",
			content:  strings.Replace(validYAML, "own_domain: example.shop", "own_domain: \"\"", 1),
			wantCode: "E012",
		},
		{
			name:     "stage references an unknown id",
			content:  strings.Replace(validYAML, "question_ids: [Q01, Q02]", "question_ids: [Q01, Q09]", 1),
			wantCode: "E031",
		},
		{
			name:     "runs per question out of range",
			content:  strings.Replace(validYAML, "runs_per_question: 3", "runs_per_question: 9", 1),
			wantCode: "E033",
		},
		{
			name:     "two questions normalise to the same text",
			content:  strings.Replace(validYAML, "THCHとは何ですか？", "ＴＨＣＶとＴＨＣの違いは？", 1),
			wantCode: "E024",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, err := LoadYAML(writeTemp(t, tc.content))
			if err != nil {
				t.Fatalf("LoadYAML: %v", err)
			}
			errorsOut, _ := set.Validate(false)
			joined := strings.Join(errorsOut, "\n")
			if !strings.Contains(joined, tc.wantCode) {
				t.Fatalf("expected %s, got:\n%s", tc.wantCode, joined)
			}
		})
	}
}

func TestStageNestingIsAWarningUnlessStrict(t *testing.T) {
	// initial no longer contains the smoke question: smoke is not a subset.
	content := strings.Replace(validYAML, "question_ids: [Q01, Q02]", "question_ids: [Q02]", 1)
	set, err := LoadYAML(writeTemp(t, content))
	if err != nil {
		t.Fatal(err)
	}
	errorsOut, warnings := set.Validate(false)
	if len(errorsOut) != 0 {
		t.Fatalf("nesting must only warn by default: %v", errorsOut)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "W040") {
		t.Fatalf("expected W040: %v", warnings)
	}
	errorsOut, _ = set.Validate(true)
	if !strings.Contains(strings.Join(errorsOut, "\n"), "E040") {
		t.Fatalf("--strict must turn the nesting warning into an error: %v", errorsOut)
	}
}

func TestParseCSV(t *testing.T) {
	questions, err := ParseCSV(strings.NewReader("id,text,category,language\nQ01,質問,definition,ja\n"))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	if len(questions) != 1 || questions[0].Key != "Q01" || questions[0].Language != "ja" {
		t.Fatalf("questions = %+v", questions)
	}
	if _, err := ParseCSV(strings.NewReader("text,category\nfoo,bar\n")); err == nil {
		t.Fatal("a CSV without the id header must fail instead of guessing")
	}
	if _, err := ParseCSV(strings.NewReader("id,text,category\n")); err == nil {
		t.Fatal("an empty CSV must fail")
	}
}
