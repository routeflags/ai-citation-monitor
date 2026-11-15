package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// NowUTC is the single timestamp format used everywhere in the store.
func NowUTC() string { return time.Now().UTC().Format("2006-01-02T15:04:05Z") }

// ContentVersion derives a question version from the question text, so editing a
// question cannot silently reuse the previous version.
func ContentVersion(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return "sha:" + hex.EncodeToString(sum[:])[:12]
}

// EnsureProject inserts or refreshes the project row and returns its id.
func (d *DB) EnsureProject(ctx context.Context, p Project) (int64, error) {
	now := NowUTC()
	subdomains := 0
	if p.Subdomains {
		subdomains = 1
	}
	if _, err := d.sql.ExecContext(ctx, `
		INSERT INTO projects (site_id, name, own_domain, subdomains, market, language, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (site_id) DO UPDATE SET
			name = excluded.name,
			own_domain = excluded.own_domain,
			subdomains = excluded.subdomains,
			market = excluded.market,
			language = excluded.language,
			updated_at = excluded.updated_at`,
		p.SiteID, p.Name, p.OwnDomain, subdomains, p.Market, p.Language, now, now,
	); err != nil {
		return 0, fmt.Errorf("upsert project %q: %w", p.SiteID, err)
	}
	var id int64
	if err := d.sql.QueryRowContext(ctx, `SELECT id FROM projects WHERE site_id = ?`, p.SiteID).Scan(&id); err != nil {
		return 0, fmt.Errorf("read project %q: %w", p.SiteID, err)
	}
	return id, nil
}

// Project returns the single configured project (the tool measures one site per
// database file).
func (d *DB) Project(ctx context.Context) (*Project, error) {
	row := d.sql.QueryRowContext(ctx, `
		SELECT id, site_id, name, own_domain, subdomains, market, language
		FROM projects ORDER BY id LIMIT 1`)
	var p Project
	var subdomains int
	if err := row.Scan(&p.ID, &p.SiteID, &p.Name, &p.OwnDomain, &subdomains, &p.Market, &p.Language); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoProject
		}
		return nil, err
	}
	p.Subdomains = subdomains == 1
	return &p, nil
}

// ErrNoProject means the database has no project yet (run `aicm init`).
var ErrNoProject = errors.New("no project in this database (run `aicm init` first)")

