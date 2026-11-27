package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"testing"

	"github.com/routeflags/ai-citation-monitor/migrations"
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
		Usage:    UsageWrite{InputTokens: intPointer(1000), OutputTokens: intPointer(500), ToolCalls: intPointer(1)},
	}
}

func intPointer(value int) *int { return &value }

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
	if runs[0].ToolCalls == nil || *runs[0].ToolCalls != 1 {
		t.Errorf("tool calls were not joined from usage_records: %v", runs[0].ToolCalls)
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

// The brand terms status is provenance of the imported set (issue #7, D20): it is
// written with the set, survives the re-import of the same file, and is read back by
// sha. An unrecorded sha and an empty value both mean "not declared".
func TestRecordQuestionSetKeepsTheBrandTermsStatus(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	project, _ := seedProject(t, db)

	state, err := db.RecordQuestionSet(ctx, project.ID, "1.0.0", "sha-proposed", "set.yml", "proposed")
	if err != nil || state != "inserted" {
		t.Fatalf("first record: state=%q err=%v", state, err)
	}
	// The same bytes again: skipped, no second row, and nothing rewrites the status.
	state, err = db.RecordQuestionSet(ctx, project.ID, "1.0.0", "sha-proposed", "set.yml", "proposed")
	if err != nil || state != "skipped" {
		t.Fatalf("re-import of the same sha: state=%q err=%v", state, err)
	}
	if _, err := db.RecordQuestionSet(ctx, project.ID, "1.0.0", "sha-undeclared", "other.yml", ""); err != nil {
		t.Fatalf("record without a status: %v", err)
	}

	statuses, err := db.BrandTermsStatuses(ctx, []string{"sha-proposed", "sha-undeclared", "sha-missing", ""})
	if err != nil {
		t.Fatalf("BrandTermsStatuses: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("statuses = %v, want exactly the two recorded sets", statuses)
	}
	if statuses["sha-proposed"] != "proposed" {
		t.Errorf("sha-proposed = %q, want proposed", statuses["sha-proposed"])
	}
	if value, ok := statuses["sha-undeclared"]; !ok || value != "" {
		t.Errorf("sha-undeclared = %q (present=%v), want an empty value for a set that declared nothing", value, ok)
	}
	if _, ok := statuses["sha-missing"]; ok {
		t.Error("a sha that was never recorded must be absent, not an empty status")
	}

	counts, err := db.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.QuestionSets != 2 {
		t.Fatalf("question_sets = %d, want 2 (the re-import wrote nothing)", counts.QuestionSets)
	}
}

// A database created before migration 0003 keeps its rows: the new column is empty,
// which the report reads as "not declared" rather than "confirmed".
func TestOldQuestionSetsReadAsNotDeclared(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	project, _ := seedProject(t, db)
	if _, err := db.RecordQuestionSet(ctx, project.ID, "1.0.0", "sha-old", "set.yml", ""); err != nil {
		t.Fatalf("RecordQuestionSet: %v", err)
	}
	statuses, err := db.BrandTermsStatuses(ctx, []string{"sha-old"})
	if err != nil {
		t.Fatalf("BrandTermsStatuses: %v", err)
	}
	if value := statuses["sha-old"]; value != "" {
		t.Errorf("status = %q, want an empty value", value)
	}
}

// A database written by v0.2.0 has question_sets rows and no status column. Opening it
// with this build applies migration 0003 without touching those rows, and the report
// reads them as "not declared" rather than as anything measured.
func TestUpgradingAnOldDatabaseLeavesQuestionSetsNotDeclared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	handle, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open the old database: %v", err)
	}
	for _, name := range []string{"0001_init.sql", "0002_response_body_stored.sql"} {
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := handle.Exec(string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO schema_migrations (version, applied_at) VALUES (1, 'then'), (2, 'then')`,
		`INSERT INTO projects (site_id, own_domain, market, language, created_at, updated_at)
			VALUES ('demo', 'example.shop', 'ja-JP', 'ja', 'then', 'then')`,
		`INSERT INTO question_sets (project_id, set_version, sha256, path, loaded_at)
			VALUES (1, '1.0.0', 'sha-old', 'set.yml', 'then')`,
	} {
		if _, err := handle.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close the old database: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open (upgrade): %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	statuses, err := db.BrandTermsStatuses(ctx, []string{"sha-old"})
	if err != nil {
		t.Fatalf("BrandTermsStatuses: %v", err)
	}
	value, ok := statuses["sha-old"]
	if !ok || value != "" {
		t.Fatalf("status of a pre-0003 row = %q (present=%v), want an empty value", value, ok)
	}
	counts, err := db.Counts(ctx)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.QuestionSets != 1 {
		t.Fatalf("question_sets = %d after the upgrade, want 1 (the rows are kept)", counts.QuestionSets)
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
