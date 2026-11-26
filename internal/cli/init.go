package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/routeflags/ai-citation-monitor/internal/config"
	"github.com/routeflags/ai-citation-monitor/internal/domain"
	"github.com/routeflags/ai-citation-monitor/internal/providers"
	"github.com/routeflags/ai-citation-monitor/internal/providers/anthropic"
	"github.com/routeflags/ai-citation-monitor/internal/providers/grok"
	"github.com/routeflags/ai-citation-monitor/internal/providers/perplexity"
	"github.com/routeflags/ai-citation-monitor/internal/runner"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

type initOptions struct {
	nonInteractive bool
	siteID         string
	siteName       string
	ownDomain      string
	market         string
	language       string
	reportLanguage string
	brandTerms     []string
	competitors    []string
	dbPath         string
	reportDir      string
	out            string
	force          bool
	skipDB         bool
}

func (a *App) initCommand() *cobra.Command {
	opts := initOptions{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create aicm.toml and the database project row",
		Long: `Create the configuration file and the project row.

Credentials are never written: the configuration stores the NAME of the
environment variable that carries the key (for example OPENAI_API_KEY).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runInit(cmd, opts)
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&opts.nonInteractive, "non-interactive", false, "do not prompt; require the flags")
	flags.StringVar(&opts.siteID, "site-id", "", "site identifier (for example example-shop)")
	flags.StringVar(&opts.siteName, "site-name", "", "human readable site name")
	flags.StringVar(&opts.ownDomain, "own-domain", "", "your domain, host only (for example example.shop)")
	flags.StringVar(&opts.market, "market", config.DefaultMarket, "target market (for example ja-JP)")
	flags.StringVar(&opts.language, "language", config.DefaultLanguage, "answer language")
	flags.StringVar(&opts.reportLanguage, "report-language", "", "language of the generated report (default: the site language)")
	flags.StringSliceVar(&opts.brandTerms, "brand-terms", nil, "brand terms used for the mention rate")
	flags.StringSliceVar(&opts.competitors, "competitors", nil, "competitor domains to measure against")
	flags.StringVar(&opts.dbPath, "db-path", config.DefaultDBPath, "database path")
	flags.StringVar(&opts.reportDir, "report-dir", config.DefaultReportDir, "directory for generated reports")
	flags.StringVar(&opts.out, "out", "", "configuration file to write (default: the --config path)")
	flags.BoolVar(&opts.force, "force", false, "overwrite an existing configuration file")
	flags.BoolVar(&opts.skipDB, "skip-db", false, "write the configuration but do not create the database")
	return cmd
}

func (a *App) runInit(cmd *cobra.Command, opts initOptions) error {
	ctx := cmd.Context()
	path := opts.out
	if path == "" {
		path = a.ConfigPath
	}
	if path == "" {
		path = "aicm.toml"
	}

	if _, err := os.Stat(path); err == nil && !opts.force {
		return failf(ExitConfig, "config_exists",
			"%s already exists; pass --force to overwrite it", path)
	}

	cfg := &config.Config{}
	if existing, _, err := config.Load(path); err == nil {
		cfg = existing
	}

	interactive := !opts.nonInteractive && isTerminal(a.In)
	if interactive {
		if err := a.prompt(cfg); err != nil {
			return err
		}
	}

	if opts.siteID != "" {
		cfg.SiteID = opts.siteID
	}
	if opts.siteName != "" {
		cfg.SiteName = opts.siteName
	}
	if opts.ownDomain != "" {
		cfg.OwnDomain = opts.ownDomain
	}
	// Only override what the operator actually passed: a default value must not
	// silently replace what the existing configuration already says.
	if cmd.Flags().Changed("market") {
		cfg.Market = opts.market
	}
	if cmd.Flags().Changed("language") {
		cfg.Language = opts.language
	}
	if cmd.Flags().Changed("report-language") {
		cfg.Report.Language = opts.reportLanguage
	}
	if len(opts.brandTerms) > 0 {
		cfg.BrandTerms = opts.brandTerms
	}
	if len(opts.competitors) > 0 {
		cfg.Competitors = opts.competitors
	}
	if cmd.Flags().Changed("db-path") {
		cfg.DB = opts.dbPath
	}
	if cmd.Flags().Changed("report-dir") {
		cfg.ReportDir = opts.reportDir
	}

	cfg.ApplyDefaults()

	if problems := cfg.Problems(); len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintf(a.Err, "- %s\n", problem)
		}
		return failf(ExitConfig, "config_incomplete",
			"the configuration is incomplete: %s", strings.Join(problems, "; "))
	}

	if err := config.Save(path, cfg); err != nil {
		return failf(ExitConfig, "config_write_failed", "cannot write %s: %v", path, err)
	}

	createdDB := ""
	if !opts.skipDB {
		db, err := sqlite.Open(cfg.DB)
		if err != nil {
			return failf(ExitConfig, "db_unavailable", "cannot open database %s: %v", cfg.DB, err)
		}
		defer db.Close()
		if _, err := db.EnsureProject(ctx, sqlite.Project{
			SiteID: cfg.SiteID, Name: cfg.SiteName, OwnDomain: cfg.OwnDomain,
			Subdomains: cfg.HasSubdomains(), Market: cfg.Market, Language: cfg.Language,
		}); err != nil {
			return failf(ExitConfig, "db_unavailable", "cannot create the project row: %v", err)
		}
		createdDB = db.Path()
	}

	if a.JSON {
		return a.PrintJSON(map[string]any{
			"config":     path,
			"db":         createdDB,
			"site_id":    cfg.SiteID,
			"own_domain": cfg.OwnDomain,
		})
	}

	fmt.Fprintf(a.Out, "config: %s\n", path)
	if createdDB != "" {
		fmt.Fprintf(a.Out, "db:     %s\n", createdDB)
	}
	fmt.Fprintf(a.Out, "\nnext steps:\n")
	fmt.Fprintf(a.Out, "  1. aicm questions import --file questions.yml   (or questions.csv)\n")
	fmt.Fprintf(a.Out, "  2. export %s=...   (never put the key in the config)\n", cfg.Providers.OpenAI.APIKeyEnv)
	fmt.Fprintf(a.Out, "  3. aicm run --provider openai --repeat 1 --dry-run\n")
	fmt.Fprintf(a.Out, "  4. aicm report --period 30d\n")
	return nil
}

func (a *App) prompt(cfg *config.Config) error {
	reader := bufio.NewReader(a.In)
	ask := func(label, current string) (string, error) {
		suffix := ""
		if current != "" {
			suffix = " [" + current + "]"
		}
		fmt.Fprintf(a.Err, "%s%s: ", label, suffix)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", failf(ExitConfig, "input_failed", "cannot read input: %v", err)
		}
		value := strings.TrimSpace(line)
		if value == "" {
			return current, nil
		}
		return value, nil
	}

	var err error
	if cfg.SiteID, err = ask("site_id", cfg.SiteID); err != nil {
		return err
	}
	if cfg.OwnDomain, err = ask("own_domain (host only)", cfg.OwnDomain); err != nil {
		return err
	}
	if cfg.SiteName, err = ask("site name", cfg.SiteName); err != nil {
		return err
	}
	market := cfg.Market
	if market == "" {
		market = config.DefaultMarket
	}
	if cfg.Market, err = ask("market", market); err != nil {
		return err
	}
	brand := strings.Join(cfg.BrandTerms, ",")
	if brand, err = ask("brand terms (comma separated)", brand); err != nil {
		return err
	}
	cfg.BrandTerms = splitList(brand)
	competitors := strings.Join(cfg.Competitors, ",")
	if competitors, err = ask("competitor domains (comma separated)", competitors); err != nil {
		return err
	}
	cfg.Competitors = splitList(competitors)
	if cfg.DB, err = ask("database path", cfg.DB); err != nil {
		return err
	}
	return nil
}

func isTerminal(reader io.Reader) bool {
	file, ok := reader.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func (a *App) versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the tool version and the API surfaces this build speaks",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := map[string]any{
				"tool":         "aicm",
				"version":      runner.Build,
				"tool_version": runner.ToolVersion(),
				"normalizer":   domain.NormalizerVersion,
				"providers":    config.ProviderNames(),
				"endpoints":    endpointVersions(),
				"measurement":  "official_api",
				// Some adapters have never been called against the real service. The
				// lists below say which ones, so "this build speaks the API" is never
				// read as "this build has observed the API".
				"live_api_tested":   false, // no test calls a provider
				"live_observations": providers.LiveObservation,
				"live_not_observed": unobservedProviders(),
			}
			if a.JSON {
				return a.PrintJSON(info)
			}
			fmt.Fprintf(a.Out, "aicm %s (tool_version %s)\n", runner.Build, runner.ToolVersion())
			fmt.Fprintf(a.Out, "providers: %s\n", describeEndpoints())
			fmt.Fprintf(a.Out, "live API: %s\n", liveAPIStatus())
			fmt.Fprintf(a.Out, "tests:    never call a provider; CI uses fixtures and httptest only\n")
			return nil
		},
	}
}

// endpointVersions is the surface each adapter speaks, in the provider order.
func endpointVersions() map[string]string {
	return map[string]string{
		"openai":     "responses/v1+web_search",
		"gemini":     "generateContent/v1beta+google_search",
		"anthropic":  anthropic.EndpointVersion,
		"perplexity": perplexity.EndpointVersion,
		"grok":       grok.EndpointVersion,
	}
}

// describeEndpoints renders the providers inline, so a one-line human output does
// not need a hand-written sentence per provider.
func describeEndpoints() string {
	names := config.ProviderNames()
	endpoints := endpointVersions()
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s (%s)", name, endpoints[name]))
	}
	return strings.Join(parts, ", ")
}

// unobservedProviders lists the providers this build supports but has never called
// against the real service (no entry in providers.LiveObservation).
func unobservedProviders() []string {
	missing := make([]string, 0, len(config.ProviderNames()))
	for _, name := range config.ProviderNames() {
		if providers.LiveObservation[name] == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

// liveAPIStatus says which providers were observed once and which were not. One
// observed model is not a supported-model list, and an adapter that has never been
// called must not read as observed.
func liveAPIStatus() string {
	observed := summarizeLiveObservations()
	missing := unobservedProviders()
	if len(missing) == 0 {
		return fmt.Sprintf("observed once per provider (%s)", observed)
	}
	if observed == "no live call from this build" {
		return fmt.Sprintf("no live call from this build; implemented but not observed: %s",
			strings.Join(missing, ", "))
	}
	return fmt.Sprintf("observed once: %s — implemented but NOT observed: %s",
		observed, strings.Join(missing, ", "))
}

// summarizeLiveObservations renders a stable one-line summary, so the command does
// not need a hand-written sentence per provider.
func summarizeLiveObservations() string {
	names := config.ProviderNames()
	parts := make([]string, 0, len(names))
	for _, name := range names {
		if note := providers.LiveObservation[name]; note != "" {
			parts = append(parts, name+": "+note)
		}
	}
	if len(parts) == 0 {
		return "no live call from this build"
	}
	return strings.Join(parts, "; ")
}
