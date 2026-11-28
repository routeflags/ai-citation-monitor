package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/routeflags/ai-citation-monitor/internal/domain"
)

func (a *App) competitorsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "competitors",
		Short: "Manage competitor domains (add / list / remove)",
		Long: `Manage competitor domains.

A competitor does not have to be verified by ownership: it is a domain you want
to compare against in the citations that AI answers happen to show.

The list is applied at report time, so adding a competitor does not invalidate an
existing measurement.`,
	}
	cmd.AddCommand(a.competitorsAddCommand(), a.competitorsListCommand(), a.competitorsRemoveCommand())
	return cmd
}

func (a *App) competitorsAddCommand() *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "add <domain>",
		Short: "Register a competitor domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			competitor := normalizeDomain(args[0])
			if competitor == "" {
				return failf(ExitConfig, "usage", "a bare host name is required")
			}
			// A public suffix is shared by unrelated organisations, and a competitor
			// row now stands for its whole site (D23): registering one would count
			// every customer of that suffix as that competitor.
			if domain.IsPublicSuffix(competitor) {
				return failf(ExitConfig, "usage", "%s is a public suffix, so every site under it would count as that competitor; register a registrable domain instead", competitor)
			}
			cfg, err := a.LoadConfig()
			if err != nil {
				return err
			}
			db, err := a.OpenDB(cfg)
			if err != nil {
				return err
			}
			defer db.Close()
			ctx := cmd.Context()
			project, err := a.RequireProject(ctx, db)
			if err != nil {
				return err
			}
			if err := db.AddCompetitor(ctx, project.ID, competitor, label); err != nil {
				return failf(ExitConfig, "db_write_failed", "%v", err)
			}
			if a.JSON {
				return a.PrintJSON(map[string]any{"domain": competitor, "label": label})
			}
			fmt.Fprintf(a.Out, "competitor: %s\n", competitor)
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "human readable label")
	return cmd
}

func (a *App) competitorsListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered competitor domains",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.LoadConfig()
			if err != nil {
				return err
			}
			db, err := a.OpenDB(cfg)
			if err != nil {
				return err
			}
			defer db.Close()
			ctx := cmd.Context()
			project, err := a.RequireProject(ctx, db)
			if err != nil {
				return err
			}
			stored, err := db.Competitors(ctx, project.ID)
			if err != nil {
				return failf(ExitConfig, "db_unavailable", "%v", err)
			}
			combined := map[string]string{}
			for _, configured := range cfg.Competitors {
				combined[normalizeDomain(configured)] = "config"
			}
			for _, stored := range stored {
				combined[normalizeDomain(stored)] = "database"
			}
			if a.JSON {
				return a.PrintJSON(map[string]any{"competitors": combined})
			}
			if len(combined) == 0 {
				fmt.Fprintln(a.Out, "no competitor domains registered")
				return nil
			}
			names := make([]string, 0, len(combined))
			for competitor := range combined {
				names = append(names, competitor)
			}
			sort.Strings(names)
			for _, competitor := range names {
				fmt.Fprintf(a.Out, "%-40s %s\n", competitor, combined[competitor])
			}
			return nil
		},
	}
}

func (a *App) competitorsRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <domain>",
		Short: "Remove a registered competitor domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			competitor := normalizeDomain(args[0])
			cfg, err := a.LoadConfig()
			if err != nil {
				return err
			}
			db, err := a.OpenDB(cfg)
			if err != nil {
				return err
			}
			defer db.Close()
			ctx := cmd.Context()
			project, err := a.RequireProject(ctx, db)
			if err != nil {
				return err
			}
			stored, err := db.Competitors(ctx, project.ID)
			if err != nil {
				return failf(ExitConfig, "db_unavailable", "%v", err)
			}
			// A row recorded before the canonicaliser existed may hold the Unicode form
			// while the input is now punycode (or the other way round). Remove every
			// stored spelling that canonicalises to the requested domain.
			targets := map[string]bool{competitor: true}
			for _, storedDomain := range stored {
				if normalizeDomain(storedDomain) == competitor {
					targets[storedDomain] = true
				}
			}
			removed := false
			for target := range targets {
				one, err := db.RemoveCompetitor(ctx, project.ID, target)
				if err != nil {
					return failf(ExitConfig, "db_write_failed", "%v", err)
				}
				removed = removed || one
			}
			inConfig := false
			for _, configured := range cfg.Competitors {
				if normalizeDomain(configured) == competitor {
					inConfig = true
				}
			}
			if a.JSON {
				return a.PrintJSON(map[string]any{"domain": competitor, "removed": removed, "in_config": inConfig})
			}
			if removed {
				fmt.Fprintf(a.Out, "removed: %s\n", competitor)
			} else {
				fmt.Fprintf(a.Out, "not in the database: %s\n", competitor)
			}
			if inConfig {
				fmt.Fprintf(a.Out, "note: %s is also listed in %s; remove it there to stop counting it\n", competitor, cfg.Path)
			}
			return nil
		},
	}
}

// normalizeDomain reduces a configured or typed competitor to the form the
// measurement compares: lower case, no scheme or path, no trailing dot, and IDN
// labels in punycode. It is the same canonicalisation a citation host goes through
// (domain.CanonicalHost), so "Example.COM." and "例え.テスト" cannot fail to match
// the hosts they name.
func normalizeDomain(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	trimmed = strings.TrimPrefix(trimmed, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	if index := strings.IndexAny(trimmed, "/"); index >= 0 {
		trimmed = trimmed[:index]
	}
	return domain.CanonicalHost(trimmed)
}
