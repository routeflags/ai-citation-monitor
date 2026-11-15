// Package export writes the machine-readable views of the records.
//
// The database stays the source of truth; every export can be regenerated from
// it. JSONL is one run per line (with its citations), JSON is one document, CSV
// is one flat table for spreadsheets.
package export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

// Citation is one cited URL in an export row.
type Citation struct {
	Position      int     `json:"position"`
	RawURL        string  `json:"raw_url"`
	NormalizedURL string  `json:"normalized_url"`
	Hostname      *string `json:"hostname"`
	SourceType    string  `json:"source_type"`
	IsOwn         bool    `json:"is_own"`
	Malformed     bool    `json:"malformed"`
}

// Row is one run with everything a consumer needs, flat enough for CSV.
type Row struct {
	RunID                 int64      `json:"run_id"`
	StartedAtUTC          string     `json:"started_at_utc"`
	CompletedAtUTC        string     `json:"completed_at_utc"`
	Provider              string     `json:"provider"`
	Model                 string     `json:"model"`
	APIEndpointVersion    string     `json:"api_endpoint_version"`
	SearchEnabled         bool       `json:"search_enabled"`
	MeasurementMethod     string     `json:"measurement_method"`
	Stage                 string     `json:"stage"`
	QuestionKey           string     `json:"question_key"`
	QuestionVersion       string     `json:"question_version"`
	QuestionText          string     `json:"question_text"`
	Category              string     `json:"category"`
	RepeatIndex           int        `json:"repeat_index"`
	Status                string     `json:"status"`
	CitationObservability string     `json:"citation_observability"`
	CitedOwn              bool       `json:"cited_own"`
	CitationCount         int        `json:"citation_count"`
	BrandMentioned        bool       `json:"brand_mentioned"`
	InputTokens           *int       `json:"input_tokens"`
	OutputTokens          *int       `json:"output_tokens"`
	EstimatedCost         *float64   `json:"estimated_cost"`
	CostStatus            string     `json:"cost_status"`
	ErrorCode             string     `json:"error_code"`
	ErrorMessage          string     `json:"error_message"`
	AnswerSHA256          string     `json:"answer_sha256"`
	QuestionSetVersion    string     `json:"question_set_version"`
	QuestionSetSHA256     string     `json:"question_set_sha256"`
	ToolVersion           string     `json:"tool_version"`
	Session               string     `json:"session"`
	Citations             []Citation `json:"citations"`
}

// BuildRows joins runs with their response and citations.
func BuildRows(runs []sqlite.Run, responses []sqlite.ResponseRow, citations []sqlite.CitationRow) []Row {
	responseByRun := make(map[int64]sqlite.ResponseRow, len(responses))
	for _, response := range responses {
		responseByRun[response.RunID] = response
	}
	citationsByRun := make(map[int64][]sqlite.CitationRow, len(citations))
	for _, citation := range citations {
		citationsByRun[citation.RunID] = append(citationsByRun[citation.RunID], citation)
	}

	rows := make([]Row, 0, len(runs))
	for _, run := range runs {
		response := responseByRun[run.ID]
		row := Row{
			RunID:                 run.ID,
			StartedAtUTC:          run.StartedAtUTC,
			CompletedAtUTC:        run.CompletedAtUTC,
			Provider:              run.Provider,
			Model:                 run.Model,
			APIEndpointVersion:    run.APIEndpointVersion,
			SearchEnabled:         run.SearchEnabled,
			MeasurementMethod:     run.MeasurementMethod,
			Stage:                 run.Stage,
			QuestionKey:           run.QuestionKey,
			QuestionVersion:       run.QuestionVersion,
			QuestionText:          run.QuestionText,
			Category:              run.Category,
			RepeatIndex:           run.RepeatIndex,
			Status:                run.Status,
			CitationObservability: run.CitationObservability,
			CitedOwn:              run.CitedOwn,
			CitationCount:         run.CitationCount,
			BrandMentioned:        run.BrandMentioned,
			InputTokens:           run.InputTokens,
			OutputTokens:          run.OutputTokens,
			EstimatedCost:         response.EstimatedCost,
			CostStatus:            response.CostStatus,
			ErrorCode:             response.ErrorCode,
			ErrorMessage:          response.ErrorMessage,
			AnswerSHA256:          response.AnswerSHA256,
			QuestionSetVersion:    run.QuestionSetVersion,
			QuestionSetSHA256:     run.QuestionSetSHA256,
			ToolVersion:           run.ToolVersion,
			Session:               run.Session,
			Citations:             make([]Citation, 0, len(citationsByRun[run.ID])),
		}
		for _, citation := range citationsByRun[run.ID] {
			row.Citations = append(row.Citations, Citation{
				Position:      citation.Position,
				RawURL:        citation.RawURL,
				NormalizedURL: citation.NormalizedURL,
				Hostname:      citation.Hostname,
				SourceType:    citation.SourceType,
				IsOwn:         citation.IsOwn,
				Malformed:     citation.Malformed,
			})
		}
		rows = append(rows, row)
	}
	return rows
}

// WriteJSONL writes one JSON object per run.
func WriteJSONL(w io.Writer, rows []Row) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

// WriteJSON writes one document with metadata and the run rows.
func WriteJSON(w io.Writer, meta map[string]any, rows []Row) error {
	document := map[string]any{"runs": rows}
	for key, value := range meta {
		document[key] = value
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}

var csvHeader = []string{
	"run_id", "started_at_utc", "completed_at_utc", "provider", "model",
	"api_endpoint_version", "search_enabled", "measurement_method", "stage",
	"question_key", "question_version", "question_text", "category", "repeat_index",
	"status", "citation_observability", "cited_own", "citation_count", "brand_mentioned",
	"input_tokens", "output_tokens", "estimated_cost", "cost_status",
	"error_code", "error_message", "answer_sha256",
	"question_set_version", "question_set_sha256", "tool_version", "session",
}

// WriteCSV writes the flat table (one row per run).
func WriteCSV(w io.Writer, rows []Row) error {
	writer := csv.NewWriter(w)
	if err := writer.Write(csvHeader); err != nil {
		return err
	}
	for _, row := range rows {
		record := []string{
			strconv.FormatInt(row.RunID, 10),
			row.StartedAtUTC,
			row.CompletedAtUTC,
			row.Provider,
			row.Model,
			row.APIEndpointVersion,
			strconv.FormatBool(row.SearchEnabled),
			row.MeasurementMethod,
			row.Stage,
			row.QuestionKey,
			row.QuestionVersion,
			row.QuestionText,
			row.Category,
			strconv.Itoa(row.RepeatIndex),
			row.Status,
			row.CitationObservability,
			strconv.FormatBool(row.CitedOwn),
			strconv.Itoa(row.CitationCount),
			strconv.FormatBool(row.BrandMentioned),
			optionalInt(row.InputTokens),
			optionalInt(row.OutputTokens),
			optionalFloat(row.EstimatedCost),
			row.CostStatus,
			row.ErrorCode,
			row.ErrorMessage,
			row.AnswerSHA256,
			row.QuestionSetVersion,
			row.QuestionSetSHA256,
			row.ToolVersion,
			row.Session,
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func optionalInt(value *int) string {
	if value == nil {
		return ""
	}
	return strconv.Itoa(*value)
}

func optionalFloat(value *float64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%.6f", *value)
}
