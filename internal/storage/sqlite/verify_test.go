package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"path/filepath"
	"testing"
)

// These tests follow the reproduction steps a reader of the handoff document came
// up with: one invariant, one broken row, one expected issue. `aicm verify` is the
// replacement for the old cassette audit, so what it can detect is worth pinning.

func newVerifyDB(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aicm.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

// seedRecordedRun inserts one complete run (run + response + citation + usage).
func seedRecordedRun(t *testing.T, db *DB) int64 {
	t.Helper()
	project, question := seedProject(t, db)
	id, inserted, err := db.InsertRun(context.Background(), sampleWrite(project, question, "key-1", "gpt-test"))
	if err != nil || !inserted {
		t.Fatalf("InsertRun: inserted=%v err=%v", inserted, err)
	}
	return id
}

func issueKinds(t *testing.T, db *DB) map[string]int {
	t.Helper()
	issues, err := db.IntegrityIssues(context.Background())
	if err != nil {
		t.Fatalf("IntegrityIssues: %v", err)
	}
	counts := map[string]int{}
	for _, issue := range issues {
		counts[issue.Kind]++
	}
	return counts
}

// execOn runs a statement on a connection with foreign keys OFF, which is how an
// orphan row can exist at all (the tool never writes one).
func execOn(t *testing.T, path, statement string, args ...any) {
	t.Helper()
	handle, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer handle.Close()
	if _, err := handle.Exec(statement, args...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

func TestIntegrityAcceptsACompleteRecord(t *testing.T) {
	db, _ := newVerifyDB(t)
	seedRecordedRun(t, db)
	if got := issueKinds(t, db); len(got) != 0 {
		t.Fatalf("a complete run must be consistent, got %v", got)
	}
}

func TestIntegrityDetectsEachBrokenInvariant(t *testing.T) {
	cases := []struct {
		name   string
		break_ func(t *testing.T, db *DB, path string, runID int64)
		kind   string
	}{
		{
			name: "run without a response",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `DELETE FROM citations WHERE run_id = ?`, runID)
				execOn(t, path, `DELETE FROM mentions WHERE run_id = ?`, runID)
				execOn(t, path, `DELETE FROM responses WHERE run_id = ?`, runID)
			},
			kind: "run_without_response",
		},
		{
			name: "response without a run",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `INSERT INTO responses (run_id, answer_sha256, citation_observability, created_at)
					VALUES (999999, 'x', 'observed', '2026-11-15T00:00:00Z')`)
			},
			kind: "response_without_run",
		},
		{
			name: "citation without a response",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `INSERT INTO citations (run_id, response_id, position, raw_url, source_type, is_own, malformed)
					VALUES (?, 999999, 9, 'https://example.shop/x', 'own', 1, 0)`, runID)
			},
			kind: "citation_without_response",
		},
		{
			name: "mention without a run",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `INSERT INTO mentions (run_id, response_id, term, kind, occurrences, ambiguous)
					VALUES (999999, (SELECT id FROM responses WHERE run_id = ?), 'Other', 'brand', 1, 0)`, runID)
			},
			kind: "mention_without_run",
		},
		{
			name: "usage record without a run",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `INSERT INTO usage_records (run_id, provider, model, created_at)
					VALUES (999999, 'openai', 'gpt-test', '2026-11-15T00:00:00Z')`)
			},
			kind: "usage_without_run",
		},
		{
			name: "run without a usage record",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `DELETE FROM usage_records WHERE run_id = ?`, runID)
			},
			kind: "missing_usage_record",
		},
		{
			name: "citation count that disagrees with the rows",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `UPDATE runs SET citation_count = 5 WHERE id = ?`, runID)
			},
			kind: "citation_count_mismatch",
		},
		{
			name: "success without a completion time",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `UPDATE runs SET completed_at_utc = '' WHERE id = ?`, runID)
			},
			kind: "success_without_completed_at",
		},
		{
			name: "success with an empty answer",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `UPDATE responses SET answer_text = '' WHERE run_id = ?`, runID)
			},
			kind: "success_with_empty_answer",
		},
		{
			name: "answer edited after it was recorded",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `UPDATE responses SET answer_text = 'rewritten' WHERE run_id = ?`, runID)
			},
			kind: "answer_sha256_mismatch",
		},
		{
			name: "run pointing at a question version that is gone",
			break_: func(t *testing.T, db *DB, path string, runID int64) {
				execOn(t, path, `UPDATE runs SET question_version = 'sha:missing' WHERE id = ?`, runID)
			},
			kind: "question_version_missing",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			db, path := newVerifyDB(t)
			runID := seedRecordedRun(t, db)
			testCase.break_(t, db, path, runID)
			if got := issueKinds(t, db)[testCase.kind]; got == 0 {
				t.Fatalf("%s was not reported; issues = %v", testCase.kind, issueKinds(t, db))
			}
		})
	}
}