// UpsertQuestion stores a question and makes sure the version row exists.
// Returns the stored question with its current version.
func (d *DB) UpsertQuestion(ctx context.Context, projectID int64, q Question) (Question, error) {
	version := q.Version
	if version == "" {
		version = ContentVersion(q.Text)
	}
	now := NowUTC()
	if _, err := d.sql.ExecContext(ctx, `
		INSERT INTO questions (project_id, question_key, text, category, language, active, created_at)
		VALUES (?, ?, ?, ?, ?, 1, ?)
		ON CONFLICT (project_id, question_key) DO UPDATE SET
			text = excluded.text,
			category = excluded.category,
			language = excluded.language`,
		projectID, q.Key, q.Text, q.Category, q.Language, now,
	); err != nil {
		return Question{}, fmt.Errorf("upsert question %q: %w", q.Key, err)
	}
	var id int64
	if err := d.sql.QueryRowContext(ctx,
		`SELECT id FROM questions WHERE project_id = ? AND question_key = ?`, projectID, q.Key,
	).Scan(&id); err != nil {
		return Question{}, err
	}
	if _, err := d.sql.ExecContext(ctx, `
		INSERT OR IGNORE INTO question_versions (question_id, version, text, category, content_sha256, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, version, q.Text, q.Category, ContentVersion(q.Text), now,
	); err != nil {
		return Question{}, fmt.Errorf("record question version %q: %w", version, err)
	}
	return Question{ID: id, ProjectID: projectID, Key: q.Key, Text: q.Text, Category: q.Category, Language: q.Language, Active: true, Version: version}, nil
}

// Questions lists the active questions of a project with their current version.
func (d *DB) Questions(ctx context.Context, projectID int64) ([]Question, error) {
	rows, err := d.sql.QueryContext(ctx, `
		SELECT q.id, q.project_id, q.question_key, q.text, q.category, q.language, q.active,
		       COALESCE((
		           SELECT qv.version FROM question_versions qv
		           WHERE qv.question_id = q.id
		           ORDER BY qv.id DESC LIMIT 1
		       ), '')
		FROM questions q
		WHERE q.project_id = ? AND q.active = 1
		ORDER BY q.question_key`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	questions := make([]Question, 0, 32)
	for rows.Next() {
		var q Question
		var active int
		if err := rows.Scan(&q.ID, &q.ProjectID, &q.Key, &q.Text, &q.Category, &q.Language, &active, &q.Version); err != nil {
			return nil, err
		}
		q.Active = active == 1
		questions = append(questions, q)
	}
	return questions, rows.Err()
}

// QuestionVersion returns the stored text of one question version.
func (d *DB) QuestionVersion(ctx context.Context, questionID int64, version string) (string, error) {
	var text string
	err := d.sql.QueryRowContext(ctx,
		`SELECT text FROM question_versions WHERE question_id = ? AND version = ?`, questionID, version,
	).Scan(&text)
	return text, err
}

// RecordQuestionSet stores the provenance of an imported question set.
func (d *DB) RecordQuestionSet(ctx context.Context, projectID int64, setVersion, sha, path string) (string, error) {
	var existing int64
	err := d.sql.QueryRowContext(ctx, `SELECT id FROM question_sets WHERE sha256 = ?`, sha).Scan(&existing)
	if err == nil {
		return "skipped", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if _, err := d.sql.ExecContext(ctx, `
		INSERT INTO question_sets (project_id, set_version, sha256, path, loaded_at)
		VALUES (?, ?, ?, ?, ?)`, projectID, setVersion, sha, path, NowUTC()); err != nil {
		return "", err
	}
	return "inserted", nil
}

// AddCompetitor registers a competitor domain.
func (d *DB) AddCompetitor(ctx context.Context, projectID int64, domain, label string) error {
	_, err := d.sql.ExecContext(ctx, `
		INSERT INTO competitors (project_id, domain, label, source, created_at)
		VALUES (?, ?, ?, 'registered', ?)
		ON CONFLICT (project_id, domain) DO UPDATE SET label = excluded.label`,
		projectID, strings.ToLower(strings.TrimSpace(domain)), label, NowUTC())
	return err
}

// RemoveCompetitor deletes a registered competitor domain.
func (d *DB) RemoveCompetitor(ctx context.Context, projectID int64, domain string) (bool, error) {
	result, err := d.sql.ExecContext(ctx,
		`DELETE FROM competitors WHERE project_id = ? AND domain = ?`,
		projectID, strings.ToLower(strings.TrimSpace(domain)))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// Competitors lists registered competitor domains.
func (d *DB) Competitors(ctx context.Context, projectID int64) ([]string, error) {
	rows, err := d.sql.QueryContext(ctx,
		`SELECT domain FROM competitors WHERE project_id = ? ORDER BY domain`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var domains []string
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			return nil, err
		}
		domains = append(domains, domain)
	}
	return domains, rows.Err()
}

// PlanKeySeen reports whether a call with this identity already completed.
func (d *DB) PlanKeySeen(ctx context.Context, planKey string) (bool, error) {
	var id int64
	err := d.sql.QueryRowContext(ctx, `SELECT id FROM runs WHERE plan_key = ?`, planKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// InsertRun appends one run with its response, citations, mentions and usage in a
// single transaction. It never updates an existing run: when the plan_key is
// already present the call is skipped and inserted is false.
func (d *DB) InsertRun(ctx context.Context, w RunWrite) (runID int64, inserted bool, err error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	// Always release the transaction: a skipped run returns without committing,
	// and a leaked transaction holds the single writer connection forever.
	defer func() { _ = tx.Rollback() }()

	var seen int64
	scanErr := tx.QueryRowContext(ctx, `SELECT id FROM runs WHERE plan_key = ?`, w.PlanKey).Scan(&seen)
	if scanErr == nil {
		return 0, false, nil
	}
	if !errors.Is(scanErr, sql.ErrNoRows) {
		return 0, false, scanErr
	}

	searchEnabled := 0
	if w.SearchEnabled {
		searchEnabled = 1
	}
	citedOwn := 0
	if w.CitedOwn {
		citedOwn = 1
	}
	brandMentioned := 0
	if w.BrandMentioned {
		brandMentioned = 1
	}
	now := NowUTC()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO runs (
			project_id, question_id, question_version, repeat_index, provider, model,
			api_endpoint_version, search_enabled, measurement_method, stage,
			question_set_version, question_set_sha256, plan_key, session,
			started_at_utc, completed_at_utc, status, citation_observability,
			cited_own, citation_count, brand_mentioned, tool_version, inserted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'official_api', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.ProjectID, w.QuestionID, w.QuestionVersion, w.RepeatIndex, w.Provider, w.Model,
		w.APIEndpointVersion, searchEnabled, w.Stage,
		w.QuestionSetVersion, w.QuestionSetSHA256, w.PlanKey, w.Session,
		w.StartedAtUTC, w.CompletedAtUTC, w.Status, w.CitationObservability,
		citedOwn, w.CitationCount, brandMentioned, w.ToolVersion, now,
	)
	if err != nil {
		return 0, false, fmt.Errorf("insert run: %w", err)
	}
	runID, err = result.LastInsertId()
	if err != nil {
		return 0, false, err
	}

	responseResult, err := tx.ExecContext(ctx, `
		INSERT INTO responses (
			run_id, answer_text, answer_sha256, raw_response_json, citation_observability,
			error_code, error_message, input_tokens, output_tokens, tool_usage, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, w.AnswerText, w.AnswerSHA256, w.RawResponseJSON, w.CitationObservability,
		w.ErrorCode, w.ErrorMessage, w.Usage.InputTokens, w.Usage.OutputTokens,
		"", now,
	)
	if err != nil {
		return 0, false, fmt.Errorf("insert response: %w", err)
	}
	responseID, err := responseResult.LastInsertId()
	if err != nil {
		return 0, false, err
	}

	for _, citation := range w.Citations {
		isOwn := 0
		if citation.IsOwn {
			isOwn = 1
		}
		malformed := 0
		if citation.Malformed {
			malformed = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO citations (
				run_id, response_id, position, raw_url, normalized_url, dedupe_key,
				hostname, source_type, is_own, malformed)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, responseID, citation.Position, citation.RawURL, citation.NormalizedURL,
			citation.DedupeKey, citation.Hostname, citation.SourceType, isOwn, malformed,
		); err != nil {
			return 0, false, fmt.Errorf("insert citation %d: %w", citation.Position, err)
		}
	}

	for _, mention := range w.Mentions {
		ambiguous := 0
		if mention.Ambiguous {
			ambiguous = 1
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mentions (run_id, response_id, term, kind, matched_text, occurrences, ambiguous)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			runID, responseID, mention.Term, mention.Kind, mention.MatchedText,
			mention.Occurrences, ambiguous,
		); err != nil {
			return 0, false, fmt.Errorf("insert mention %q: %w", mention.Term, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO usage_records (
			run_id, provider, model, input_tokens, output_tokens, total_tokens, tool_calls,
			created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, w.Provider, w.Model, w.Usage.InputTokens, w.Usage.OutputTokens,
		w.Usage.TotalTokens, w.Usage.ToolCalls, now,
	); err != nil {
		return 0, false, fmt.Errorf("insert usage: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return runID, true, nil
}

// RunFilter narrows a run query. StartUTC is inclusive, EndUTC exclusive.
type RunFilter struct {
	StartUTC string
	EndUTC   string
	Provider string
	Model    string
	Stage    string
}

// Runs reads runs with their question text and usage totals.
func (d *DB) Runs(ctx context.Context, filter RunFilter) ([]Run, error) {
	sqlText := `
		SELECT r.id, r.project_id, r.question_id, q.question_key, r.question_version,
		       COALESCE(qv.text, ''), COALESCE(qv.category, ''), r.repeat_index,
		       r.provider, r.model, r.api_endpoint_version, r.search_enabled,
		       r.measurement_method, r.stage, r.question_set_version, r.question_set_sha256,
		       r.plan_key, r.session, r.started_at_utc, r.completed_at_utc, r.status,
		       r.citation_observability, r.cited_own, r.citation_count, r.brand_mentioned,
		r.tool_version, resp.input_tokens, resp.output_tokens, u.tool_calls
		FROM runs r
		JOIN questions q ON q.id = r.question_id
		LEFT JOIN question_versions qv ON qv.question_id = r.question_id AND qv.version = r.question_version
		LEFT JOIN responses resp ON resp.run_id = r.id
		LEFT JOIN usage_records u ON u.run_id = r.id
		WHERE 1 = 1`
	args := []any{}
	if filter.StartUTC != "" {
		sqlText += ` AND r.started_at_utc >= ?`
		args = append(args, filter.StartUTC)
	}
	if filter.EndUTC != "" {
		sqlText += ` AND r.started_at_utc < ?`
		args = append(args, filter.EndUTC)
	}
	if filter.Provider != "" {
		sqlText += ` AND r.provider = ?`
		args = append(args, filter.Provider)
	}
	if filter.Model != "" {
		sqlText += ` AND r.model = ?`
		args = append(args, filter.Model)
	}
	if filter.Stage != "" {
		sqlText += ` AND r.stage = ?`
		args = append(args, filter.Stage)
	}
	sqlText += ` ORDER BY r.started_at_utc, q.question_key, r.repeat_index`

	rows, err := d.sql.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := make([]Run, 0, 64)
	for rows.Next() {
		var run Run
		var searchEnabled, citedOwn, brandMentioned int
		var inputTokens, outputTokens, toolCalls sql.NullInt64
		if err := rows.Scan(
			&run.ID, &run.ProjectID, &run.QuestionID, &run.QuestionKey, &run.QuestionVersion,
			&run.QuestionText, &run.Category, &run.RepeatIndex,
			&run.Provider, &run.Model, &run.APIEndpointVersion, &searchEnabled,
			&run.MeasurementMethod, &run.Stage, &run.QuestionSetVersion, &run.QuestionSetSHA256,
			&run.PlanKey, &run.Session, &run.StartedAtUTC, &run.CompletedAtUTC, &run.Status,
			&run.CitationObservability, &citedOwn, &run.CitationCount, &brandMentioned,
			&run.ToolVersion, &inputTokens, &outputTokens, &toolCalls,
		); err != nil {
			return nil, err
		}
		run.SearchEnabled = searchEnabled == 1
		run.CitedOwn = citedOwn == 1
		run.BrandMentioned = brandMentioned == 1
		run.InputTokens = nullInt(inputTokens)
		run.OutputTokens = nullInt(outputTokens)
		run.ToolCalls = nullInt(toolCalls)
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func nullInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	number := int(value.Int64)
	return &number
}

// Responses reads response rows for the given runs (all runs when ids is empty).
func (d *DB) Responses(ctx context.Context, runIDs []int64) ([]ResponseRow, error) {
	sqlText := `SELECT run_id, answer_text, answer_sha256, raw_response_json, citation_observability,
		error_code, error_message, input_tokens, output_tokens, tool_usage FROM responses`
	args := []any{}
	if len(runIDs) > 0 {
		sqlText += ` WHERE run_id IN (` + placeholders(len(runIDs)) + `)`
		for _, id := range runIDs {
			args = append(args, id)
		}
	}
	sqlText += ` ORDER BY run_id`
	rows, err := d.sql.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var responses []ResponseRow
	for rows.Next() {
		var r ResponseRow
		var inputTokens, outputTokens sql.NullInt64
		if err := rows.Scan(&r.RunID, &r.AnswerText, &r.AnswerSHA256, &r.RawResponseJSON,
			&r.CitationObservability, &r.ErrorCode, &r.ErrorMessage, &inputTokens,
			&outputTokens, &r.ToolUsage); err != nil {
			return nil, err
		}
		r.InputTokens = nullInt(inputTokens)
		r.OutputTokens = nullInt(outputTokens)
		responses = append(responses, r)
	}
	return responses, rows.Err()
}

// Citations reads citation rows for the given runs.
func (d *DB) Citations(ctx context.Context, runIDs []int64) ([]CitationRow, error) {
	sqlText := `SELECT run_id, response_id, position, raw_url, normalized_url, dedupe_key,
		hostname, source_type, is_own, malformed FROM citations`
	args := []any{}
	if len(runIDs) > 0 {
		sqlText += ` WHERE run_id IN (` + placeholders(len(runIDs)) + `)`
		for _, id := range runIDs {
			args = append(args, id)
		}
	}
	sqlText += ` ORDER BY run_id, position`
	rows, err := d.sql.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var citations []CitationRow
	for rows.Next() {
		var c CitationRow
		var hostname sql.NullString
		var isOwn, malformed int
		if err := rows.Scan(&c.RunID, &c.ResponseID, &c.Position, &c.RawURL, &c.NormalizedURL,
			&c.DedupeKey, &hostname, &c.SourceType, &isOwn, &malformed); err != nil {
			return nil, err
		}
		if hostname.Valid {
			value := hostname.String
			c.Hostname = &value
		}
		c.IsOwn = isOwn == 1
		c.Malformed = malformed == 1
		citations = append(citations, c)
	}
	return citations, rows.Err()
}

// Mentions reads mention rows for the given runs.
func (d *DB) Mentions(ctx context.Context, runIDs []int64) ([]MentionRow, error) {
	sqlText := `SELECT run_id, response_id, term, kind, matched_text, occurrences, ambiguous FROM mentions`
	args := []any{}
	if len(runIDs) > 0 {
		sqlText += ` WHERE run_id IN (` + placeholders(len(runIDs)) + `)`
		for _, id := range runIDs {
			args = append(args, id)
		}
	}
	sqlText += ` ORDER BY run_id, term`
	rows, err := d.sql.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mentions []MentionRow
	for rows.Next() {
		var m MentionRow
		var ambiguous int
		if err := rows.Scan(&m.RunID, &m.ResponseID, &m.Term, &m.Kind, &m.MatchedText, &m.Occurrences, &ambiguous); err != nil {
			return nil, err
		}
		m.Ambiguous = ambiguous == 1
		mentions = append(mentions, m)
	}
	return mentions, rows.Err()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// Counts returns row counts for status output.
func (d *DB) Counts(ctx context.Context) (Counts, error) {
	var counts Counts
	tables := []struct {
		name string
		dest *int
	}{
		{"projects", &counts.Projects},
		{"questions", &counts.Questions},
		{"question_versions", &counts.QuestionVersions},
		{"question_sets", &counts.QuestionSets},
		{"competitors", &counts.Competitors},
		{"runs", &counts.Runs},
		{"responses", &counts.Responses},
		{"citations", &counts.Citations},
		{"mentions", &counts.Mentions},
	}
	for _, table := range tables {
		if err := d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table.name).Scan(table.dest); err != nil {
			return counts, err
		}
	}
	if err := d.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM v_runs_valid`).Scan(&counts.RunsValid); err != nil {
		return counts, err
	}
	return counts, nil
}
