package runner

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/routeflags/ai-citation-monitor/internal/providers"
	"github.com/routeflags/ai-citation-monitor/internal/storage/sqlite"
)

// fakeProvider needs no network: it returns a fixed result.
type fakeProvider struct {
	name    string
	calls   atomic.Int32
	result  providers.Result
	delay   time.Duration
	blockOn *atomic.Bool
}

func (f *fakeProvider) Name() string            { return f.name }
func (f *fakeProvider) EndpointVersion() string { return "fake/v1" }
func (f *fakeProvider) Ask(ctx context.Context, req providers.Request) (providers.Result, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return providers.Result{Status: providers.StatusCancelled, ErrorKind: providers.ErrorKindCancelled}, nil
		case <-time.After(f.delay):
		}
	}
	result := f.result
	result.StartedAt = time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)
	result.CompletedAt = result.StartedAt.Add(time.Second)
	return result, nil
}

func setup(t *testing.T) (*Runner, sqlite.Question, *sqlite.DB) {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "aicm.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if _, err := db.EnsureProject(ctx, sqlite.Project{
		SiteID: "demo", OwnDomain: "example.shop", Subdomains: true, Market: "ja-JP",
	}); err != nil {
		t.Fatal(err)
	}
	project, err := db.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	question, err := db.UpsertQuestion(ctx, project.ID, sqlite.Question{
		Key: "Q01", Text: "THCVとは？", Category: "definition",
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := &Runner{
		DB: db, ProjectID: project.ID, OwnDomain: "example.shop", Subdomains: true,
		BrandTerms: []string{"Example"}, Concurrency: 2, Session: "2026-11-15",
		QuestionSetVersion: "1.0.0", QuestionSetSHA256: "sha",
		Now: func() time.Time { return time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC) },
	}
	return engine, question, db
}

func successResult() providers.Result {
	in, out, total, tools := 1000, 500, 1500, 1
	return providers.Result{
		Status: providers.StatusSuccess, Observability: providers.ObservabilityObserved,
		AnswerText: "Example answers this.",
		Citations:  []providers.Citation{{RawURL: "https://example.shop/a"}},
		Usage:      providers.Usage{InputTokens: &in, OutputTokens: &out, TotalTokens: &total, ToolCalls: &tools},
		RawJSON:    []byte(`{"ok":true}`),
	}
}

func TestPlanSkipsAlreadyRecordedCalls(t *testing.T) {
	engine, question, _ := setup(t)
	fake := &fakeProvider{name: "fake", result: successResult()}
	engine.Providers = map[string]providers.Provider{"fake": fake}
	targets := []Target{{Provider: "fake", Model: "model"}}

	plan, err := engine.BuildPlan(context.Background(), []sqlite.Question{question}, targets, 2, "smoke", 0)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Pending != 2 || plan.AlreadyDone != 0 {
		t.Fatalf("plan = %+v", plan)
	}

	summary, err := engine.Execute(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Calls != 2 || summary.Success != 2 {
		t.Fatalf("summary = %+v", summary)
	}

	again, err := engine.BuildPlan(context.Background(), []sqlite.Question{question}, targets, 2, "smoke", 0)
	if err != nil {
		t.Fatal(err)
	}
	if again.Pending != 0 || again.AlreadyDone != 2 {
		t.Fatalf("a resumed plan must skip both calls: %+v", again)
	}
	summary, err = engine.Execute(context.Background(), again)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Calls != 0 || fake.calls.Load() != 2 {
		t.Fatalf("the provider was called again: calls=%d summary=%+v", fake.calls.Load(), summary)
	}
}

func TestCancellationStopsNewCallsAndKeepsTheRest(t *testing.T) {
	engine, question, db := setup(t)
	fake := &fakeProvider{name: "fake", result: successResult(), delay: 5 * time.Millisecond}
	engine.Providers = map[string]providers.Provider{"fake": fake}
	engine.Concurrency = 1
	targets := []Target{{Provider: "fake", Model: "model"}}

	plan, err := engine.BuildPlan(context.Background(), []sqlite.Question{question}, targets, 4, "smoke", 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summary, err := engine.Execute(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if summary.NotRun == 0 {
		t.Fatalf("the cancelled calls must be reported as not run: %+v", summary)
	}
	runs, err := db.Runs(context.Background(), sqlite.RunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) > 0 && len(runs) > 4 {
		t.Fatalf("recorded more runs than planned: %d", len(runs))
	}
}

func TestCitationNormalisationRunsOnRecordedResults(t *testing.T) {
	engine, question, db := setup(t)
	result := successResult()
	result.Citations = []providers.Citation{
		{RawURL: "https://example.shop/a"},
		{RawURL: "https://example.shop/a?utm=x#y"},
		{RawURL: "mailto:x@y"},
	}
	fake := &fakeProvider{name: "fake", result: result}
	engine.Providers = map[string]providers.Provider{"fake": fake}
	targets := []Target{{Provider: "fake", Model: "model"}}

	plan, err := engine.BuildPlan(context.Background(), []sqlite.Question{question}, targets, 1, "smoke", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	runs, err := db.Runs(context.Background(), sqlite.RunFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	if runs[0].CitationCount != 2 {
		t.Fatalf("citation count = %d, want 2 (duplicate folded, malformed kept)", runs[0].CitationCount)
	}
	if !runs[0].CitedOwn {
		t.Error("the own citation was not recognised")
	}
	if !runs[0].BrandMentioned {
		t.Error("the brand mention was not recognised")
	}
}