// TestIntegrityRepairSequence walks the same steps a person repairing a damaged
// database would: the lights go out, some come back, and the last one goes out when
// the answer is restored.
func TestIntegrityRepairSequence(t *testing.T) {
	db, path := newVerifyDB(t)
	runID := seedRecordedRun(t, db)

	execOn(t, path, `DELETE FROM citations WHERE run_id = ?`, runID)
	execOn(t, path, `DELETE FROM mentions WHERE run_id = ?`, runID)
	execOn(t, path, `DELETE FROM responses WHERE run_id = ?`, runID)
	execOn(t, path, `DELETE FROM usage_records WHERE run_id = ?`, runID)
	// The row count is part of the record: with the citations gone, the stored count
	// has to go too, or the repair leaves a second inconsistency behind.
	execOn(t, path, `UPDATE runs SET citation_count = 0 WHERE id = ?`, runID)

	got := issueKinds(t, db)
	for _, kind := range []string{"run_without_response", "missing_usage_record"} {
		if got[kind] == 0 {
			t.Fatalf("%s was not reported after the evidence was removed: %v", kind, got)
		}
	}

	// A response with the wrong hash: the shape is back but the text is not proven.
	execOn(t, path, `INSERT INTO responses (run_id, answer_text, answer_sha256, citation_observability, created_at)
		VALUES (?, 'text', 'not-a-hash', 'observed', '2026-11-15T00:00:00Z')`, runID)
	if issueKinds(t, db)["answer_sha256_mismatch"] == 0 {
		t.Fatal("a wrong answer hash must be reported")
	}

	// The hash is the sha256 of the text, without the sha: prefix that question
	// versions use.
	sum := sha256.Sum256([]byte("text"))
	execOn(t, path, `UPDATE responses SET answer_sha256 = ? WHERE run_id = ?`, hex.EncodeToString(sum[:]), runID)
	execOn(t, path, `INSERT INTO usage_records (run_id, provider, model, created_at)
		VALUES (?, 'openai', 'gpt-test', '2026-11-15T00:00:00Z')`, runID)

	if got := issueKinds(t, db); len(got) != 0 {
		t.Fatalf("the repaired run must be consistent, got %v", got)
	}
}

// TestHashOnlyBodyKeepsTheHashNotTheText covers the C1 contract's O20: a database
// that is committed must not carry the answer text. The hash stays, the empty text
// is a choice (body_stored = 0), and verify must not call it a lost answer.
func TestHashOnlyBodyKeepsTheHashNotTheText(t *testing.T) {
	db, _ := newVerifyDB(t)
	project, question := seedProject(t, db)
	write := sampleWrite(project, question, "key-hash-only", "gpt-test")
	write.HashOnlyBody = true

	runID, inserted, err := db.InsertRun(context.Background(), write)
	if err != nil || !inserted {
		t.Fatalf("InsertRun: inserted=%v err=%v", inserted, err)
	}
	responses, err := db.Responses(context.Background(), []int64{runID})
	if err != nil || len(responses) != 1 {
		t.Fatalf("Responses: %d rows, err=%v", len(responses), err)
	}
	response := responses[0]
	if response.AnswerText != "" || response.RawResponseJSON != "" {
		t.Errorf("the body must not be stored: text=%q raw=%q", response.AnswerText, response.RawResponseJSON)
	}
	if response.BodyStored {
		t.Error("body_stored must be 0 for a hash-only row")
	}
	if response.AnswerSHA256 == "" {
		t.Error("the answer hash must still be recorded")
	}
	if got := issueKinds(t, db); len(got) != 0 {
		t.Fatalf("a hash-only run is consistent, got %v", got)
	}
}
