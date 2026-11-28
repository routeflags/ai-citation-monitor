package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/routeflags/ai-citation-monitor/internal/question"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

type questionAddOptions struct {
	id       string
	text     string
	category string
	language string
	version  string
}

type questionImportOptions struct {
	file     string
	stage    string
	check    bool
	dryRun   bool
	strict   bool
	language string
}

func (a *App) questionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "questions",
		Short: "Manage the fixed question set (add / list / import)",
	}
	cmd.AddCommand(a.questionsAddCommand(), a.questionsListCommand(), a.questionsImportCommand())
	return cmd
}

func (a *App) questionsAddCommand() *cobra.Command {
	opts := questionAddOptions{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add one question (or update its text as a new version)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(opts.id) == "" || strings.TrimSpace(opts.text) == "" {
				return failf(ExitConfig, "question_invalid", "--id and --text are required")
			}
			if !validCategory(opts.category) {
				return failf(ExitConfig, "question_invalid",
					"--category %q is not in the vocabulary (%s)", opts.category, strings.Join(question.Categories, ", "))
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
			stored, err := db.UpsertQuestion(ctx, project.ID, toStoreQuestion(question.Question{
				Key: opts.id, Text: opts.text, Category: opts.category,
				Language: opts.language, Version: opts.version,
			}))
			if err != nil {
				return failf(ExitConfig, "db_write_failed", "%v", err)
			}
			if a.JSON {
				return a.PrintJSON(map[string]any{"question": stored})
			}
			fmt.Fprintf(a.Out, "question: %s version=%s category=%s\n", stored.Key, stored.Version, stored.Category)
			return nil
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.id, "id", "", "question id (stable, for example Q01)")
	flags.StringVar(&opts.text, "text", "", "question text")
	flags.StringVar(&opts.category, "category", "", "category ("+strings.Join(question.Categories, "|")+")")
	flags.StringVar(&opts.language, "language", "", "language of the question")
	flags.StringVar(&opts.version, "version", "", "version label (default: derived from the text)")
	return cmd
}

func (a *App) questionsListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the stored questions",
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
			questions, err := db.Questions(ctx, project.ID)
			if err != nil {
				return failf(ExitConfig, "db_unavailable", "%v", err)
			}
			if a.JSON {
				return a.PrintJSON(map[string]any{"questions": questions, "total": len(questions)})
			}
			if len(questions) == 0 {
				fmt.Fprintln(a.Out, "no questions yet (import a question set with `aicm questions import --file ...`)")
				return nil
			}
			fmt.Fprintln(a.Out, a.styles().header("%-6s %-14s %-16s %s", "id", "version", "category", "text"))
			for _, q := range questions {
				fmt.Fprintf(a.Out, "%-6s %-14s %-16s %s\n", q.Key, q.Version, q.Category, q.Text)
			}
			fmt.Fprintf(a.Out, "\ntotal: %d\n", len(questions))
			return nil
		},
	}
}

