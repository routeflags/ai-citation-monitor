// Package runner executes a measurement plan: it builds the call list, refuses to
// repeat a completed call, keeps concurrency and RPM bounded, and records every
// attempt (success, failure or cancellation).
package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/routeflags/ai-citation-monitor/internal/domain"
	"github.com/routeflags/ai-citation-monitor/internal/providers"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

// Build is the tool version written into every run (release builds override it
// with the tag through -X).
var Build = "0.1.1"

// ToolVersion includes the normalisation rules version: together they decide what
// cited_own and brand_mentioned mean, so records from different versions are not
// silently compared.
func ToolVersion() string { return Build + "+norm" + domain.NormalizerVersion }

// Target is one provider/model pair to measure.
type Target struct {
	Provider string
	Model    string
}

// Item is one planned call.
type Item struct {
	Question    sqlite.Question
	Provider    string
	Model       string
	Stage       string
	Repeat      int
	PlanKey     string
	AlreadyDone bool
}

// Plan is the call list shown before anything is sent.
type Plan struct {
	Items          []Item
	Pending        int
	AlreadyDone    int
	FailedRecorded int
	Repeat         int
	Stage          string
	Targets        []Target
	Questions      int
}

// Runner holds the collaborators of one run.
type Runner struct {
	DB                 *sqlite.DB
	ProjectID          int64
	OwnDomain          string
	Subdomains         bool
	BrandTerms         []string
	Competitors        []string
	Providers          map[string]providers.Provider
	Concurrency        int
	Session            string
	QuestionSetVersion string
	QuestionSetSHA256  string
	Logf               func(format string, args ...any)
	Now                func() time.Time
}

func planKey(projectID int64, question sqlite.Question, provider, model string, repeat int, stage, session string) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "%d|%d|%s|%s|%s|%d|%s|%s",
		projectID, question.ID, question.Version, provider, model, repeat, stage, session)
	return hex.EncodeToString(hash.Sum(nil))[:32]
}

// BuildPlan enumerates every call and marks the ones already attempted. It does
// not touch the network.
//
// A recorded failure blocks its condition for the same session just like a
// success does, because the database keeps one row per condition. Such a call is
// counted in FailedRecorded so the caller can say why nothing is being sent
// instead of looking like a run with nothing to do.
func (r *Runner) BuildPlan(ctx context.Context, questions []sqlite.Question, targets []Target, repeat int, stage string, limit int) (*Plan, error) {
	if repeat <= 0 {
		repeat = 1
	}
	if limit > 0 && limit < len(questions) {
		questions = questions[:limit]
	}
	plan := &Plan{Repeat: repeat, Stage: stage, Targets: targets, Questions: len(questions)}
	for _, target := range targets {
		for _, question := range questions {
			for index := 1; index <= repeat; index++ {
				key := planKey(r.ProjectID, question, target.Provider, target.Model, index, stage, r.Session)
				attempted, succeeded, err := r.DB.PlanState(ctx, key)
				if err != nil {
					return nil, err
				}
				plan.Items = append(plan.Items, Item{
					Question: question, Provider: target.Provider, Model: target.Model,
					Stage: stage, Repeat: index, PlanKey: key, AlreadyDone: attempted,
				})
				switch {
				case succeeded:
					plan.AlreadyDone++
				case attempted:
					plan.AlreadyDone++
					plan.FailedRecorded++
				default:
					plan.Pending++
				}
			}
		}
	}
	return plan, nil
}

// Summary is what the CLI turns into an exit code and a printed table.
type Summary struct {
	Calls             int
	Success           int
	Failed            int
	Cancelled         int
	Skipped           int
	NotRun            int
	TransportFailures int
	ProviderFailures  int
}

