package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// harness runs the CLI against a local test server. No external network and no
// real credential are ever involved: the API key is a dummy value and the base
// URL points at httptest.
type harness struct {
	t          *testing.T
	dir        string
	configPath string
	dbPath     string
	server     *httptest.Server
	openaiHits atomic.Int32
	geminiHits atomic.Int32
	// openaiBody is served for every /responses call.
	openaiBody string
	geminiBody string
	budgetUSD  float64
}

const openAICited = `{
  "id": "resp_test",
  "status": "completed",
  "output_text": "Example runs the glossary. Rival also has a page.",
  "output": [
    {"type": "web_search_call", "id": "ws_1", "status": "completed"},
    {"type": "message", "role": "assistant", "content": [
      {"type": "output_text", "text": "Example runs the glossary.", "annotations": [
        {"type": "url_citation", "url": "https://example.shop/guide", "title": "guide"},
        {"type": "url_citation", "url": "https://rival.example/page", "title": "rival"},
        {"type": "url_citation", "url": "https://example.shop/guide?utm_source=x#top", "title": "duplicate"}
      ]}
    ]}
  ],
  "usage": {"input_tokens": 1000, "output_tokens": 500, "total_tokens": 1500}
}`

const geminiCited = `{
  "candidates": [
    {"content": {"parts": [{"text": "Example covers it."}]},
     "groundingMetadata": {"webSearchQueries": ["q"], "groundingChunks": [
       {"web": {"uri": "https://example.shop/faq", "title": "faq"}}
     ]}}
  ],
  "usageMetadata": {"promptTokenCount": 900, "candidatesTokenCount": 400, "totalTokenCount": 1300}
}`

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t: t, dir: t.TempDir(), openaiBody: openAICited, geminiBody: geminiCited, budgetUSD: 10,
	}
	h.configPath = filepath.Join(h.dir, "aicm.toml")
	h.dbPath = filepath.Join(h.dir, "measurements", "aicm.db")

	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/responses"):
			h.openaiHits.Add(1)
			if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
				t.Errorf("missing authorization header")
			}
			w.Write([]byte(h.openaiBody))
		case strings.Contains(r.URL.Path, ":generateContent"):
			h.geminiHits.Add(1)
			if r.Header.Get("x-goog-api-key") == "" {
				t.Errorf("missing x-goog-api-key header")
			}
			w.Write([]byte(h.geminiBody))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(h.server.Close)

	t.Setenv("OPENAI_API_KEY", "sk-test-dummy")
	t.Setenv("GEMINI_API_KEY", "gemini-test-dummy")
	h.writeConfig()
	if code, _, stderr := h.run("init", "--non-interactive", "--force", "--site-id", "demo", "--own-domain", "example.shop"); code != ExitOK {
		t.Fatalf("init = %d: %s", code, stderr)
	}
	return h
}

func (h *harness) writeConfig() {
	h.t.Helper()
	config := fmt.Sprintf(`site_id = "demo"
own_domain = "example.shop"
market = "ja-JP"
language = "ja"
db = %q
report_dir = %q
brand_terms = ["Example"]
competitors = ["rival.example"]

[report]
language = "en"

[providers.openai]
model = "gpt-test"
api_key_env = "OPENAI_API_KEY"
base_url = %q
timeout_seconds = 5

[providers.gemini]
model = "gemini-test"
api_key_env = "GEMINI_API_KEY"
base_url = %q
timeout_seconds = 5

[limits]
concurrency = 2
rpm = 6000
max_retries = 0

[budget]
max_usd = %g
currency = "USD"

[pricing."openai/gpt-test"]
input_usd_per_mtok = 1
output_usd_per_mtok = 2
input_tokens_per_call = 1000
output_tokens_per_call = 500

[pricing."gemini/gemini-test"]
input_usd_per_mtok = 1
output_usd_per_mtok = 2
input_tokens_per_call = 1000
output_tokens_per_call = 500
`, h.dbPath, filepath.Join(h.dir, "reports"), h.server.URL, h.server.URL, h.budgetUSD)
	if err := os.WriteFile(h.configPath, []byte(config), 0o644); err != nil {
		h.t.Fatalf("write config: %v", err)
	}
}

// run executes one CLI invocation and returns exit code, stdout and stderr.
func (h *harness) run(args ...string) (int, string, string) {
	h.t.Helper()
	var out, errOut bytes.Buffer
	app := &App{
		ConfigPath: h.configPath,
		Out:        &out,
		Err:        &errOut,
		In:         strings.NewReader(""),
		Now:        func() time.Time { return time.Date(2026, 11, 15, 3, 0, 0, 0, time.UTC) },
	}
	code := app.Run(append([]string{"--config", h.configPath}, args...))
	return code, out.String(), errOut.String()
}

