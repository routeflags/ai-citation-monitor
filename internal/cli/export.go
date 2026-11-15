package cli

import (
	"bufio"
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
			ctx := cmd.Context()
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

			period, err := analysis.ParsePeriod(opts.period, a.NowUTC())
			if err != nil {
				return failf(ExitConfig, "period_invalid", "%v", err)
			}
			filter := sqlite.RunFilter{
				StartUTC: period.StartUTC, EndUTC: period.EndUTC,
				Provider: opts.provider, Model: opts.model, Stage: opts.stage,
			}
			runs, err := db.Runs(ctx, filter)
			if err != nil {
				return failf(ExitConfig, "db_unavailable", "%v", err)
			}
			ids := make([]int64, 0, len(runs))
			for _, run := range runs {
				ids = append(ids, run.ID)
			}
			responses, err := db.Responses(ctx, ids)
			if err != nil {
				return failf(ExitConfig, "db_unavailable", "%v", err)
			}
			citations, err := db.Citations(ctx, ids)
			if err != nil {
				return failf(ExitConfig, "db_unavailable", "%v", err)
			}
			rows := export.BuildRows(runs, responses, citations)

			var writer *bufio.Writer
			var file *os.File
			if opts.out != "" {
				file, err = os.Create(opts.out)
				if err != nil {
					return failf(ExitConfig, "export_failed", "cannot write %s: %v", opts.out, err)
				}
				defer file.Close()
				writer = bufio.NewWriter(file)
			} else {
				writer = bufio.NewWriter(a.Out)
			}

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
			if err := writer.Flush(); err != nil {
				return failf(ExitConfig, "export_failed", "%v", err)
			}
			if opts.out != "" {
				fmt.Fprintf(a.Err, "exported %d runs to %s\n", len(rows), opts.out)
			}
			return nil
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

func (a *App) verifyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check the database against itself (missing answers, broken references)",
		Long: `Check the evidence the database is supposed to hold.

The answer text and the raw response live in the database, so "the evidence was
lost" now means "the database is not self-consistent": a run without a
responses row, a citation pointing at a missing response, a stored hash that no
longer matches the text, a successful run with an empty answer, and so on.

Exit 1 when anything is found, 0 when the database is consistent.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
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
				fmt.Fprintf(a.Out, "db: %s\n", db.Path())
				fmt.Fprintf(a.Out, "runs=%d valid=%d responses=%d citations=%d mentions=%d\n",
					counts.Runs, counts.RunsValid, counts.Responses, counts.Citations, counts.Mentions)
				if len(issues) == 0 {
					fmt.Fprintln(a.Out, "OK: the database is self-consistent")
				} else {
					fmt.Fprintf(a.Out, "\n%-32s %-8s %s\n", "kind", "run", "detail")
					for _, issue := range issues {
						fmt.Fprintf(a.Out, "%-32s %-8d %s\n", issue.Kind, issue.RunID, issue.Detail)
					}
				}
			}

			if len(issues) > 0 {
				return failf(ExitGeneral, "evidence_inconsistent",
					"%d integrity problems: the records and their evidence do not line up", len(issues))
			}
			return nil
		},
	}
	return cmd
}