// ExitKind classifies the run for the caller's exit code.
//
// "api" and "transport" both mean nothing succeeded and every failure came from
// the provider side; "partial" means some calls succeeded and some did not. A
// run where every call failed is not a partial success, so a script can tell an
// API problem (exit 4) from a mixed result (exit 1) by the reason alone.
func (s *Summary) ExitKind() string {
	switch {
	case s.Calls > 0 && s.Success == 0 && s.Failed > 0 && s.ProviderFailures == s.Failed:
		return "api"
	case s.Calls > 0 && s.Success == 0 && s.TransportFailures > 0:
		return "transport"
	case s.Calls > 0 && s.Failed > 0:
		return "partial"
	case s.Cancelled > 0:
		return "partial"
	default:
		return "ok"
	}
}

// Execute runs the plan. Every attempt is recorded; a cancellation stops new calls
// but keeps what already finished.
func (r *Runner) Execute(ctx context.Context, plan *Plan) (*Summary, error) {
	summary := &Summary{Skipped: plan.AlreadyDone}

	workers := r.Concurrency
	if workers <= 0 {
		workers = 1
	}

	var mutex sync.Mutex

	jobs := make(chan Item)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for item := range jobs {
				if ctx.Err() != nil {
					continue
				}
				result := r.executeOne(ctx, item)
				mutex.Lock()
				summary.Calls++
				switch result.status {
				case providers.StatusSuccess:
					summary.Success++
				case providers.StatusCancelled:
					summary.Cancelled++
				default:
					summary.Failed++
					switch result.errorKind {
					case providers.ErrorKindTransport:
						summary.TransportFailures++
					default:
						summary.ProviderFailures++
					}
				}
				mutex.Unlock()
			}
		}()
	}

	dispatched := 0
dispatch:
	for _, item := range plan.Items {
		if item.AlreadyDone {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			break dispatch
		case jobs <- item:
			dispatched++
		}
	}
	close(jobs)
	wait.Wait()

	pending := plan.Pending
	if dispatched < pending {
		summary.NotRun = pending - dispatched
	}
	return summary, nil
}

type oneResult struct {
	status    string
	errorKind string
}

func (r *Runner) executeOne(ctx context.Context, item Item) oneResult {
	provider, ok := r.Providers[item.Provider]
	if !ok {
		return oneResult{status: providers.StatusError, errorKind: providers.ErrorKindProvider}
	}

	result, err := provider.Ask(ctx, providers.Request{
		QuestionKey:  item.Question.Key,
		QuestionText: item.Question.Text,
		Model:        item.Model,
	})
	if err != nil {
		result = providers.Result{
			Status:       providers.StatusError,
			ErrorKind:    providers.ErrorKindTransport,
			ErrorCode:    "transport_error",
			ErrorMessage: err.Error(),
			StartedAt:    r.now(),
			CompletedAt:  r.now(),
		}
	}

	write := r.buildWrite(item, result)
	// The record must survive a cancelled context, otherwise Ctrl+C loses evidence.
	insertCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, _, err := r.DB.InsertRun(insertCtx, write); err != nil {
		if r.Logf != nil {
			r.Logf("could not record run %s/%s r%d: %v", item.Provider, item.Question.Key, item.Repeat, err)
		}
	}

	return oneResult{
		status:    result.Status,
		errorKind: result.ErrorKind,
	}
}

