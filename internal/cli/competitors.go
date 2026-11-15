package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
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
			domain := normalizeDomain(args[0])
			if domain == "" {
				return failf(ExitConfig, "usage", "a bare host name is required")
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
			if err := db.AddCompetitor(ctx, project.ID, domain, label); err != nil {
				return failf(ExitConfig, "db_write_failed", "%v", err)
			}
			if a.JSON {
				return a.PrintJSON(map[string]any{"domain": domain, "label": label})
			}
			fmt.Fprintf(a.Out, "competitor: %s\n", domain)
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
			for _, domain := range cfg.Competitors {
				combined[normalizeDomain(domain)] = "config"
			}
			for _, domain := range stored {
				combined[domain] = "database"
			}
			if a.JSON {
				return a.PrintJSON(map[string]any{"competitors": combined})
			}
			if len(combined) == 0 {
				fmt.Fprintln(a.Out, "no competitor domains registered")
				return nil
			}
			for domain, origin := range combined {
				fmt.Fprintf(a.Out, "%-40s %s\n", domain, origin)
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
			domain := normalizeDomain(args[0])
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
			removed, err := db.RemoveCompetitor(ctx, project.ID, domain)
			if err != nil {
				return failf(ExitConfig, "db_write_failed", "%v", err)
			}
			inConfig := false
			for _, configured := range cfg.Competitors {
				if normalizeDomain(configured) == domain {
					inConfig = true
				}
			}
			if a.JSON {
				return a.PrintJSON(map[string]any{"domain": domain, "removed": removed, "in_config": inConfig})
			}
			if removed {
				fmt.Fprintf(a.Out, "removed: %s\n", domain)
			} else {
				fmt.Fprintf(a.Out, "not in the database: %s\n", domain)
			}
			if inConfig {
				fmt.Fprintf(a.Out, "note: %s is also listed in %s; remove it there to stop counting it\n", domain, cfg.Path)
			}
			return nil
		},
	}
}

func normalizeDomain(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	trimmed = strings.TrimPrefix(trimmed, "https://")
	trimmed = strings.TrimPrefix(trimmed, "http://")
	if index := strings.IndexAny(trimmed, "/"); index >= 0 {
		trimmed = trimmed[:index]
	}
	return strings.TrimSuffix(trimmed, ".")
}