func (a *App) questionsImportCommand() *cobra.Command {
	opts := questionImportOptions{}
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import questions from a YAML question set or a CSV file",
		Long: `Import a question set.

YAML is the contract format (set_version / own_domain / brand_terms / stages /
questions[]). CSV needs the header id,text,category with optional language and
version columns.

--check validates and prints the summary without writing anything.

--strict turns the stage nesting warning into an error: every question of a
smaller stage must also be in the next larger one (smoke <= initial <= regular).
Without it a broken nesting only warns, and the stage sets stop meaning the same
thing between runs, so the coverage denominator is no longer comparable. CSV has
no stages, so --strict changes nothing there.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.file == "" {
				return failf(ExitConfig, "usage", "--file is required")
			}
			ext := strings.ToLower(filepath.Ext(opts.file))
			switch ext {
			case ".csv":
				return a.importCSV(cmd, opts)
			case ".yml", ".yaml":
				return a.importYAML(cmd, opts)
			default:
				return failf(ExitConfig, "usage", "%s: expected a .yml, .yaml or .csv file", opts.file)
			}
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.file, "file", "", "question set (.yml / .yaml) or question list (.csv)")
	flags.StringVar(&opts.stage, "stage", "", "import only the questions of this stage (YAML only)")
	flags.BoolVar(&opts.check, "check", false, "validate and print the summary without writing")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "validate everything but do not write")
	flags.BoolVar(&opts.strict, "strict", false, "treat the stage nesting warning as an error (exit 1, nothing is written)")
	flags.StringVar(&opts.language, "language", "", "language for CSV rows that do not set one")
	return cmd
}

func (a *App) importYAML(cmd *cobra.Command, opts questionImportOptions) error {
	set, err := question.LoadYAML(opts.file)
	if err != nil {
		return failf(ExitConfig, "question_set_unreadable", "%v", err)
	}
	problems, warnings := set.Validate(opts.strict)
	for _, warning := range warnings {
		fmt.Fprintf(a.Err, "warning: %s\n", warning)
	}
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintf(a.Err, "NG: %s\n", problem)
		}
		return failf(ExitGeneral, "question_set_invalid",
			"the question set is not usable (%d problems); nothing was written", len(problems))
	}

	questions, err := questionsToImport(set, opts)
	if err != nil {
		return err
	}
	if opts.check || opts.dryRun {
		return a.printQuestionSetCheck(set, problems, warnings)
	}

	cfg, err := a.LoadConfig()
	if err != nil {
		return err
	}
	if set.OwnDomain != "" && cfg.OwnDomain != "" && set.OwnDomain != cfg.OwnDomain {
		fmt.Fprintf(a.Err, "warning: own_domain differs: question set %q, config %q (the config decides)\n",
			set.OwnDomain, cfg.OwnDomain)
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

	inserted, updated, err := applyQuestions(ctx, db, project.ID, questions)
	if err != nil {
		return err
	}
	status, err := db.RecordQuestionSet(ctx, project.ID, set.SetVersion, set.SHA256, opts.file,
		strings.TrimSpace(set.BrandTermsStatus))
	if err != nil {
		return failf(ExitConfig, "db_write_failed", "%v", err)
	}
	recordedStatus, err := recordedBrandTermsStatus(ctx, db, set.SHA256)
	if err != nil {
		return failf(ExitConfig, "db_unavailable", "%v", err)
	}
	a.warnOnBrandTermsMismatch(recordedStatus, set.BrandTermsStatus)

	if a.JSON {
		return a.PrintJSON(map[string]any{
			"inserted": inserted, "updated": updated, "question_set": status,
			"sha256": set.SHA256, "set_version": set.SetVersion,
			"brand_terms_status": recordedStatus,
		})
	}
	fmt.Fprintf(a.Out, "questions: inserted=%d updated=%d question_set=%s\n", inserted, updated, status)
	fmt.Fprintf(a.Out, "sha256: %s\n", set.SHA256)
	fmt.Fprintf(a.Out, "brand terms status: %s\n", brandTermsStatusDisplay(recordedStatus))
	return nil
}

// questionsToImport returns the questions to write: the whole set, or the ones of
// one stage.
func questionsToImport(set *question.Set, opts questionImportOptions) ([]question.Question, error) {
	if opts.stage == "" {
		return set.Questions, nil
	}
	questions := set.StageQuestions(opts.stage)
	if len(questions) == 0 {
		return nil, failf(ExitConfig, "question_set_invalid",
			"stage %q has no questions in %s", opts.stage, opts.file)
	}
	return questions, nil
}

// printQuestionSetCheck prints the --check / --dry-run report. The warnings were
// already printed as they were found; the JSON form carries them, and the problems,
// so a caller reads one document.
func (a *App) printQuestionSetCheck(set *question.Set, problems, warnings []string) error {
	if a.JSON {
		return a.PrintJSON(map[string]any{
			"summary": set.Summarise(), "errors": problems, "warnings": warnings,
			"imported": 0,
		})
	}
	summary := set.Summarise()
	fmt.Fprintf(a.Out, "set_version: %s\nsite_id: %s\nown_domain: %s\nmarket: %s\nquestions: %d\n",
		summary.SetVersion, summary.SiteID, summary.OwnDomain, summary.Market, summary.Questions)
	fmt.Fprintf(a.Out, "brand terms status: %s\n", brandTermsStatusDisplay(summary.BrandTermsStatus))
	for _, stage := range summary.Stages {
		fmt.Fprintf(a.Out, "stage %s: %d questions x %d run = %d round trips per provider\n",
			stage.Name, stage.Questions, stage.RunsPerQuestion, stage.RoundTrips)
	}
	fmt.Fprintln(a.Out, "OK: the question set is usable (nothing was written)")
	return nil
}

// applyQuestions upserts each question and counts what was new. Both import
// formats write the same fields and report the same totals, so the loop lives here
// once.
func applyQuestions(ctx context.Context, db *sqlite.DB, projectID int64, items []question.Question) (inserted, updated int, err error) {
	for _, item := range items {
		exists, err := db.QuestionExists(ctx, projectID, item.Key)
		if err != nil {
			return 0, 0, failf(ExitConfig, "db_unavailable", "%v", err)
		}
		if _, err := db.UpsertQuestion(ctx, projectID, toStoreQuestion(item)); err != nil {
			return 0, 0, failf(ExitConfig, "db_write_failed", "%v", err)
		}
		if exists {
			updated++
		} else {
			inserted++
		}
	}
	return inserted, updated, nil
}

// warnOnBrandTermsMismatch says when the record disagrees with the file. The record
// is append-only, so a set that was imported before the status was recorded keeps
// the empty value even when the same bytes are imported again. Print what the record
// says, and say so when the file disagrees: telling the operator the file's value
// would claim a record that does not exist.
func (a *App) warnOnBrandTermsMismatch(recordedStatus, declaredStatus string) {
	declared := strings.TrimSpace(declaredStatus)
	if recordedStatus == declared {
		return
	}
	fmt.Fprintf(a.Err, "warning: the question set is already recorded with brand terms status %q; this file declares %q. "+
		"A recorded set is not updated (append-only), so a report keeps reading the recorded value; "+
		"change the question set (and its set_version) to record the new status.\n",
		brandTermsStatusDisplay(recordedStatus), brandTermsStatusDisplay(declared))
}

func (a *App) importCSV(cmd *cobra.Command, opts questionImportOptions) error {
	// CSV carries no stages, so --strict has nothing to promote here. The flag is
	// accepted so the same command line works for both formats; the structural
	// problems a CSV can have (duplicate id, unknown category) are always errors.
	file, err := os.Open(opts.file)
	if err != nil {
		return failf(ExitConfig, "question_set_unreadable", "%v", err)
	}
	defer file.Close()

	questions, err := question.ParseCSV(file)
	if err != nil {
		return failf(ExitGeneral, "question_set_invalid", "%v", err)
	}
	problems := []string{}
	seen := map[string]bool{}
	for _, item := range questions {
		if seen[item.Key] {
			problems = append(problems, "duplicate id: "+item.Key)
		}
		seen[item.Key] = true
		if !validCategory(item.Category) {
			problems = append(problems, fmt.Sprintf("%s: unknown category %q", item.Key, item.Category))
		}
		if item.Language == "" {
			item.Language = opts.language
		}
	}
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintf(a.Err, "NG: %s\n", problem)
		}
		return failf(ExitGeneral, "question_set_invalid", "%d problems; nothing was written", len(problems))
	}

	if opts.check || opts.dryRun {
		if a.JSON {
			return a.PrintJSON(map[string]any{"questions": len(questions), "imported": 0})
		}
		fmt.Fprintf(a.Out, "questions: %d (validated, nothing was written)\n", len(questions))
		return nil
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
	for index := range questions {
		if questions[index].Language == "" {
			questions[index].Language = cfg.Language
		}
	}
	inserted, updated, err := applyQuestions(ctx, db, project.ID, questions)
	if err != nil {
		return err
	}
	if a.JSON {
		return a.PrintJSON(map[string]any{"inserted": inserted, "updated": updated})
	}
	fmt.Fprintf(a.Out, "questions: inserted=%d updated=%d\n", inserted, updated)
	return nil
}

func toStoreQuestion(item question.Question) sqlite.Question {
	return sqlite.Question{
		Key: item.Key, Text: item.Text, Category: item.Category,
		Language: item.Language, Version: item.Version,
	}
}

func validCategory(category string) bool {
	for _, allowed := range question.Categories {
		if allowed == strings.TrimSpace(category) {
			return true
		}
	}
	return false
}

// brandTermsStatusDisplay names a status the way the report does, so an import that
// wrote nothing keeps reading as "not declared" instead of a blank field.
func brandTermsStatusDisplay(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "not declared"
	}
	return status
}

// recordedBrandTermsStatus reads back what the database holds for one question set.
// It is the only source the report uses, so an import prints this and not the file's
// value: a set recorded before the status existed cannot be updated later.
func recordedBrandTermsStatus(ctx context.Context, db *sqlite.DB, sha string) (string, error) {
	statuses, err := db.BrandTermsStatuses(ctx, []string{sha})
	if err != nil {
		return "", err
	}
	return statuses[sha], nil
}
