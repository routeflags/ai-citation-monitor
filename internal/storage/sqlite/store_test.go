package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "aicm.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedProject(t *testing.T, db *DB) (*Project, Question) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.EnsureProject(ctx, Project{
		SiteID: "demo", OwnDomain: "example.shop", Subdomains: true, Market: "ja-JP", Language: "ja",
	}); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	project, err := db.Project(ctx)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	question, err := db.UpsertQuestion(ctx, project.ID, Question{
		Key: "Q01", Text: "THCVとTHCの違いは？", Category: "comparison", Language: "ja",
	})
	if err != nil {
		t.Fatalf("UpsertQuestion: %v", err)
	}
	return project, question
}

func sampleWrite(project *Project, question Question, planKey, model string) RunWrite {
	host := "example.shop"
	answer := "THCVはTHCに似た構造です。"
	sum := sha256.Sum256([]byte(answer))
	return RunWrite{
		ProjectID: project.ID, QuestionID: question.ID, QuestionVersion: question.Version,
		RepeatIndex: 1, Provider: "openai", Model: model,
		APIEndpointVersion: "responses/v1+web_search", SearchEnabled: true,
		Stage: "smoke", QuestionSetVersion: "1.0.0", QuestionSetSHA256: "abc",
		PlanKey: planKey, Session: "2026-11-15",
		StartedAtUTC: "2026-11-15T01:00:00Z", CompletedAtUTC: "2026-11-15T01:00:05Z",
		Status: "success", CitationObservability: "observed",
		CitedOwn: true, CitationCount: 1, BrandMentioned: true, ToolVersion: "0.1.0+norm1",
		AnswerText: answer, AnswerSHA256: hex.EncodeToString(sum[:]),
		RawResponseJSON: `{"id":"x"}`,
		Citations: []CitationWrite{{
			Position: 1, RawURL: "https://example.shop/a", NormalizedURL: "https://example.shop/a",
			DedupeKey: "https://example.shop/a", Hostname: &host, SourceType: "own", IsOwn: true,
		}},
		Mentions: []MentionWrite{{Term: "Example", Kind: "brand", MatchedText: "example", Occurrences: 1}},
		Usage:    UsageWrite{CostStatus: "estimated", CostCurrency: "USD", EstimatedCost: floatPointer(0.01)},
	}
}

func floatPointer(value float64) *float64 { return &value }

func TestInsertRunIsAppendOnly(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	project, question := seedProject(t, db)

	write := sampleWrite(project, question, "key-1", "gpt-test")
	if _, inserted, err := db.InsertRun(ctx, write); err != nil || !inserted {
		t.Fatalf("first insert: inserted=%v err=%v", inserted, err)
	}
	if _, inserted, err := db.InsertRun(ctx, write); err != nil || inserted {
		t.Fatalf("second insert: inserted=%v err=%v (the same run must be skipped)", inserted, err)
	}

	runs, err := db.Runs(ctx, RunFilter{})
	if err != nil {
		t.Fatalf("Runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1 (no overwrite)", len(runs))
	}
	responses, err := db.Responses(ctx, []int64{runs[0].ID})
	if err != nil || len(responses) != 1 {
		t.Fatalf("responses = %d err=%v", len(responses), err)
	}
	if responses[0].AnswerSHA256 != write.AnswerSHA256 {
		t.Errorf("the stored hash does not match the answer")
	}
	if responses[0].EstimatedCost == nil {
		t.Error("the cost was not stored")
	}

	counts, err := db.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Runs != 1 || counts.Citations != 1 || counts.Mentions != 1 || counts.RunsValid != 1 {
		t.Errorf("counts = %+v", counts)
	}
}

func TestDifferentModelIsADifferentRun(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	project, question := seedProject(t, db)

	if _, _, err := db.InsertRun(ctx, sampleWrite(project, question, "key-a", "gpt-a")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.InsertRun(ctx, sampleWrite(project, question, "key-b", "gpt-b")); err != nil {
		t.Fatal(err)
	}
	runs, err := db.Runs(ctx, RunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %d, want 2 (a different model is a different measurement)", len(runs))
	}
	byModel, err := db.Runs(ctx, RunFilter{Model: "gpt-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byModel) != 1 || byModel[0].Model != "gpt-b" {
		t.Errorf("model filter returned %+v", byModel)
	}
}

func TestIntegrityFindsMissingEvidence(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	project, question := seedProject(t, db)
	if _, _, err := db.InsertRun(ctx, sampleWrite(project, question, "key-1", "gpt-test")); err != nil {
		t.Fatal(err)
	}

	issues, err := db.IntegrityIssues(ctx)
	if err != nil {
		t.Fatalf("IntegrityIssues: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("a consistent database produced issues: %+v", issues)
	}

	// Simulate lost evidence: the run is there but the answer is not. The
	// children have to go first because the foreign keys protect them.
	for _, statement := range []string{
		`DELETE FROM citations`, `DELETE FROM mentions`, `DELETE FROM usage_records`, `DELETE FROM responses`,
	} {
		if _, err := db.SQL().ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	issues, err = db.IntegrityIssues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, issue := range issues {
		kinds[issue.Kind] = true
	}
	if !kinds["run_without_response"] {
		t.Fatalf("a run without a response was not reported: %+v", issues)
	}
}

func TestIntegrityDetectsEditedAnswer(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	project, question := seedProject(t, db)
	if _, _, err := db.InsertRun(ctx, sampleWrite(project, question, "key-1", "gpt-test")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().ExecContext(ctx, `UPDATE responses SET answer_text = 'edited later'`); err != nil {
		t.Fatal(err)
	}
	issues, err := db.IntegrityIssues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, issue := range issues {
		if issue.Kind == "answer_sha256_mismatch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("an edited answer was not detected: %+v", issues)
	}
}

func TestMigrateIsIdempotentAndKeepsRecords(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	project, question := seedProject(t, db)
	if _, _, err := db.InsertRun(ctx, sampleWrite(project, question, "key-1", "gpt-test")); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	counts, err := db.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Runs != 1 {
		t.Fatalf("runs = %d after a re-migration, want 1", counts.Runs)
	}
}

func TestSumCostTracksUnknown(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	project, question := seedProject(t, db)
	write := sampleWrite(project, question, "key-1", "gpt-test")
	if _, _, err := db.InsertRun(ctx, write); err != nil {
		t.Fatal(err)
	}
	total, known, err := db.SumCost(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !known || total <= 0 {
		t.Fatalf("sum = %v known = %v", total, known)
	}

	unknown := sampleWrite(project, question, "key-2", "gpt-unknown")
	unknown.Usage = UsageWrite{CostStatus: "unknown", CostCurrency: "USD"}
	if _, _, err := db.InsertRun(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	_, known, err = db.SumCost(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if known {
		t.Fatal("a run with an unknown cost must make the total a lower bound, not a total")
	}
}
