// Package cli wires the commands. All I/O decisions live here: human tables go to
// stdout, logs and progress go to stderr, and --json makes stdout JSON only.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/routeflags/ai-citation-monitor/internal/analysis"
	"github.com/routeflags/ai-citation-monitor/internal/config"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

// App carries the dependencies so tests can replace stdin/stdout and the clock.
type App struct {
	ConfigPath string
	DBPath     string
	JSON       bool
	Quiet      bool
	Verbose    bool
	Out        io.Writer
	Err        io.Writer
	In         io.Reader
	Now        func() time.Time
}

// NewApp builds the production app.
func NewApp() *App {
	return &App{
		ConfigPath: "aicm.toml",
		Out:        os.Stdout,
		Err:        os.Stderr,
		In:         os.Stdin,
		Now:        time.Now,
	}
}

// Execute runs the CLI with the process arguments and returns the exit code.
func Execute() int { return NewApp().Run(os.Args[1:]) }

// Run executes the command tree with explicit arguments. Tests use it to get the
// exit code without spawning a process.
func (a *App) Run(args []string) int {
	root := a.Root()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		exit := asExit(err)
		writeError(a.Err, exit, a.JSON)
		return exit.Code
	}
	return ExitOK
}

// Root builds the command tree.
func (a *App) Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "aicm",
		Short: "Measure AI answers: citations, brand mentions and question coverage",
		Long: `aicm measures whether AI answers cite your site.

It asks a fixed question set of a provider's official search-enabled API,
records the answer together with the conditions of the measurement, and
aggregates the results into rates that always carry their denominator.

The numbers are a sample: they are not the general ChatGPT / Gemini UI and they
are not every user's answer. Every report says so.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.ConfigPath, "config", "aicm.toml", "path to the site configuration (TOML)")
	root.PersistentFlags().StringVar(&a.DBPath, "db", "", "path to the SQLite database (overrides the config)")
	root.PersistentFlags().BoolVar(&a.JSON, "json", false, "write machine readable JSON to stdout and logs to stderr")
	root.PersistentFlags().BoolVar(&a.Quiet, "quiet", false, "do not print progress")
	root.PersistentFlags().BoolVar(&a.Verbose, "verbose", false, "print request level detail to stderr")

	root.AddCommand(
		a.initCommand(),
		a.questionsCommand(),
		a.runCommand(),
		a.reportCommand(),
		a.competitorsCommand(),
		a.compareCommand(),
		a.trendsCommand(),
		a.exportCommand(),
		a.verifyCommand(),
		a.versionCommand(),
	)
	return root
}

// LoadConfig reads the configuration and prints warnings to stderr.
func (a *App) LoadConfig() (*config.Config, error) {
	path := a.ConfigPath
	if path == "" {
		path = "aicm.toml"
	}
	cfg, warnings, err := config.Load(path)
	if err != nil {
		return nil, failf(ExitConfig, "config_invalid", "%v", err)
	}
	for _, warning := range warnings {
		if !a.Quiet {
			fmt.Fprintf(a.Err, "warning: %s\n", warning)
		}
	}
	if a.DBPath != "" {
		cfg.DB = a.DBPath
	}
	return cfg, nil
}

// OpenDB opens (creating if needed) the database named by the config.
func (a *App) OpenDB(cfg *config.Config) (*sqlite.DB, error) {
	db, err := sqlite.Open(cfg.DB)
	if err != nil {
		return nil, failf(ExitConfig, "db_unavailable", "cannot open database %s: %v", cfg.DB, err)
	}
	return db, nil
}

// RequireProject returns the single project row, or a configuration failure.
func (a *App) RequireProject(ctx context.Context, db *sqlite.DB) (*sqlite.Project, error) {
	project, err := db.Project(ctx)
	if err != nil {
		if errors.Is(err, sqlite.ErrNoProject) {
			return nil, failf(ExitConfig, "project_missing",
				"this database has no project yet; run `aicm init` (db: %s)", db.Path())
		}
		return nil, failf(ExitConfig, "db_unavailable", "%v", err)
	}
	return project, nil
}

// Now is the clock (overridable in tests).
func (a *App) NowUTC() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}

// PrintJSON writes one JSON document to stdout.
func (a *App) PrintJSON(value any) error {
	encoder := json.NewEncoder(a.Out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

// Logf prints progress to stderr unless --quiet.
func (a *App) Logf(format string, args ...any) {
	if a.Quiet {
		return
	}
	fmt.Fprintf(a.Err, format+"\n", args...)
}

// Verbosef prints request level detail to stderr when --verbose.
func (a *App) Verbosef(format string, args ...any) {
	if !a.Verbose || a.Quiet {
		return
	}
	fmt.Fprintf(a.Err, format+"\n", args...)
}

// analysisOptions merges the stored competitors with the configured ones.
func (a *App) analysisOptions(ctx context.Context, db *sqlite.DB, cfg *config.Config, project *sqlite.Project) (analysis.Options, error) {
	competitors := map[string]bool{}
	for _, domain := range cfg.Competitors {
		if trimmed := strings.ToLower(strings.TrimSpace(domain)); trimmed != "" {
			competitors[trimmed] = true
		}
	}
	stored, err := db.Competitors(ctx, project.ID)
	if err != nil {
		return analysis.Options{}, err
	}
	for _, domain := range stored {
		competitors[domain] = true
	}
	list := make([]string, 0, len(competitors))
	for domain := range competitors {
		list = append(list, domain)
	}
	sort.Strings(list)

	questions, err := db.Questions(ctx, project.ID)
	if err != nil {
		return analysis.Options{}, err
	}
	roster := make([]string, 0, len(questions))
	for _, question := range questions {
		roster = append(roster, question.Key)
	}

	ownDomain := project.OwnDomain
	if ownDomain == "" {
		ownDomain = cfg.OwnDomain
	}
	return analysis.Options{
		OwnDomain:   ownDomain,
		Subdomains:  project.Subdomains,
		Competitors: list,
		Roster:      roster,
	}, nil
}

// providerFilter splits a comma separated list.
func splitList(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// unknownProviderError names the providers this build understands.
func unknownProviderError(name string) error {
	return failf(ExitConfig, "provider_unknown",
		"unknown provider %q (this build supports: %s)", name, strings.Join(config.ProviderNames(), ", "))
}
