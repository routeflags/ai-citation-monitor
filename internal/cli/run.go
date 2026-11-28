package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/routeflags/ai-citation-monitor/internal/analysis"
	"github.com/routeflags/ai-citation-monitor/internal/config"
	"github.com/routeflags/ai-citation-monitor/internal/providers"
	"github.com/routeflags/ai-citation-monitor/internal/providers/anthropic"
	"github.com/routeflags/ai-citation-monitor/internal/providers/gemini"
	"github.com/routeflags/ai-citation-monitor/internal/providers/grok"
	"github.com/routeflags/ai-citation-monitor/internal/providers/httpclient"
	"github.com/routeflags/ai-citation-monitor/internal/providers/openai"
	"github.com/routeflags/ai-citation-monitor/internal/providers/perplexity"
	"github.com/routeflags/ai-citation-monitor/internal/runner"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

type runOptions struct {
	source        string
	providerAlias string
	model         string
	repeat        int
	stage         string
	concurrency   int
	rpm           int
	dryRun        bool
	yes           bool
	limit         int
	session       string
	store         string
}

func (a *App) runCommand() *cobra.Command {
	opts := runOptions{}
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Ask the questions of the official search-enabled APIs and record the answers",
		Long: `Run one measurement pass.

One call = one question x one repeat x one provider/model. Every call is recorded
whether it succeeded, failed or was cancelled. A call whose conditions are
already recorded (same question version, provider, model, repeat, stage and
session) is skipped, so a resumed run never pays twice.

--dry-run prints the plan and the checks without touching the network.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runMeasure(cmd, opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.source, "provider", "", "provider(s) to measure: "+strings.Join(config.ProviderNames(), ", ")+" (comma separated)")
	flags.StringVar(&opts.providerAlias, "source", "", "alias of --provider")
	flags.StringVar(&opts.model, "model", "", "model name (overrides the per-provider config; models are never invented)")
	flags.IntVar(&opts.repeat, "repeat", 1, "repeats per question (smoke=1, initial=3, regular=5)")
	flags.StringVar(&opts.stage, "stage", "", "stage label recorded with the run (smoke / initial / regular)")
	flags.IntVar(&opts.concurrency, "concurrency", 0, "parallel calls (default from the config)")
	flags.IntVar(&opts.rpm, "rpm", 0, "requests per minute per provider (default from the config)")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "print the plan and stop; never touches the network")
	flags.BoolVar(&opts.yes, "yes", false, "do not ask for confirmation")
	flags.IntVar(&opts.limit, "limit", 0, "measure only the first N questions (trial runs)")
	flags.StringVar(&opts.session, "session", "", "resume window label (default: today in JST)")
	flags.StringVar(&opts.store, "store", "", "what to keep in the database: full (answer text + raw JSON) or hash-only (answer hash only; default from store_answer_body)")
	return cmd
}

func (a *App) runMeasure(cmd *cobra.Command, opts runOptions) error {
	ctx := cmd.Context()
	cfg, err := a.usableConfig()
	if err != nil {
		return err
	}
	targets, err := a.resolveTargets(cfg, opts)
	if err != nil {
		return err
	}
	storeBody, err := resolveStoreBody(cfg, opts.store)
	if err != nil {
		return err
	}

	// "the configuration is valid" and "a credential exists" are two different
	// checks, printed as two different lines. --dry-run is the
	// look-before-you-spend path, so a missing credential is reported but only
	// stops a real run (exit 3, nothing sent): the plan needs no secret.
	missing := missingCredentials(cfg, targets)
	a.printAuthState(cfg, targets, missing, opts)
	if len(missing) > 0 && !opts.dryRun {
		return authMissingError(missing)
	}

	db, project, err := a.openProject(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	questions, err := loadQuestions(ctx, db, project.ID)
	if err != nil {
		return err
	}
	settings, err := a.runSettings(cfg, opts, storeBody)
	if err != nil {
		return err
	}

	setVersion, setSHA, err := db.LatestQuestionSet(ctx, project.ID)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}
	engine := &runner.Runner{
		DB:         db,
		ProjectID:  project.ID,
		OwnDomain:  project.OwnDomain,
		Subdomains: project.Subdomains,
		BrandTerms: cfg.BrandTerms,
		// The runner classifies each citation when it records it, so a URL written
		// in the config has to be canonical before it gets there (D26).
		Competitors:        configuredCompetitors(cfg.Competitors),
		Concurrency:        settings.concurrency,
		Session:            settings.session,
		HashOnlyBody:       !settings.storeBody,
		QuestionSetVersion: setVersion,
		QuestionSetSHA256:  setSHA,
		Now:                a.NowUTC,
		Logf:               a.Verbosef,
	}
	engine.Providers, err = a.buildProviders(cfg, targets, settings.rpm)
	if err != nil {
		return err
	}
	plan, err := engine.BuildPlan(ctx, questions, targets, opts.repeat, opts.stage, opts.limit)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}

	a.printPlan(plan, settings)
	if opts.dryRun {
		return a.printDryRun(plan, missing, settings)
	}
	if err := a.confirmSpend(opts, plan); err != nil {
		return err
	}
	return a.executeMeasurement(ctx, engine, plan)
}

// usableConfig loads the configuration and refuses to measure when it is
// incomplete: a rate built on a missing site_id or own_domain is wrong, not noisy.
func (a *App) usableConfig() (*config.Config, error) {
	cfg, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	problems := cfg.Problems()
	if len(problems) == 0 {
		return cfg, nil
	}
	for _, problem := range problems {
		fmt.Fprintf(a.Err, "- %s\n", problem)
	}
	return nil, failf(ExitConfig, "config_incomplete", "the configuration is incomplete")
}

// resolveTargets turns --provider / --source / the configuration into the
// provider-model pairs to measure. A model is never guessed.
func (a *App) resolveTargets(cfg *config.Config, opts runOptions) ([]runner.Target, error) {
	requested := splitList(opts.source)
	if len(requested) == 0 {
		requested = splitList(opts.providerAlias)
	}
	if len(requested) == 0 {
		requested = defaultProviders(cfg)
	}
	if len(requested) == 0 {
		return nil, failf(ExitConfig, "provider_unknown",
			"no provider selected and none configured with a model; set [providers.openai] model in %s or pass --provider", cfg.Path)
	}
	for _, name := range requested {
		if _, ok := cfg.Provider(name); !ok {
			return nil, unknownProviderError(name)
		}
	}

	targets := make([]runner.Target, 0, len(requested))
	problems := []string{}
	for _, name := range requested {
		providerCfg, _ := cfg.Provider(name)
		model := opts.model
		if model == "" {
			model = providerCfg.Model
		}
		if strings.TrimSpace(model) == "" {
			problems = append(problems, fmt.Sprintf(
				"%s has no model: set [providers.%s] model in the config or pass --model (a model name is never guessed)",
				name, name))
			continue
		}
		targets = append(targets, runner.Target{Provider: name, Model: model})
	}
	if len(problems) == 0 {
		return targets, nil
	}
	for _, problem := range problems {
		fmt.Fprintf(a.Err, "- %s\n", problem)
	}
	return nil, failf(ExitConfig, "model_missing", "the measurement conditions are incomplete")
}

// resolveStoreBody decides what a run keeps. An empty value follows the
// configuration; a set flag overrides it.
func resolveStoreBody(cfg *config.Config, value string) (bool, error) {
	switch value {
	case "":
		return cfg.HasAnswerBody(), nil
	case "full":
		return true, nil
	case "hash-only":
		return false, nil
	default:
		return false, failf(ExitConfig, "usage", "--store must be full or hash-only, got %q", value)
	}
}

// missingCredentials names the credential variables a real run would need. Only
// the variable names are read: no value is printed or stored.
func missingCredentials(cfg *config.Config, targets []runner.Target) map[string]string {
	missing := map[string]string{}
	for _, target := range targets {
		providerCfg, _ := cfg.Provider(target.Provider)
		if strings.TrimSpace(os.Getenv(providerCfg.APIKeyEnv)) == "" {
			missing[target.Provider] = providerCfg.APIKeyEnv
		}
	}
	return missing
}

// printAuthState shows the configuration path and one auth line per target, when
// the operator asked to see the plan.
func (a *App) printAuthState(cfg *config.Config, targets []runner.Target, missing map[string]string, opts runOptions) {
	showState := (a.Verbose || opts.dryRun) && !a.Quiet
	if !showState {
		return
	}
	fmt.Fprintf(a.Err, "config: %s (valid=true)\n", nonEmpty(cfg.Path, "defaults"))
	for _, target := range targets {
		providerCfg, _ := cfg.Provider(target.Provider)
		state := "set"
		if _, ok := missing[target.Provider]; ok {
			state = "missing"
		}
		fmt.Fprintf(a.Err, "auth: %s %s=%s\n", target.Provider, providerCfg.APIKeyEnv, state)
	}
}

// authMissingError reports a real run that would fail for every target. A dry-run
// never reaches it: the plan needs no secret.
func authMissingError(missing map[string]string) error {
	names := make([]string, 0, len(missing))
	for provider, env := range missing {
		names = append(names, fmt.Sprintf("%s (%s)", provider, env))
	}
	sort.Strings(names)
	return failf(ExitAuth, "auth_missing",
		"no credential for %s; nothing was sent (export the variable, or use --dry-run to see the plan without one)",
		strings.Join(names, ", "))
}

// openProject opens the database and resolves the project row a measurement writes
// to.
func (a *App) openProject(ctx context.Context, cfg *config.Config) (*sqlite.DB, *sqlite.Project, error) {
	db, err := a.OpenDB(cfg)
	if err != nil {
		return nil, nil, err
	}
	project, err := a.RequireProject(ctx, db)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, project, nil
}

// loadQuestions reads the stored questions, and says how to import some when the
// database has none: measuring nothing would look like measuring zero.
func loadQuestions(ctx context.Context, db *sqlite.DB, projectID int64) ([]sqlite.Question, error) {
	questions, err := db.Questions(ctx, projectID)
	if err != nil {
		return nil, failf(ExitConfig, "db_unavailable", "%v", err)
	}
	if len(questions) == 0 {
		return nil, failf(ExitConfig, "questions_missing",
			"no questions stored; run `aicm questions import --file questions.yml` first")
	}
	return questions, nil
}

// runSettings is what one run resolves once: the session label, the limits and what
// the database keeps.
type runSettings struct {
	session     string
	concurrency int
	rpm         int
	storeBody   bool
}

// runSettings applies the flags over the configuration and rejects values the
// database would record as something other than the operator meant.
func (a *App) runSettings(cfg *config.Config, opts runOptions, storeBody bool) (runSettings, error) {
	session := opts.session
	if session == "" {
		session = a.NowUTC().In(analysis.JST).Format("2006-01-02")
	}
	if opts.repeat < 1 || opts.repeat > 5 {
		return runSettings{}, failf(ExitConfig, "usage", "--repeat must be 1..5, got %d", opts.repeat)
	}
	if opts.stage != "" && !validStage(opts.stage) {
		return runSettings{}, failf(ExitConfig, "usage", "--stage must be smoke, initial or regular, got %q", opts.stage)
	}
	concurrency := opts.concurrency
	if concurrency <= 0 {
		concurrency = cfg.Limits.Concurrency
	}
	rpm := opts.rpm
	if rpm <= 0 {
		rpm = cfg.Limits.RPM
	}
	return runSettings{session: session, concurrency: concurrency, rpm: rpm, storeBody: storeBody}, nil
}

// printPlan shows the plan before anything is sent: what will run, under which
// condition, and what the database already holds.
func (a *App) printPlan(plan *runner.Plan, settings runSettings) {
	if a.JSON {
		return
	}
	s := a.styles()
	s.planLine(a.Out, "questions:", "%d", plan.Questions)
	for _, target := range plan.Targets {
		s.planLine(a.Out, "target:", "%s / %s", target.Provider, target.Model)
	}
	s.planLine(a.Out, "condition:", "stage=%s repeat=%d session=%s", nonEmpty(plan.Stage, "(none)"), plan.Repeat, settings.session)
	s.planLine(a.Out, "calls:", "%d planned, %d already recorded (skipped)", plan.Pending, plan.AlreadyDone)
	s.planLine(a.Out, "store:", "%s", storeLine(settings.storeBody))
	if plan.FailedRecorded > 0 {
		// A recorded failure is not a measurement, but it holds its condition for
		// this session (the database keeps one row per condition). Say so instead of
		// letting the run look like there was nothing to do.
		s.planLine(a.Out, "note:", "%d of the recorded calls failed and are not resent in this session; pass --session <label> to measure them again",
			plan.FailedRecorded)
	}
}

func (a *App) printDryRun(plan *runner.Plan, missing map[string]string, settings runSettings) error {
	if a.JSON {
		return a.PrintJSON(map[string]any{
			"dry_run": true, "planned_calls": plan.Pending, "already_done": plan.AlreadyDone,
			"failed_recorded": plan.FailedRecorded,
			"session":         settings.session, "targets": plan.Targets, "missing_credentials": missingEnvNames(missing),
			"store": storeMode(settings.storeBody),
		})
	}
	fmt.Fprintln(a.Out, "dry-run: nothing was sent")
	return nil
}

// confirmSpend asks before a real run, and refuses even to ask when stdin is not a
// terminal: a script that forgot --yes must not wait forever.
func (a *App) confirmSpend(opts runOptions, plan *runner.Plan) error {
	if opts.yes {
		return nil
	}
	if !isTerminal(a.In) {
		return failf(ExitConfig, "confirmation_required",
			"refusing to spend without confirmation on a non-interactive stdin; pass --yes")
	}
	if !a.confirm(fmt.Sprintf("%d calls will be sent. continue?", plan.Pending)) {
		return failf(ExitGeneral, "aborted", "aborted by the operator; nothing was sent")
	}
	return nil
}

// executeMeasurement runs the plan, prints the summary and turns the outcome into
// the exit code. The signal context stops new calls but keeps what already
// finished.
func (a *App) executeMeasurement(ctx context.Context, engine *runner.Runner, plan *runner.Plan) error {
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	summary, err := engine.Execute(runCtx, plan)
	if err != nil {
		return failf(ExitGeneral, "run_failed", "%v", err)
	}
	if err := a.printSummary(summary); err != nil {
		return err
	}
	return exitForSummary(summary)
}

func (a *App) printSummary(summary *runner.Summary) error {
	if a.JSON {
		return a.PrintJSON(map[string]any{
			"calls": summary.Calls, "success": summary.Success, "failed": summary.Failed,
			"cancelled": summary.Cancelled, "skipped": summary.Skipped, "not_run": summary.NotRun,
			"exit_kind": summary.ExitKind(),
		})
	}
	fmt.Fprintf(a.Out, "run: calls=%d success=%d failed=%d cancelled=%d skipped=%d not_run=%d\n",
		summary.Calls, summary.Success, summary.Failed, summary.Cancelled, summary.Skipped, summary.NotRun)
	return nil
}

// exitForSummary turns the summary into the exit code a script reads: an all-API
// failure (4) is not a partial result (1).
func exitForSummary(summary *runner.Summary) error {
	switch summary.ExitKind() {
	case "api":
		return failf(ExitAPI, "api_failure",
			"every call failed at the provider (HTTP error); see the error_code column (records were kept)")
	case "transport":
		return failf(ExitAPI, "api_failure",
			"every call failed at the transport level; see the error_code column (records were kept)")
	case "partial":
		return failf(ExitGeneral, "partial_failure",
			"%d of %d calls failed or were cancelled; records were kept", summary.Failed+summary.Cancelled, summary.Calls)
	default:
		return nil
	}
}

func (a *App) buildProviders(cfg *config.Config, targets []runner.Target, rpm int) (map[string]providers.Provider, error) {
	result := make(map[string]providers.Provider, len(targets))
	for _, target := range targets {
		providerCfg, ok := cfg.Provider(target.Provider)
		if !ok {
			return nil, unknownProviderError(target.Provider)
		}
		apiKey := os.Getenv(providerCfg.APIKeyEnv)
		timeout := time.Duration(providerCfg.TimeoutSeconds) * time.Second
		client := httpclient.New(timeout, rpm, cfg.Limits.MaxRetries)
		client.Logf = a.Verbosef
		secrets := []string{apiKey}
		// The adapter stamps the call's start and end. It gets the same clock as
		// the runner, so a recorded run and its timestamps come from one source
		// (production uses time.Now; tests pin a date and must not depend on the
		// wall clock).
		now := a.NowUTC
		switch target.Provider {
		case "openai":
			result["openai"] = openai.New(openai.Options{
				APIKey: apiKey, BaseURL: providerCfg.BaseURL, HTTP: client, Secrets: secrets,
				Now: now,
			})
		case "gemini":
			result["gemini"] = gemini.New(gemini.Options{
				APIKey: apiKey, BaseURL: providerCfg.BaseURL, HTTP: client, Secrets: secrets,
				Now: now,
			})
		case "anthropic":
			result["anthropic"] = anthropic.New(anthropic.Options{
				APIKey: apiKey, BaseURL: providerCfg.BaseURL, HTTP: client, Secrets: secrets,
				MaxTokens: cfg.Providers.Anthropic.MaxTokens, MaxUses: cfg.Providers.Anthropic.MaxUses,
				Now: now,
			})
		case "perplexity":
			result["perplexity"] = perplexity.New(perplexity.Options{
				APIKey: apiKey, BaseURL: providerCfg.BaseURL, HTTP: client, Secrets: secrets,
				Now: now,
			})
		case "grok":
			result["grok"] = grok.New(grok.Options{
				APIKey: apiKey, BaseURL: providerCfg.BaseURL, HTTP: client, Secrets: secrets,
				Now: now,
			})
		}
	}
	return result, nil
}

func (a *App) confirm(question string) bool {
	fmt.Fprintf(a.Err, "%s [y/N]: ", question)
	var answer string
	_, err := fmt.Fscanln(a.In, &answer)
	if err != nil {
		return false
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}

func defaultProviders(cfg *config.Config) []string {
	names := []string{}
	for _, name := range config.ProviderNames() {
		providerCfg, ok := cfg.Provider(name)
		if ok && strings.TrimSpace(providerCfg.Model) != "" {
			names = append(names, name)
		}
	}
	return names
}

// missingEnvNames lists the credential variables a real run would need, so a
// dry-run can say why it would stop without printing any value.
func missingEnvNames(missing map[string]string) []string {
	names := make([]string, 0, len(missing))
	for _, env := range missing {
		names = append(names, env)
	}
	sort.Strings(names)
	return names
}

func validStage(stage string) bool {
	switch stage {
	case "smoke", "initial", "regular":
		return true
	default:
		return false
	}
}

// storeMode is the machine word for what a run keeps.
func storeMode(storeBody bool) string {
	if storeBody {
		return "full"
	}
	return "hash-only"
}

// storeLine explains the store mode in the plan, because "hash-only" changes what
// can be re-checked later.
func storeLine(storeBody bool) string {
	if storeBody {
		return "full (answer text and raw JSON are kept and the hash is checked)"
	}
	return "hash-only (answer text and raw JSON are not written; only the hash is kept)"
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
