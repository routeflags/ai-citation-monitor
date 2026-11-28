package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/routeflags/ai-citation-monitor/internal/analysis"
	"github.com/routeflags/ai-citation-monitor/internal/i18n"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

type compareOptions struct {
	period         string
	baseline       string
	provider       string
	model          string
	stage          string
	comparableOnly bool
}

func (a *App) compareCommand() *cobra.Command {
	opts := compareOptions{}
	cmd := &cobra.Command{
		Use:   "compare",
		Short: "Compare two periods (citation rate and mention rate per group)",
		Long: `Compare two periods per provider x model x stage.

Differences are in percentage points. When the two sides are not the same
measurement (different endpoint version, question set or tool version) the row
says so instead of subtracting, and --comparable-only hides those rows.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runCompare(cmd, opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.period, "period", "30d", "current period")
	flags.StringVar(&opts.baseline, "baseline", "previous", "baseline period (or \"previous\" for the same length before)")
	flags.StringVar(&opts.provider, "provider", "", "only this provider")
	flags.StringVar(&opts.model, "model", "", "only this model")
	flags.StringVar(&opts.stage, "stage", "", "only this stage")
	flags.BoolVar(&opts.comparableOnly, "comparable-only", false, "hide rows whose conditions differ")
	return cmd
}

func (a *App) runCompare(cmd *cobra.Command, opts compareOptions) error {
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
	var baseline analysis.Period
	if opts.baseline == "previous" || opts.baseline == "" {
		previous, ok := analysis.PreviousPeriod(period)
		if !ok {
			return failf(ExitConfig, "period_invalid", "--baseline previous needs a bounded period")
		}
		baseline = previous
	} else {
		baseline, err = analysis.ParsePeriod(opts.baseline, a.NowUTC())
		if err != nil {
			return failf(ExitConfig, "period_invalid", "%v", err)
		}
	}

	filter := sqlite.RunFilter{Provider: opts.provider, Model: opts.model, Stage: opts.stage}
	baseResult, err := analysis.LoadPeriod(ctx, db, baseline, filter, analysisOpts)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}
	currentResult, err := analysis.LoadPeriod(ctx, db, period, filter, analysisOpts)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}

	rows := analysis.Compare(baseResult, currentResult)
	if opts.comparableOnly {
		filtered := make([]analysis.CompareRow, 0, len(rows))
		for _, row := range rows {
			if row.Comparable {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}

	if a.JSON {
		return a.PrintJSON(map[string]any{
			"baseline": baseline, "period": period, "rows": rows,
		})
	}
	s := a.styles()
	fmt.Fprintf(a.Out, "baseline: %s\nperiod:   %s\n\n", baseline.Label, period.Label)
	if len(rows) == 0 {
		fmt.Fprintln(a.Out, "no comparable groups (no runs on either side)")
		return nil
	}
	fmt.Fprintln(a.Out, s.header("%-8s %-28s %-9s %-18s %-18s %-10s %-18s %-18s %-10s %s",
		"provider", "model", "stage", "cited (base)", "cited (now)", "delta", "brand (base)", "brand (now)", "delta", "note"))
	for _, row := range rows {
		fmt.Fprintf(a.Out, "%-8s %-28s %-9s %-18s %-18s %s %-18s %-18s %s %s\n",
			row.Provider, row.Model, nonEmpty(row.Stage, "—"),
			row.CitedA.Display(i18n.EN), row.CitedB.Display(i18n.EN), s.delta(row.CitedDelta, row.CitedDeltaText, 10),
			row.BrandA.Display(i18n.EN), row.BrandB.Display(i18n.EN), s.delta(row.BrandDelta, row.BrandDeltaText, 10), row.Note)
	}
	return nil
}

type trendOptions struct {
	period   string
	interval string
	provider string
	model    string
	stage    string
}

func (a *App) trendsCommand() *cobra.Command {
	opts := trendOptions{}
	cmd := &cobra.Command{
		Use:   "trends",
		Short: "Show the time series of each group (daily / weekly / monthly)",
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
			series, err := analysis.Trends(result.Runs, opts.interval)
			if err != nil {
				return failf(ExitConfig, "interval_invalid", "%v", err)
			}
			if a.JSON {
				return a.PrintJSON(map[string]any{"period": period, "interval": opts.interval, "series": series})
			}
			fmt.Fprintf(a.Out, "period: %s  interval: %s\n\n", period.Label, opts.interval)
			if len(series) == 0 {
				fmt.Fprintln(a.Out, "no runs in this period (—（未測定）)")
				return nil
			}
			s := a.styles()
			for _, one := range series {
				fmt.Fprintf(a.Out, "%s / %s / %s\n", one.Provider, one.Model, nonEmpty(one.Stage, "—"))
				fmt.Fprintln(a.Out, s.header("  %-12s %6s %6s %6s %6s %-22s %-12s %-22s %s",
					"bucket", "runs", "ok", "fail", "unk", "cited", "delta", "brand", "note"))
				for _, point := range one.Points {
					fmt.Fprintf(a.Out, "  %-12s %6d %6d %6d %6d %-22s %s %-22s %s\n",
						point.Bucket, point.RunsTotal, point.RunsValid, point.RunsError, point.RunsUnknown,
						point.CitedOwn.Display(i18n.EN), s.delta(point.CitedDelta, point.CitedDeltaText, 12), point.BrandMentioned.Display(i18n.EN), point.Note)
				}
				fmt.Fprintln(a.Out)
			}
			return nil
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.period, "period", "90d", "period")
	flags.StringVar(&opts.interval, "interval", "weekly", "daily, weekly or monthly")
	flags.StringVar(&opts.provider, "provider", "", "only this provider")
	flags.StringVar(&opts.model, "model", "", "only this model")
	flags.StringVar(&opts.stage, "stage", "", "only this stage")
	return cmd
}