func (r *Runner) buildWrite(item Item, result providers.Result) sqlite.RunWrite {
	inputs := make([]domain.CitationInput, 0, len(result.Citations))
	for _, citation := range result.Citations {
		// A provider may point at an opaque redirect and name the real site
		// alongside it (Gemini); the hint travels with the citation so the
		// site a run counted is the provider's answer, not the redirect.
		inputs = append(inputs, domain.CitationInput{URL: citation.RawURL, SourceHost: citation.SourceHost})
	}
	details := domain.NormalizeCitationInputs(inputs, r.OwnDomain, r.Subdomains)
	citations := make([]sqlite.CitationWrite, 0, len(details))
	citedOwn := false
	for _, detail := range details {
		if detail.IsOwn {
			citedOwn = true
		}
		citations = append(citations, sqlite.CitationWrite{
			Position:      detail.Position,
			RawURL:        detail.RawURL,
			NormalizedURL: detail.NormalizedURL,
			DedupeKey:     detail.DedupeKey,
			Hostname:      detail.Domain,
			SourceType:    sourceType(detail, r.OwnDomain, r.Subdomains, r.Competitors),
			IsOwn:         detail.IsOwn,
			Malformed:     detail.Malformed,
		})
	}

	usage := sqlite.UsageWrite{
		InputTokens:  result.Usage.InputTokens,
		OutputTokens: result.Usage.OutputTokens,
		TotalTokens:  result.Usage.TotalTokens,
		ToolCalls:    result.Usage.ToolCalls,
	}

	completed := result.CompletedAt
	if completed.IsZero() {
		completed = r.now()
	}
	started := result.StartedAt
	if started.IsZero() {
		started = r.now()
	}

	observability := result.Observability
	if observability == "" {
		observability = providers.ObservabilityUnknown
	}

	// The run column and the mentions rows are two views of one match, so they are
	// computed together: a mention that can only be read as part of a longer word
	// is recorded as ambiguous and does not count as "mentioned".
	mentions := brandMentions(result.AnswerText, r.BrandTerms)
	brandMentioned := false
	for _, mention := range mentions {
		if !mention.Ambiguous {
			brandMentioned = true
			break
		}
	}

	return sqlite.RunWrite{
		ProjectID:             r.ProjectID,
		QuestionID:            item.Question.ID,
		QuestionVersion:       item.Question.Version,
		RepeatIndex:           item.Repeat,
		Provider:              item.Provider,
		Model:                 item.Model,
		APIEndpointVersion:    r.Providers[item.Provider].EndpointVersion(),
		SearchEnabled:         true,
		Stage:                 item.Stage,
		QuestionSetVersion:    r.QuestionSetVersion,
		QuestionSetSHA256:     r.QuestionSetSHA256,
		PlanKey:               item.PlanKey,
		Session:               r.Session,
		StartedAtUTC:          started.UTC().Format("2006-01-02T15:04:05Z"),
		CompletedAtUTC:        completed.UTC().Format("2006-01-02T15:04:05Z"),
		Status:                result.Status,
		CitationObservability: observability,
		CitedOwn:              result.Status == providers.StatusSuccess && citedOwn,
		CitationCount:         len(citations),
		BrandMentioned:        result.Status == providers.StatusSuccess && brandMentioned,
		ToolVersion:           ToolVersion(),
		AnswerText:            result.AnswerText,
		AnswerSHA256:          sha256Hex(result.AnswerText),
		RawResponseJSON:       string(result.RawJSON),
		ErrorCode:             result.ErrorCode,
		ErrorMessage:          result.ErrorMessage,
		Citations:             citations,
		Mentions:              mentions,
		Usage:                 usage,
	}
}

func sourceType(detail domain.CitationDetail, ownDomain string, subdomains bool, competitors []string) string {
	if detail.Malformed || detail.Domain == nil {
		return "unknown"
	}
	switch domain.ClassifyDomain(*detail.Domain, ownDomain, subdomains, competitors) {
	case "own":
		return "own"
	case "competitor":
		return "competitor"
	default:
		return "other"
	}
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// brandMentions records which brand terms hit the answer. The rule lives in
// domain.MatchBrandTerm: ASCII terms match on word boundaries, terms that only
// appear inside a longer word are marked ambiguous and left out of the rate.
func brandMentions(answerText string, terms []string) []sqlite.MentionWrite {
	mentions := make([]sqlite.MentionWrite, 0, len(terms))
	for _, term := range terms {
		match, ok := domain.MatchBrandTerm(answerText, term)
		if !ok {
			continue
		}
		mentions = append(mentions, sqlite.MentionWrite{
			Term:        match.Term,
			Kind:        "brand",
			MatchedText: domain.NormalizeText(match.Term),
			Occurrences: match.Occurrences,
			Ambiguous:   match.Ambiguous,
		})
	}
	return mentions
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
