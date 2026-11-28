package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/routeflags/ai-citation-monitor/internal/domain"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

// configuredCompetitors canonicalises the configured competitor list the same way
// `competitors add`, the config validator and report-time matching do, dropping empty
// values and duplicates in a fixed order. The runner classifies citations when it
// records them, so the list has to be canonical before it gets there too: otherwise a
// URL written in the config would be "other" in the record and "competitor" in the
// report (D26).
func configuredCompetitors(configured []string) []string {
	seen := map[string]bool{}
	names := make([]string, 0, len(configured))
	for _, value := range configured {
		canonical := domain.CanonicalConfiguredDomain(value)
		if canonical == "" || seen[canonical] {
			continue
		}
		seen[canonical] = true
		names = append(names, canonical)
	}
	sort.Strings(names)
	return names
}

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
			competitor := domain.CanonicalConfiguredDomain(args[0])
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
			return a.runCompetitorsList(cmd.Context())
		},
	}
}

// competitorList is one project's listing: every configured and stored domain with
// the source it came from, plus the ones that must be fixed (D26).
type competitorList struct {
	sources map[string]string
	names   []string
	invalid []string
}

// listCompetitors merges the configured and stored domains by their canonical form.
// A stored row is kept, not dropped: the table is the management record, and a report
// still applies the row until the config or the row changes (D26).
func listCompetitors(configured, stored []string) competitorList {
	sources := map[string]string{}
	for _, value := range configured {
		sources[domain.CanonicalConfiguredDomain(value)] = "config"
	}
	for _, value := range stored {
		sources[domain.CanonicalConfiguredDomain(value)] = "database"
	}
	list := competitorList{
		sources: sources,
		names:   make([]string, 0, len(sources)),
		invalid: make([]string, 0, len(sources)),
	}
	for competitor := range sources {
		list.names = append(list.names, competitor)
		// A row recorded before either check existed is not deleted: it is shown with
		// the reason it must be fixed, because a report still applies it.
		if domain.IsPublicSuffix(competitor) {
			list.invalid = append(list.invalid, competitor)
		}
	}
	sort.Strings(list.names)
	sort.Strings(list.invalid)
	return list
}

func (a *App) runCompetitorsList(ctx context.Context) error {
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
	stored, err := db.Competitors(ctx, project.ID)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}
	list := listCompetitors(cfg.Competitors, stored)
	if a.JSON {
		return a.PrintJSON(map[string]any{"competitors": list.sources, "invalid": list.invalid})
	}
	a.printCompetitors(list)
	return nil
}

// printCompetitors prints one line per domain in sorted order. An empty list says so
// rather than printing nothing.
func (a *App) printCompetitors(list competitorList) {
	if len(list.sources) == 0 {
		fmt.Fprintln(a.Out, "no competitor domains registered")
		return
	}
	for _, competitor := range list.names {
		a.printCompetitor(competitor, list.sources[competitor])
	}
}

func (a *App) printCompetitor(competitor, source string) {
	if domain.IsPublicSuffix(competitor) {
		fmt.Fprintf(a.Out, "%-40s %-9s public suffix: every site under it would count as this competitor\n",
			competitor, source)
		return
	}
	fmt.Fprintf(a.Out, "%-40s %s\n", competitor, source)
}

func (a *App) competitorsRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <domain>",
		Short: "Remove a registered competitor domain",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.removeCompetitor(cmd.Context(), domain.CanonicalConfiguredDomain(args[0]))
		},
	}
}

func (a *App) removeCompetitor(ctx context.Context, competitor string) error {
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
	stored, err := db.Competitors(ctx, project.ID)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}
	removed, err := removeStoredCompetitors(ctx, db, project.ID, competitor, stored)
	if err != nil {
		return err
	}
	inConfig := competitorInConfig(cfg.Competitors, competitor)
	if a.JSON {
		return a.PrintJSON(map[string]any{"domain": competitor, "removed": removed, "in_config": inConfig})
	}
	a.printRemoval(competitor, cfg.Path, removed, inConfig)
	return nil
}

// removeStoredCompetitors deletes the requested domain and every stored spelling
// that canonicalises to it. A row recorded before the canonicaliser existed may
// hold the Unicode form while the input is now punycode (or the other way round).
func removeStoredCompetitors(ctx context.Context, db *sqlite.DB, projectID int64, competitor string, stored []string) (bool, error) {
	targets := map[string]bool{competitor: true}
	for _, storedDomain := range stored {
		if domain.CanonicalConfiguredDomain(storedDomain) == competitor {
			targets[storedDomain] = true
		}
	}
	removed := false
	for target := range targets {
		one, err := db.RemoveCompetitor(ctx, projectID, target)
		if err != nil {
			return false, failf(ExitConfig, "db_write_failed", "%v", err)
		}
		removed = removed || one
	}
	return removed, nil
}

func competitorInConfig(configured []string, competitor string) bool {
	for _, value := range configured {
		if domain.CanonicalConfiguredDomain(value) == competitor {
			return true
		}
	}
	return false
}

func (a *App) printRemoval(competitor, configPath string, removed, inConfig bool) {
	if removed {
		fmt.Fprintf(a.Out, "removed: %s\n", competitor)
	} else {
		fmt.Fprintf(a.Out, "not in the database: %s\n", competitor)
	}
	if inConfig {
		fmt.Fprintf(a.Out, "note: %s is also listed in %s; remove it there to stop counting it\n", competitor, configPath)
	}
}
