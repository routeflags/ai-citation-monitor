package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/routeflags/ai-citation-monitor/internal/analysis"
	"github.com/routeflags/ai-citation-monitor/internal/export"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

type exportOptions struct {
	format   string
	out      string
	period   string
	provider string
	model    string
	stage    string
}

func (a *App) exportCommand() *cobra.Command {
	opts := exportOptions{}
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export the records as CSV, JSON or JSONL",
		Long: `Export the recorded runs.

CSV is one flat row per run, JSONL is one object per run (with its citations),
JSON is one document with metadata. The database stays the source of truth:
an export can always be regenerated.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runExport(cmd.Context(), opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.format, "format", "csv", "csv, json or jsonl")
	flags.StringVar(&opts.out, "out", "", "write to this file (default: stdout)")
	flags.StringVar(&opts.period, "period", "all", "period to export (default: all)")
	flags.StringVar(&opts.provider, "provider", "", "only this provider")
	flags.StringVar(&opts.model, "model", "", "only this model")
	flags.StringVar(&opts.stage, "stage", "", "only this stage")
	return cmd
}

// runExport reads the records of the requested period and writes them to the
// requested format and destination.
func (a *App) runExport(ctx context.Context, opts exportOptions) error {
	cfg, err := a.LoadConfig()
	if err != nil {
		return err
	}
	db, err := a.OpenDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := a.RequireProject(ctx, db); err != nil {
		return err
	}

	period, rows, err := a.loadExport(ctx, db, opts)
	if err != nil {
		return err
	}
	writer, release, err := a.exportWriter(opts.out)
	if err != nil {
		return err
	}
	defer release()
	if err := writeExport(writer, opts, period, rows); err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return failf(ExitConfig, "export_failed", "%v", err)
	}
	if opts.out != "" {
		fmt.Fprintf(a.Err, "exported %d runs to %s\n", len(rows), opts.out)
	}
	return nil
}

// loadExport reads the runs one export covers, with each run's evidence.
func (a *App) loadExport(ctx context.Context, db *sqlite.DB, opts exportOptions) (analysis.Period, []export.Row, error) {
	period, err := analysis.ParsePeriod(opts.period, a.NowUTC())
	if err != nil {
		return analysis.Period{}, nil, failf(ExitConfig, "period_invalid", "%v", err)
	}
	filter := sqlite.RunFilter{
		StartUTC: period.StartUTC, EndUTC: period.EndUTC,
		Provider: opts.provider, Model: opts.model, Stage: opts.stage,
	}
	runs, err := db.Runs(ctx, filter)
	if err != nil {
		return analysis.Period{}, nil, failf(ExitConfig, "db_unavailable", "%v", err)
	}
	ids := make([]int64, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.ID)
	}
	responses, err := db.Responses(ctx, ids)
	if err != nil {
		return analysis.Period{}, nil, failf(ExitConfig, "db_unavailable", "%v", err)
	}
	citations, err := db.Citations(ctx, ids)
	if err != nil {
		return analysis.Period{}, nil, failf(ExitConfig, "db_unavailable", "%v", err)
	}
	return period, export.BuildRows(runs, responses, citations), nil
}

// exportWriter is the destination: a file when --out names one, stdout otherwise.
// The returned release func closes the file and is a no-op for stdout, so the caller
// can always defer it.
func (a *App) exportWriter(path string) (*bufio.Writer, func(), error) {
	if path == "" {
		return bufio.NewWriter(a.Out), func() {}, nil
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, failf(ExitConfig, "export_failed", "cannot write %s: %v", path, err)
	}
	return bufio.NewWriter(file), func() { file.Close() }, nil
}

// writeExport writes the rows in the requested format. An unknown format is a usage
// error, not a write failure.
func writeExport(writer *bufio.Writer, opts exportOptions, period analysis.Period, rows []export.Row) error {
	var err error
	switch opts.format {
	case "csv":
		err = export.WriteCSV(writer, rows)
	case "jsonl":
		err = export.WriteJSONL(writer, rows)
	case "json":
		err = export.WriteJSON(writer, map[string]any{
			"period": period, "provider": opts.provider, "model": opts.model, "stage": opts.stage,
			"total": len(rows),
		}, rows)
	default:
		return failf(ExitConfig, "usage", "--format must be csv, json or jsonl (got %q)", opts.format)
	}
	if err != nil {
		return failf(ExitConfig, "export_failed", "%v", err)
	}
	return nil
}

func (a *App) verifyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Check the database against itself (missing answers, broken references)",
		Long: `Check the evidence the database is supposed to hold.

The answer text and the raw response live in the database, so "the evidence was
lost" now means "the database is not self-consistent": a run without a
responses row, a citation pointing at a missing response, a stored hash that no
longer matches the text, a successful run with an empty answer, and so on.

Exit 1 when anything is found, 0 when the database is consistent.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runVerify(cmd.Context())
		},
	}
}

// runVerify reads the counts and the integrity issues, prints them in the requested
// shape and fails the command when any issue was found.
func (a *App) runVerify(ctx context.Context) error {
	cfg, err := a.LoadConfig()
	if err != nil {
		return err
	}
	db, err := a.OpenDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := a.RequireProject(ctx, db); err != nil {
		return err
	}
	counts, err := db.Counts(ctx)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}
	issues, err := db.IntegrityIssues(ctx)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}

	if a.JSON {
		if err := a.PrintJSON(map[string]any{"counts": counts, "issues": issues, "total": len(issues)}); err != nil {
			return err
		}
	} else {
		a.printVerification(db, counts, issues)
	}
	if len(issues) > 0 {
		return failf(ExitGeneral, "evidence_inconsistent",
			"%d integrity problems: the records and their evidence do not line up", len(issues))
	}
	return nil
}

// printVerification writes the counts always and the problem table only when there is
// one: an empty table would read as a check that ran and found nothing to say.
func (a *App) printVerification(db *sqlite.DB, counts sqlite.Counts, issues []sqlite.Issue) {
	fmt.Fprintf(a.Out, "db: %s\n", db.Path())
	fmt.Fprintf(a.Out, "runs=%d valid=%d responses=%d citations=%d mentions=%d\n",
		counts.Runs, counts.RunsValid, counts.Responses, counts.Citations, counts.Mentions)
	if len(issues) == 0 {
		fmt.Fprintln(a.Out, "OK: the database is self-consistent")
		return
	}
	fmt.Fprintf(a.Out, "\n%s\n", a.styles().header("%-32s %-8s %s", "kind", "run", "detail"))
	for _, issue := range issues {
		fmt.Fprintf(a.Out, "%-32s %-8d %s\n", issue.Kind, issue.RunID, issue.Detail)
	}
}
