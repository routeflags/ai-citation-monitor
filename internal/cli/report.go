package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/routeflags/ai-citation-monitor/internal/analysis"
	"github.com/routeflags/ai-citation-monitor/internal/i18n"
	"github.com/routeflags/ai-citation-monitor/internal/reporting"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

type reportOptions struct {
	period   string
	provider string
	model    string
	stage    string
	compare  string
	out      string
	stdout   bool
}

func (a *App) reportCommand() *cobra.Command {
	opts := reportOptions{}
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Render a Markdown report for a period",
		Long: `Render the Markdown report.

The file name carries the period and every group condition (provider, model,
stage, question set hash), so reports of different conditions never overwrite
each other. A rate with no denominator is written as "—（未測定）", never 0.0%.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runReport(cmd, opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.period, "period", "30d", "period: 30d, 2026-11-02:2026-11-08, 2026-11-12 or all")
	flags.StringVar(&opts.provider, "provider", "", "only this provider")
	flags.StringVar(&opts.model, "model", "", "only this model")
	flags.StringVar(&opts.stage, "stage", "", "only this stage")
	flags.StringVar(&opts.compare, "compare", "", "also compare with this period (or \"previous\")")
	flags.StringVar(&opts.out, "out", "", "directory for the report (default from the config)")
	flags.BoolVar(&opts.stdout, "stdout", false, "write the report to stdout instead of a file")
	return cmd
}

func (a *App) runReport(cmd *cobra.Command, opts reportOptions) error {
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
	project, err := a.RequireProject(ctx, db)
	if err != nil {
		return err
	}
	analysisOpts, err := a.analysisOptions(ctx, db, cfg, project)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}

	period, err := analysis.ParsePeriod(opts.period, a.NowUTC())
	if err != nil {
		return failf(ExitConfig, "period_invalid", "%v", err)
	}
	filter := sqlite.RunFilter{Provider: opts.provider, Model: opts.model, Stage: opts.stage}
	result, err := analysis.LoadPeriod(ctx, db, period, filter, analysisOpts)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}

	var compareResult *analysis.Result
	if opts.compare != "" {
		var comparePeriod analysis.Period
		if opts.compare == "previous" {
			previous, ok := analysis.PreviousPeriod(period)
			if !ok {
				return failf(ExitConfig, "period_invalid", "--compare previous needs a bounded period")
			}
			comparePeriod = previous
		} else {
			comparePeriod, err = analysis.ParsePeriod(opts.compare, a.NowUTC())
			if err != nil {
				return failf(ExitConfig, "period_invalid", "%v", err)
			}
		}
		compareResult, err = analysis.LoadPeriod(ctx, db, comparePeriod, filter, analysisOpts)
		if err != nil {
			return failf(ExitConfig, "db_unavailable", "%v", err)
		}
	}

	markdown := reporting.Build(result, reporting.Options{
		Config:      cfg,
		Language:    i18n.Parse(cfg.Report.Language),
		Compare:     compareResult,
		GeneratedAt: a.NowUTC().Format("2006-01-02T15:04:05Z"),
	})

	fileName := reporting.FileName(period, result.Conditions)
	if opts.stdout {
		if a.JSON {
			return a.PrintJSON(map[string]any{"period": period, "groups": result.Groups, "totals": result.Totals, "conditions": result.Conditions})
		}
		fmt.Fprint(a.Out, markdown)
		return nil
	}

	directory := opts.out
	if directory == "" {
		directory = cfg.ReportDir
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return failf(ExitConfig, "report_write_failed", "cannot create %s: %v", directory, err)
	}
	path := filepath.Join(directory, fileName)
	if err := os.WriteFile(path, []byte(markdown), 0o644); err != nil {
		return failf(ExitConfig, "report_write_failed", "cannot write %s: %v", path, err)
	}

	if a.JSON {
		return a.PrintJSON(map[string]any{
			"path": path, "period": period, "groups": result.Groups,
			"totals": result.Totals, "conditions": result.Conditions,
		})
	}
	fmt.Fprintf(a.Out, "report: %s\n", path)
	if len(result.Groups) == 0 {
		fmt.Fprintf(a.Out, "note:   no runs in this period; the report says %s\n", i18n.EN.Unmeasured())
	}
	return nil
}
