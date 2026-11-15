package reporting

import (
	"strings"
	"testing"

	"github.com/routeflags/ai-citation-monitor/internal/analysis"
)

func conditions(provider, model, stage string) analysis.Conditions {
	return analysis.Conditions{
		Providers:          []string{provider},
		Models:             []string{model},
		Stages:             []string{stage},
		QuestionSetSHA256s: []string{"2eee65141222aabbccdd"},
	}
}

// Regression for the extraction bug: report-<period>.md overwrote itself when two
// stages of the same period were rendered. The name must carry the conditions.
func TestFileNameCarriesEveryCondition(t *testing.T) {
	period := analysis.Period{Label: "2026-11-12:2026-11-12"}
	smoke := FileName(period, conditions("openai", "gpt-6-luna", "smoke"))
	initial := FileName(period, conditions("openai", "gpt-6-luna", "initial"))

	if smoke == initial {
		t.Fatalf("two stages produced the same file name: %s", smoke)
	}
	for _, want := range []string{"2026-11-12_2026-11-12", "openai", "gpt-6-luna", "smoke", "qs-2eee65141222"} {
		if !strings.Contains(smoke, want) {
			t.Errorf("file name %q does not contain %q", smoke, want)
		}
	}
	if !strings.Contains(initial, "initial") {
		t.Errorf("file name %q does not contain the stage", initial)
	}
}

func TestFileNameHandlesUnknownConditions(t *testing.T) {
	name := FileName(analysis.Period{Label: "all"}, analysis.Conditions{})
	for _, want := range []string{"report-all", "all-providers", "all-models", "all-stages"} {
		if !strings.Contains(name, want) {
			t.Errorf("file name %q does not contain %q", name, want)
		}
	}
}

func TestFileNameIsPortable(t *testing.T) {
	name := FileName(analysis.Period{Label: "2026-11-12:2026-11-12"}, conditions("openai", "vendor/model", "smoke"))
	if strings.ContainsAny(name, "/\\:") {
		t.Fatalf("file name %q contains a path separator or colon", name)
	}
}
