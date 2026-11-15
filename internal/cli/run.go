package cli

import (
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
	"github.com/routeflags/ai-citation-monitor/internal/providers/httpclient"
	"github.com/routeflags/ai-citation-monitor/internal/providers/openai"
	"github.com/routeflags/ai-citation-monitor/internal/runner"
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
	return cmd
}

func (a *App) runMeasure(cmd *cobra.Command, opts runOptions) error {
	ctx := cmd.Context()
	cfg, err := a.LoadConfig()
	if err != nil {
		return err
	}
	if problems := cfg.Problems(); len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintf(a.Err, "- %s\n", problem)
		}
		return failf(ExitConfig, "config_incomplete", "the configuration is incomplete")
	}

	requested := splitList(opts.source)
	if len(requested) == 0 {
		requested = splitList(opts.providerAlias)
	}
	if len(requested) == 0 {
		requested = defaultProviders(cfg)
	}
	if len(requested) == 0 {
		return failf(ExitConfig, "provider_unknown",
			"no provider selected and none configured with a model; set [providers.openai] model in %s or pass --provider", cfg.Path)
	}
	for _, name := range requested {
		if _, ok := cfg.Provider(name); !ok {
			return unknownProviderError(name)
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
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintf(a.Err, "- %s\n", problem)
		}
		return failf(ExitConfig, "model_missing", "the measurement conditions are incomplete")
	}

	// "the configuration is valid" and "a credential exists" are two different
	// checks, printed as two different lines. --dry-run is the
	// look-before-you-spend path, so a missing credential is reported but only
	// stops a real run (exit 3, nothing sent): the plan needs no secret.
	missing := map[string]string{}
	for _, target := range targets {
		providerCfg, _ := cfg.Provider(target.Provider)
		if strings.TrimSpace(os.Getenv(providerCfg.APIKeyEnv)) == "" {
			missing[target.Provider] = providerCfg.APIKeyEnv
		}
	}
	if !a.Quiet && (a.Verbose || opts.dryRun) {
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

	if len(missing) > 0 && !opts.dryRun {
		names := make([]string, 0, len(missing))
		for provider, env := range missing {
			names = append(names, fmt.Sprintf("%s (%s)", provider, env))
		}
		sort.Strings(names)
		return failf(ExitAuth, "auth_missing",
			"no credential for %s; nothing was sent (export the variable, or use --dry-run to see the plan without one)",
			strings.Join(names, ", "))
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
	questions, err := db.Questions(ctx, project.ID)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}
	if len(questions) == 0 {
		return failf(ExitConfig, "questions_missing",
			"no questions stored; run `aicm questions import --file questions.yml` first")
	}

	session := opts.session
	if session == "" {
		session = a.NowUTC().In(analysis.JST).Format("2006-01-02")
	}
	if opts.repeat < 1 || opts.repeat > 5 {
		return failf(ExitConfig, "usage", "--repeat must be 1..5, got %d", opts.repeat)
	}
	if opts.stage != "" && !validStage(opts.stage) {
		return failf(ExitConfig, "usage", "--stage must be smoke, initial or regular, got %q", opts.stage)
	}

	concurrency := opts.concurrency
	if concurrency <= 0 {
		concurrency = cfg.Limits.Concurrency
	}
	rpm := opts.rpm
	if rpm <= 0 {
		rpm = cfg.Limits.RPM
	}

	setVersion, setSHA, err := db.LatestQuestionSet(ctx, project.ID)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}

	engine := &runner.Runner{
		DB:                 db,
		ProjectID:          project.ID,
		OwnDomain:          project.OwnDomain,
		Subdomains:         project.Subdomains,
		BrandTerms:         cfg.BrandTerms,
		Competitors:        cfg.Competitors,
		Concurrency:        concurrency,
		Session:            session,
		QuestionSetVersion: setVersion,
		QuestionSetSHA256:  setSHA,
		Now:                a.NowUTC,
		Logf:               a.Verbosef,
	}
	engine.Providers, err = a.buildProviders(cfg, targets, rpm)
	if err != nil {
		return err
	}

	plan, err := engine.BuildPlan(ctx, questions, targets, opts.repeat, opts.stage, opts.limit)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}

	if !a.JSON {
		fmt.Fprintf(a.Out, "questions: %d\n", plan.Questions)
		for _, target := range targets {
			fmt.Fprintf(a.Out, "target:    %s / %s\n", target.Provider, target.Model)
		}
		fmt.Fprintf(a.Out, "condition: stage=%s repeat=%d session=%s\n", nonEmpty(opts.stage, "(none)"), opts.repeat, session)
		fmt.Fprintf(a.Out, "calls:     %d planned, %d already recorded (skipped)\n", plan.Pending, plan.AlreadyDone)
		if plan.FailedRecorded > 0 {
			// A recorded failure is not a measurement, but it holds its condition for
			// this session (the database keeps one row per condition). Say so instead of
			// letting the run look like there was nothing to do.
			fmt.Fprintf(a.Out, "note:      %d of the recorded calls failed and are not resent in this session; pass --session <label> to measure them again\n",
				plan.FailedRecorded)
		}
	}

	if opts.dryRun {
		if a.JSON {
			return a.PrintJSON(map[string]any{
				"dry_run": true, "planned_calls": plan.Pending, "already_done": plan.AlreadyDone,
				"failed_recorded": plan.FailedRecorded,
				"session":         session, "targets": targets, "missing_credentials": missingEnvNames(missing),
			})
		}
		fmt.Fprintln(a.Out, "dry-run: nothing was sent")
		return nil
	}

	if !opts.yes {
		if !isTerminal(a.In) {
			return failf(ExitConfig, "confirmation_required",
				"refusing to spend without confirmation on a non-interactive stdin; pass --yes")
		}
		if !a.confirm(fmt.Sprintf("%d calls will be sent. continue?", plan.Pending)) {
			return failf(ExitGeneral, "aborted", "aborted by the operator; nothing was sent")
		}
	}

	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	summary, err := engine.Execute(runCtx, plan)
	if err != nil {
		return failf(ExitGeneral, "run_failed", "%v", err)
	}

	if a.JSON {
		payload := map[string]any{
			"calls": summary.Calls, "success": summary.Success, "failed": summary.Failed,
			"cancelled": summary.Cancelled, "skipped": summary.Skipped, "not_run": summary.NotRun,
			"exit_kind": summary.ExitKind(),
		}
		if err := a.PrintJSON(payload); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.Out, "run: calls=%d success=%d failed=%d cancelled=%d skipped=%d not_run=%d\n",
			summary.Calls, summary.Success, summary.Failed, summary.Cancelled, summary.Skipped, summary.NotRun)
	}

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
		switch target.Provider {
		case "openai":
			result["openai"] = openai.New(openai.Options{
				APIKey: apiKey, BaseURL: providerCfg.BaseURL, HTTP: client, Secrets: secrets,
			})
		case "gemini":
			result["gemini"] = gemini.New(gemini.Options{
				APIKey: apiKey, BaseURL: providerCfg.BaseURL, HTTP: client, Secrets: secrets,
			})
		case "anthropic":
			result["anthropic"] = anthropic.New(anthropic.Options{
				APIKey: apiKey, BaseURL: providerCfg.BaseURL, HTTP: client, Secrets: secrets,
				MaxTokens: cfg.Providers.Anthropic.MaxTokens, MaxUses: cfg.Providers.Anthropic.MaxUses,
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

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
