package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

// IntegrityIssues checks the database against itself, not against the
// filesystem: the answer text and the raw response live in the DB, so a missing
// artefact shows up as a run without a response row, a child row pointing at a
// missing response, or a stored hash that no longer matches the text.
//
// This is the replacement for the old cassette audit: with direct HTTP there is
// no cassette file to lose, so "evidence loss" means "the DB stopped being
// self-consistent".
func (d *DB) IntegrityIssues(ctx context.Context) ([]Issue, error) {
	var issues []Issue

	queries := []struct {
		kind string
		sql  string
	}{
		{
			kind: "run_without_response",
			sql: `SELECT r.id FROM runs r
			      LEFT JOIN responses resp ON resp.run_id = r.id
			      WHERE resp.id IS NULL ORDER BY r.id`,
		},
		{
			kind: "response_without_run",
			sql: `SELECT resp.run_id FROM responses resp
			      LEFT JOIN runs r ON r.id = resp.run_id
			      WHERE r.id IS NULL ORDER BY resp.run_id`,
		},
		{
			kind: "citation_without_response",
			sql: `SELECT c.run_id FROM citations c
			      LEFT JOIN responses resp ON resp.id = c.response_id
			      WHERE resp.id IS NULL ORDER BY c.run_id`,
		},
		{
			kind: "citation_run_mismatch",
			sql: `SELECT c.run_id FROM citations c
			      JOIN responses resp ON resp.id = c.response_id
			      WHERE resp.run_id <> c.run_id ORDER BY c.run_id`,
		},
		{
			kind: "mention_without_run",
			sql: `SELECT m.run_id FROM mentions m
			      LEFT JOIN runs r ON r.id = m.run_id
			      WHERE r.id IS NULL ORDER BY m.run_id`,
		},
		{
			kind: "usage_without_run",
			sql: `SELECT u.run_id FROM usage_records u
			      LEFT JOIN runs r ON r.id = u.run_id
			      WHERE r.id IS NULL ORDER BY u.run_id`,
		},
		{
			kind: "missing_usage_record",
			sql: `SELECT r.id FROM runs r
			      LEFT JOIN usage_records u ON u.run_id = r.id
			      WHERE u.id IS NULL ORDER BY r.id`,
		},
		{
			kind: "citation_count_mismatch",
			sql: `SELECT r.id FROM runs r
			      LEFT JOIN (SELECT run_id, COUNT(*) AS n FROM citations GROUP BY run_id) c
			        ON c.run_id = r.id
			      WHERE r.citation_count <> COALESCE(c.n, 0) ORDER BY r.id`,
		},
		{
			kind: "success_without_completed_at",
			sql: `SELECT id FROM runs
			      WHERE status = 'success' AND completed_at_utc = '' ORDER BY id`,
		},
		{
			kind: "question_version_missing",
			sql: `SELECT r.id FROM runs r
			      LEFT JOIN question_versions qv
			        ON qv.question_id = r.question_id AND qv.version = r.question_version
			      WHERE qv.id IS NULL ORDER BY r.id`,
		},
	}

	for _, query := range queries {
		rows, err := d.sql.QueryContext(ctx, query.sql)
		if err != nil {
			return nil, fmt.Errorf("integrity query %s: %w", query.kind, err)
		}
		for rows.Next() {
			var runID int64
			if err := rows.Scan(&runID); err != nil {
				rows.Close()
				return nil, err
			}
			issues = append(issues, Issue{Kind: query.kind, Detail: issueDetail(query.kind), RunID: runID})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	// Hash and emptiness checks need the text in Go.
	hashRows, err := d.sql.QueryContext(ctx, `SELECT run_id, answer_text, answer_sha256 FROM responses ORDER BY run_id`)
	if err != nil {
		return nil, err
	}
	defer hashRows.Close()
	for hashRows.Next() {
		var runID int64
		var text, stored string
		if err := hashRows.Scan(&runID, &text, &stored); err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(text))
		if actual := hex.EncodeToString(sum[:]); actual != stored {
			issues = append(issues, Issue{
				Kind:   "answer_sha256_mismatch",
				Detail: fmt.Sprintf("stored=%s actual=%s (the answer text changed after it was recorded)", stored, actual),
				RunID:  runID,
			})
		}
	}

	emptyRows, err := d.sql.QueryContext(ctx, `
		SELECT r.id FROM runs r JOIN responses resp ON resp.run_id = r.id
		WHERE r.status = 'success' AND TRIM(resp.answer_text) = '' ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer emptyRows.Close()
	for emptyRows.Next() {
		var runID int64
		if err := emptyRows.Scan(&runID); err != nil {
			return nil, err
		}
		issues = append(issues, Issue{Kind: "success_with_empty_answer", Detail: issueDetail("success_with_empty_answer"), RunID: runID})
	}

	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Kind == issues[j].Kind {
			return issues[i].RunID < issues[j].RunID
		}
		return issues[i].Kind < issues[j].Kind
	})
	return issues, nil
}

func issueDetail(kind string) string {
	switch kind {
	case "run_without_response":
		return "run has no responses row: the answer text and raw response are gone"
	case "response_without_run":
		return "responses row points at a run that does not exist"
	case "citation_without_response":
		return "citation points at a response that does not exist"
	case "citation_run_mismatch":
		return "citation.run_id differs from its response.run_id"
	case "mention_without_run":
		return "mention points at a run that does not exist"
	case "usage_without_run":
		return "usage record points at a run that does not exist"
	case "missing_usage_record":
		return "run has no usage record (cost/usage is unknown)"
	case "citation_count_mismatch":
		return "runs.citation_count differs from the number of citation rows"
	case "success_without_completed_at":
		return "successful run has no completed_at_utc"
	case "question_version_missing":
		return "run references a question version that is not stored"
	case "success_with_empty_answer":
		return "successful run has an empty answer (a failed fetch recorded as success?)"
	default:
		return ""
	}
}

// LatestQuestionSet returns the most recently imported question set provenance.
func (d *DB) LatestQuestionSet(ctx context.Context, projectID int64) (version, sha string, err error) {
	row := d.sql.QueryRowContext(ctx,
		`SELECT set_version, sha256 FROM question_sets WHERE project_id = ? ORDER BY id DESC LIMIT 1`, projectID)
	if err := row.Scan(&version, &sha); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", nil
		}
		return "", "", err
	}
	return version, sha, nil
}

// QuestionExists reports whether the project already has this question key.
func (d *DB) QuestionExists(ctx context.Context, projectID int64, key string) (bool, error) {
	var id int64
	err := d.sql.QueryRowContext(ctx,
		`SELECT id FROM questions WHERE project_id = ? AND question_key = ?`, projectID, key).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}