func (h *harness) importQuestions() {
	h.t.Helper()
	code, _, stderr := h.run("questions", "import", "--file", filepath.Join("testdata", "questions.yml"))
	if code != ExitOK {
		h.t.Fatalf("questions import = %d: %s", code, stderr)
	}
}

func (h *harness) query(sqlText string) *sqlHandle {
	h.t.Helper()
	db, err := openForTest(h.dbPath)
	if err != nil {
		h.t.Fatalf("open db: %v", err)
	}
	return &sqlHandle{db: db, t: h.t, query: sqlText}
}

func TestRunDryRunDoesNotTouchTheNetwork(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()

	code, stdout, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--dry-run")
	if code != ExitOK {
		t.Fatalf("exit = %d: %s", code, stderr)
	}
	if h.openaiHits.Load() != 0 {
		t.Fatalf("dry-run sent %d requests", h.openaiHits.Load())
	}
	for _, want := range []string{"questions: 2", "calls:     2 planned", "dry-run: nothing was sent"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
}

func TestRunDryRunWithoutCredentialStillPrintsThePlan(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()
	t.Setenv("OPENAI_API_KEY", "")

	code, stdout, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--dry-run")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: the plan needs no credential: %s", code, stderr)
	}
	if h.openaiHits.Load() != 0 {
		t.Fatalf("dry-run sent %d requests", h.openaiHits.Load())
	}
	for _, want := range []string{"calls:     2 planned", "dry-run: nothing was sent"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "auth: openai OPENAI_API_KEY=missing") {
		t.Errorf("stderr should report the missing credential separately from the plan: %s", stderr)
	}
}

func TestRunWithoutCredentialSendsNothing(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()
	t.Setenv("OPENAI_API_KEY", "")

	code, _, stderr := h.run("run", "--provider", "openai", "--yes")
	if code != ExitAuth {
		t.Fatalf("exit = %d, want %d (credential missing): %s", code, ExitAuth, stderr)
	}
	if h.openaiHits.Load() != 0 {
		t.Fatalf("a missing credential must not send anything (%d requests)", h.openaiHits.Load())
	}
	if !strings.Contains(stderr, "OPENAI_API_KEY") {
		t.Errorf("the error should name the variable to set: %s", stderr)
	}
}

func TestFullPipelineRecordsReportsAndExports(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()

	code, stdout, stderr := h.run("run", "--provider", "openai", "--stage", "smoke", "--repeat", "1", "--yes")
	if code != ExitOK {
		t.Fatalf("run = %d: %s\n%s", code, stderr, stdout)
	}
	if h.openaiHits.Load() != 2 {
		t.Fatalf("requests = %d, want 2 (2 questions x 1 repeat)", h.openaiHits.Load())
	}

	code, stdout, stderr = h.run("report", "--period", "2026-11-15")
	if code != ExitOK {
		t.Fatalf("report = %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "reports/report-2026-11-15_2026-11-15-openai-gpt-test-smoke") {
		t.Errorf("the report file name must carry period, provider, model and stage:\n%s", stdout)
	}

	code, csvOut, stderr := h.run("export", "--format", "csv")
	if code != ExitOK {
		t.Fatalf("export = %d: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(csvOut), "\n")
	if len(lines) != 3 {
		t.Fatalf("csv lines = %d, want a header + 2 rows:\n%s", len(lines), csvOut)
	}

	code, verifyOut, stderr := h.run("verify")
	if code != ExitOK {
		t.Fatalf("verify = %d: %s\n%s", code, stderr, verifyOut)
	}
	if !strings.Contains(verifyOut, "self-consistent") {
		t.Errorf("verify output: %s", verifyOut)
	}
}

func TestIngestIsAppendOnly(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()
	if code, _, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--yes"); code != ExitOK {
		t.Fatalf("first run = %d: %s", code, stderr)
	}

	code, stdout, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--yes")
	if code != ExitOK {
		t.Fatalf("second run = %d: %s", code, stderr)
	}
	if h.openaiHits.Load() != 2 {
		t.Fatalf("requests = %d, want 2: a recorded call must not be repeated", h.openaiHits.Load())
	}
	if !strings.Contains(stdout, "calls:     0 planned, 2 already recorded") {
		t.Errorf("stdout:\n%s", stdout)
	}

	counts := h.query("SELECT COUNT(*) FROM runs")
	defer counts.close()
	if got := counts.int64(); got != 2 {
		t.Fatalf("runs = %d, want 2", got)
	}
}

func TestDifferentModelIsADifferentGroup(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()
	if code, _, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--yes"); code != ExitOK {
		t.Fatalf("run a = %d: %s", code, stderr)
	}
	if code, _, stderr := h.run("run", "--provider", "openai", "--model", "gpt-other", "--repeat", "1", "--yes"); code != ExitOK {
		t.Fatalf("run b = %d: %s", code, stderr)
	}

	code, stdout, stderr := h.run("report", "--period", "2026-11-15", "--stdout")
	if code != ExitOK {
		t.Fatalf("report = %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "gpt-test") || !strings.Contains(stdout, "gpt-other") {
		t.Fatalf("both models must appear as separate groups:\n%s", stdout)
	}
}

func TestReportSeparatesProvidersAndShowsDenominators(t *testing.T) {
	h := newHarness(t)
	// Only one question so the denominators are easy to read.
	if code, _, stderr := h.run("questions", "import", "--file", filepath.Join("testdata", "questions.csv")); code != ExitOK {
		t.Fatalf("import: %s", stderr)
	}
	if code, _, stderr := h.run("run", "--provider", "openai,gemini", "--repeat", "1", "--yes"); code != ExitOK {
		t.Fatalf("run = %d: %s", code, stderr)
	}

	code, stdout, _ := h.run("report", "--period", "2026-11-15", "--stdout")
	if code != ExitOK {
		t.Fatalf("report = %d", code)
	}
	if strings.Contains(stdout, "— (not measured) (n=0/0)") {
		t.Errorf("measured groups should not show the unmeasured form:\n%s", stdout)
	}
	if !strings.Contains(stdout, "(n=2/2)") {
		t.Errorf("a rate must carry its denominator:\n%s", stdout)
	}
	if !strings.Contains(stdout, "openai") || !strings.Contains(stdout, "gemini") {
		t.Errorf("both providers must be reported separately:\n%s", stdout)
	}
}

func TestReportWithoutRunsSaysNotMeasured(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()
	code, stdout, stderr := h.run("report", "--period", "2026-11-15", "--stdout")
	if code != ExitOK {
		t.Fatalf("report = %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "— (not measured)") {
		t.Errorf("an empty period must print the unmeasured form, not 0%%:\n%s", stdout)
	}
	if strings.Contains(stdout, "0.0%") {
		t.Errorf("an empty period must not print 0.0%%:\n%s", stdout)
	}
}

func TestBudgetStopsBeforeSending(t *testing.T) {
	h := newHarness(t)
	h.budgetUSD = 0.000001
	h.writeConfig()
	h.importQuestions()

	code, _, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--yes")
	if code != ExitBudget {
		t.Fatalf("exit = %d, want %d: %s", code, ExitBudget, stderr)
	}
	if h.openaiHits.Load() != 0 {
		t.Fatalf("the budget check must run before the first request (%d sent)", h.openaiHits.Load())
	}
}

func TestTransportFailureExits4AndKeepsNoFalseSuccess(t *testing.T) {
	h := newHarness(t)
	h.server.Close() // the base URL now refuses connections
	h.importQuestions()

	code, stdout, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--yes")
	if code != ExitAPI {
		t.Fatalf("exit = %d, want %d: %s\n%s", code, ExitAPI, stderr, stdout)
	}
	counts := h.query("SELECT COUNT(*) FROM runs WHERE status = 'error'")
	defer counts.close()
	if got := counts.int64(); got != 2 {
		t.Fatalf("error runs = %d, want 2 (failures are recorded)", got)
	}
}

func TestVerifyFindsTamperedAnswer(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()
	if code, _, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--yes"); code != ExitOK {
		t.Fatalf("run = %d: %s", code, stderr)
	}
	write := h.query("UPDATE responses SET answer_text = 'rewritten'")
	write.exec()
	write.close()

	code, stdout, stderr := h.run("verify")
	if code != ExitGeneral {
		t.Fatalf("verify = %d, want %d: %s", code, ExitGeneral, stderr)
	}
	if !strings.Contains(stdout, "answer_sha256_mismatch") {
		t.Errorf("verify output:\n%s", stdout)
	}
}

func TestConfigProblemsExit2(t *testing.T) {
	h := newHarness(t)
	content := `site_id = ""\nown_domain = ""`
	if err := os.WriteFile(h.configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := h.run("run", "--provider", "openai", "--dry-run")
	if code != ExitConfig {
		t.Fatalf("exit = %d, want %d: %s", code, ExitConfig, stderr)
	}
}

func TestJSONOutputIsOnlyJSON(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()
	code, stdout, stderr := h.run("--json", "report", "--period", "2026-11-15", "--stdout")
	if code != ExitOK {
		t.Fatalf("report = %d: %s", code, stderr)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON only: %v\n%s", err, stdout)
	}
}
