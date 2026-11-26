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
	t              *testing.T
	dir            string
	configPath     string
	dbPath         string
	server         *httptest.Server
	openaiHits     atomic.Int32
	geminiHits     atomic.Int32
	anthropicHits  atomic.Int32
	perplexityHits atomic.Int32
	grokHits       atomic.Int32
	// openaiBody is served for every /responses call.
	openaiBody     string
	openaiStatus   int
	geminiBody     string
	anthropicBody  string
	perplexityBody string
	grokBody       string
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

const openAIInvalidKey = `{
  "error": {
    "message": "Incorrect API key provided: sk-test-****. You can find your API key at https://platform.openai.com/account/api-keys.",
    "type": "invalid_request_error",
    "code": "invalid_api_key",
    "param": null
  },
  "status": 401
}`

const anthropicCited = `{
  "id": "msg_test",
  "type": "message",
  "role": "assistant",
  "model": "claude-test",
  "content": [
    {"type": "server_tool_use", "id": "srvtoolu_1", "name": "web_search", "input": {"query": "q"}},
    {"type": "web_search_tool_result", "tool_use_id": "srvtoolu_1", "content": [
      {"type": "web_search_result", "url": "https://example.shop/faq", "title": "faq"}
    ]},
    {"type": "text", "text": "Example Shop answers it. Rival also has a page.", "citations": [
      {"type": "web_search_result_location", "url": "https://example.shop/faq", "title": "faq", "encrypted_index": "e1", "cited_text": "..."},
      {"type": "web_search_result_location", "url": "https://rival.example/page", "title": "rival", "encrypted_index": "e2", "cited_text": "..."}
    ]}
  ],
  "stop_reason": "end_turn",
	  "usage": {"input_tokens": 800, "output_tokens": 300, "server_tool_use": {"web_search_requests": 1}}
}`

// grokCited is the xAI Responses shape: a web_search_call item, a message whose
// output_text carries positional url_citation annotations, and the usage block
// with the web search counter. The top-level `citations` array is present on
// purpose: it lists what the search saw, and only the annotations may count.
const grokCited = `{
  "id": "resp_grok_test",
  "object": "response",
  "status": "completed",
  "output": [
    {"type": "web_search_call", "id": "ws_1", "status": "completed", "action": {"type": "search", "query": "q"}},
    {"type": "message", "role": "assistant", "status": "completed", "content": [
      {"type": "output_text", "text": "Example Shop answers it.[[1]](https://example.shop/faq) Rival also has a page.[[2]](https://rival.example/page)", "annotations": [
        {"type": "url_citation", "url": "https://example.shop/faq", "start_index": 24, "end_index": 65, "title": "1"},
        {"type": "url_citation", "url": "https://rival.example/page", "start_index": 84, "end_index": 124, "title": "2"}
      ]}
    ]}
  ],
  "citations": ["https://example.shop/faq", "https://rival.example/page", "https://unused.example/x"],
  "usage": {
    "input_tokens": 700, "output_tokens": 250, "total_tokens": 950,
    "num_server_side_tools_used": 1,
    "server_side_tool_usage_details": {"web_search_calls": 1}
  }
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

// perplexityCited copies the documented Agent API shape: a search_results item with
// the sources, a message whose content parts carry the url_citation annotations,
// and usage with the search tool under `tool_calls_details.search_web`. The two
// sources appear in both places, so the adapter must count each URL once.
const perplexityCited = `{
  "id": "resp_test",
  "object": "response",
  "created_at": 1781000000,
  "status": "completed",
  "model": "openai/gpt-test",
  "output": [
    {"type": "search_results", "queries": ["Example Shop"], "results": [
      {"id": 1, "url": "https://example.shop/guide", "title": "guide", "snippet": "...", "date": "2026-05-01", "last_updated": "2026-05-01", "source": "web"},
      {"id": 2, "url": "https://rival.example/page", "title": "rival", "snippet": "...", "date": null, "last_updated": "2026-05-02", "source": "web"}
    ]},
    {"type": "message", "role": "assistant", "status": "completed", "content": [
      {"type": "output_text", "text": "Example runs the glossary. Rival also has a page.", "annotations": [
        {"type": "url_citation", "url": "https://example.shop/guide", "title": "guide", "start_index": 0, "end_index": 7},
        {"type": "url_citation", "url": "https://rival.example/page", "title": "rival", "start_index": 30, "end_index": 35}
      ]}
    ]}
  ],
  "usage": {"input_tokens": 1000, "output_tokens": 300, "total_tokens": 1300,
            "tool_calls_details": {"search_web": {"invocation": 1}}}
}`

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t: t, dir: t.TempDir(), openaiBody: openAICited, geminiBody: geminiCited,
		anthropicBody: anthropicCited, perplexityBody: perplexityCited, grokBody: grokCited,
	}
	h.configPath = filepath.Join(h.dir, "aicm.toml")
	h.dbPath = filepath.Join(h.dir, "measurements", "aicm.db")

	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/perplexity/"):
			// base_url + /agent, so the path also proves the adapter appends the
			// Agent API path and not /responses.
			if r.URL.Path != "/perplexity/v1/agent" {
				t.Errorf("perplexity path = %s, want /perplexity/v1/agent", r.URL.Path)
			}
			h.perplexityHits.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer perplexity-test-dummy" {
				t.Errorf("perplexity authorization = %q", got)
			}
			w.Write([]byte(h.perplexityBody))
		case r.URL.Path == "/xai/responses":
			// Grok speaks the same Responses path as OpenAI, so the harness gives
			// it its own base-URL prefix. The match is exact, not a suffix: a
			// suffix rule would serve the OpenAI fixture to grok and the test
			// would still pass on the shared output[]/annotations[] shape.
			h.grokHits.Add(1)
			if got := r.Header.Get("Authorization"); got != "Bearer xai-test-dummy" {
				t.Errorf("grok authorization = %q, want the configured key", got)
			}
			w.Write([]byte(h.grokBody))
		case r.URL.Path == "/responses":
			h.openaiHits.Add(1)
			if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
				t.Errorf("missing authorization header")
			}
			status := h.openaiStatus
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			w.Write([]byte(h.openaiBody))
		case strings.Contains(r.URL.Path, ":generateContent"):
			h.geminiHits.Add(1)
			if r.Header.Get("x-goog-api-key") == "" {
				t.Errorf("missing x-goog-api-key header")
			}
			w.Write([]byte(h.geminiBody))
		case strings.HasSuffix(r.URL.Path, "/v1/messages"):
			h.anthropicHits.Add(1)
			if r.Header.Get("x-api-key") == "" {
				t.Errorf("missing x-api-key header")
			}
			if r.Header.Get("anthropic-version") == "" {
				t.Errorf("missing anthropic-version header")
			}
			w.Write([]byte(h.anthropicBody))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(h.server.Close)

	t.Setenv("OPENAI_API_KEY", "sk-test-dummy")
	t.Setenv("GEMINI_API_KEY", "gemini-test-dummy")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-dummy")
	t.Setenv("PERPLEXITY_API_KEY", "perplexity-test-dummy")
	t.Setenv("XAI_API_KEY", "xai-test-dummy")
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

[providers.anthropic]
model = "claude-test"
api_key_env = "ANTHROPIC_API_KEY"
base_url = %q
timeout_seconds = 5
max_tokens = 2048
max_uses = 3

[providers.perplexity]
model = "openai/gpt-test"
api_key_env = "PERPLEXITY_API_KEY"
base_url = %q
timeout_seconds = 5

[providers.grok]
model = "grok-test"
api_key_env = "XAI_API_KEY"
base_url = %q
timeout_seconds = 5

[limits]
concurrency = 2
rpm = 6000
max_retries = 0
`, h.dbPath, filepath.Join(h.dir, "reports"),
		h.server.URL, h.server.URL, h.server.URL,
		h.server.URL+"/perplexity/v1", h.server.URL+"/xai")
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

// brokenNesting drops the smoke question from initial, so smoke is no longer a
// subset of initial.
const brokenNesting = `set_version: "1.0.0"
site_id: demo
own_domain: example.shop
market: ja-JP
brand_terms:
  - Example
questions:
  - { id: Q01, text: "THCVとTHCの違いは？", category: comparison }
  - { id: Q02, text: "THCHとは何ですか？", category: definition }
stages:
  smoke:
    runs_per_question: 1
    question_ids: [Q01, Q02]
  initial:
    runs_per_question: 3
    question_ids: [Q02]
`

func writeQuestionSet(t *testing.T, h *harness, content string) string {
	t.Helper()
	path := filepath.Join(h.dir, "questions-strict.yml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write question set: %v", err)
	}
	return path
}

func TestQuestionsImportWarnsAboutBrokenNestingByDefault(t *testing.T) {
	h := newHarness(t)
	path := writeQuestionSet(t, h, brokenNesting)

	code, stdout, stderr := h.run("questions", "import", "--file", path)
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0 (a warning must not stop the import): %s", code, stderr)
	}
	if !strings.Contains(stderr, "W040") {
		t.Fatalf("stderr should carry the nesting warning:\n%s", stderr)
	}
	if !strings.Contains(stdout, "inserted=2") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestQuestionsImportStrictTurnsTheNestingWarningIntoAnError(t *testing.T) {
	h := newHarness(t)
	path := writeQuestionSet(t, h, brokenNesting)

	for _, extra := range [][]string{
		{"--strict"},
		{"--strict", "--check"},
		{"--strict", "--dry-run"},
	} {
		args := append([]string{"questions", "import", "--file", path}, extra...)
		code, stdout, stderr := h.run(args...)
		if code != ExitGeneral {
			t.Fatalf("%v: exit = %d, want %d: %s", extra, code, ExitGeneral, stderr)
		}
		if !strings.Contains(stderr, "E040") {
			t.Errorf("%v: the promoted error must keep the code:\n%s", extra, stderr)
		}
		if strings.Contains(stdout, "OK:") {
			t.Errorf("%v: a strict failure must not report OK:\n%s", extra, stdout)
		}
	}

	counts := h.query("SELECT COUNT(*) FROM questions")
	defer counts.close()
	if got := counts.int64(); got != 0 {
		t.Fatalf("questions = %d, want 0: a strict failure writes nothing", got)
	}
}

func TestQuestionsImportStrictJSONFailureIsOnlyJSON(t *testing.T) {
	h := newHarness(t)
	path := writeQuestionSet(t, h, brokenNesting)

	code, stdout, stderr := h.run("--json", "questions", "import", "--file", path, "--strict")
	if code != ExitGeneral {
		t.Fatalf("exit = %d, want %d", code, ExitGeneral)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("a strict failure must not write a data document on stdout:\n%s", stdout)
	}
	// The per-problem lines and the JSON error both go to stderr: the last line is
	// the machine readable failure, the lines before it say which codes fired.
	if !strings.Contains(stderr, "E040") {
		t.Errorf("stderr should name the promoted code:\n%s", stderr)
	}
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	last := lines[len(lines)-1]
	var payload struct {
		Error struct {
			Code   int    `json:"code"`
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(last), &payload); err != nil {
		t.Fatalf("the last stderr line is not the JSON error document: %v\n%s", err, stderr)
	}
	if payload.Error.Code != ExitGeneral || payload.Error.Reason != "question_set_invalid" {
		t.Errorf("error = %+v, want code 1 / question_set_invalid", payload.Error)
	}
}

func TestQuestionsImportStrictAcceptsACorrectSet(t *testing.T) {
	h := newHarness(t)

	// The nesting contract holds here, so --strict must not fire.
	code, _, stderr := h.run("questions", "import", "--file", filepath.Join("testdata", "questions.yml"), "--strict")
	if code != ExitOK {
		t.Fatalf("yaml: exit = %d, want 0: %s", code, stderr)
	}
	// CSV carries no stages, so --strict has nothing to promote: the same command
	// line must keep working.
	code, stdout, stderr := h.run("questions", "import", "--file", filepath.Join("testdata", "questions.csv"), "--strict", "--dry-run")
	if code != ExitOK {
		t.Fatalf("csv: exit = %d, want 0: %s", code, stderr)
	}
	if !strings.Contains(stdout, "questions: 2") {
		t.Errorf("stdout:\n%s", stdout)
	}
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

// A recorded run must carry the app clock, not the wall clock. Providers stamp
// the start and end of a call, so they need the same clock as the runner;
// otherwise a report for a past period stops being reproducible once the real
// date moves on.
func TestRunTimestampsComeFromTheAppClock(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()

	if code, _, stderr := h.run("run", "--provider", "openai", "--stage", "smoke", "--repeat", "1", "--yes"); code != ExitOK {
		t.Fatalf("run = %d: %s", code, stderr)
	}

	// The harness clock is 2026-11-15T03:00:00Z; a real datetime here means an
	// adapter kept its own time.Now instead of the injected clock.
	q := h.query("SELECT COUNT(*) FROM runs WHERE started_at_utc = '2026-11-15T03:00:00Z' AND completed_at_utc = '2026-11-15T03:00:00Z'")
	defer q.close()
	if got := q.int64(); got != 2 {
		t.Fatalf("runs stamped by the app clock = %d, want 2 (2 questions x 1 repeat)", got)
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
	// The report path is printed with the platform separator; compare on a
	// normalised copy so the assertion is about the name, not about Windows.
	if !strings.Contains(strings.ReplaceAll(stdout, `\`, "/"), "reports/report-2026-11-15_2026-11-15-openai-gpt-test-smoke") {
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

func TestEveryCallFailingAtTheProviderExits4(t *testing.T) {
	// 1 of 1 calls failing is not a partial success: a script must be able to tell
	// an API problem (exit 4, reason api_failure) from a mixed result (exit 1).
	h := newHarness(t)
	h.importQuestions()
	h.openaiBody = openAIInvalidKey
	h.openaiStatus = http.StatusUnauthorized

	code, stdout, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--yes")
	if code != ExitAPI {
		t.Fatalf("exit = %d, want %d: %s\n%s", code, ExitAPI, stderr, stdout)
	}
	counts := h.query(`SELECT COUNT(*) FROM runs r JOIN responses resp ON resp.run_id = r.id
		WHERE r.status = 'error' AND resp.error_code = 'invalid_api_key'`)
	defer counts.close()
	if got := counts.int64(); got != 2 {
		t.Fatalf("runs with the provider's error code = %d, want 2 (the real error envelope must be read)", got)
	}
}

func TestAFailedCallHoldsItsConditionForTheSession(t *testing.T) {
	// A failure is recorded but is not a measurement. The database keeps one row
	// per measurement condition, so the same session does not resend it: the plan
	// must say that instead of looking like a run with nothing to do, and
	// --session is how an operator asks for a real retry.
	h := newHarness(t)
	h.importQuestions()
	h.openaiBody = openAIInvalidKey
	h.openaiStatus = http.StatusUnauthorized
	if code, _, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--yes"); code != ExitAPI {
		t.Fatalf("the failing run = %d, want %d: %s", code, ExitAPI, stderr)
	}

	h.openaiBody = openAICited
	h.openaiStatus = http.StatusOK
	code, stdout, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--yes")
	if code != ExitOK {
		t.Fatalf("the second run = %d, want 0 (nothing was sent): %s\n%s", code, stderr, stdout)
	}
	if h.openaiHits.Load() != 2 {
		t.Fatalf("requests = %d, want 2: the failed conditions are not resent in the same session", h.openaiHits.Load())
	}
	if !strings.Contains(stdout, "calls:     0 planned, 2 already recorded") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "2 of the recorded calls failed") || !strings.Contains(stdout, "--session") {
		t.Errorf("the plan must explain why nothing was sent and how to retry:\n%s", stdout)
	}

	// A new session is a new measurement condition, so the calls are sent again.
	code, stdout, stderr = h.run("run", "--provider", "openai", "--repeat", "1", "--session", "retry-1", "--yes")
	if code != ExitOK {
		t.Fatalf("the retry session = %d: %s\n%s", code, stderr, stdout)
	}
	if h.openaiHits.Load() != 4 {
		t.Fatalf("requests = %d, want 4 (2 failed + 2 measured again in a new session)", h.openaiHits.Load())
	}
	total := h.query("SELECT COUNT(*) FROM runs")
	defer total.close()
	if got := total.int64(); got != 4 {
		t.Fatalf("runs = %d, want 4: the retry is appended, nothing is overwritten", got)
	}
	successes := h.query("SELECT COUNT(*) FROM runs WHERE status = 'success'")
	defer successes.close()
	if got := successes.int64(); got != 2 {
		t.Fatalf("successful runs = %d, want 2", got)
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

func TestPublicSuffixOwnDomainExits2(t *testing.T) {
	// Every customer of github.io or co.jp would otherwise count as us.
	for _, own := range []string{"github.io", "co.jp", "com"} {
		h := newHarness(t)
		content := fmt.Sprintf(`site_id = "demo"
own_domain = %q
market = "ja-JP"

[providers.openai]
model = "gpt-test"
api_key_env = "OPENAI_API_KEY"
`, own)
		if err := os.WriteFile(h.configPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		code, _, stderr := h.run("run", "--provider", "openai", "--dry-run")
		if code != ExitConfig {
			t.Fatalf("own_domain %q: exit = %d, want %d: %s", own, code, ExitConfig, stderr)
		}
		if !strings.Contains(stderr, "public suffix") {
			t.Errorf("own_domain %q: the message must name the problem:\n%s", own, stderr)
		}
	}

	// A normal domain is not blocked.
	h := newHarness(t)
	h.importQuestions()
	if code, _, stderr := h.run("run", "--provider", "openai", "--dry-run"); code != ExitOK {
		t.Fatalf("example.shop must be accepted: %d: %s", code, stderr)
	}
}

func TestAnthropicIsARegisteredProvider(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()

	// --provider anthropic runs through the same plan and recording path.
	code, stdout, stderr := h.run("run", "--provider", "anthropic", "--repeat", "1", "--yes")
	if code != ExitOK {
		t.Fatalf("run = %d: %s\n%s", code, stderr, stdout)
	}
	if h.anthropicHits.Load() != 2 {
		t.Fatalf("anthropic requests = %d, want 2 (2 questions x 1 repeat)", h.anthropicHits.Load())
	}
	if h.openaiHits.Load() != 0 {
		t.Fatalf("openai was called (%d) although only anthropic was selected", h.openaiHits.Load())
	}

	runs := h.query("SELECT COUNT(*) FROM runs WHERE provider = 'anthropic' AND api_endpoint_version LIKE 'messages/v1%'")
	defer runs.close()
	if got := runs.int64(); got != 2 {
		t.Fatalf("anthropic runs = %d, want 2 with the messages/v1 endpoint version", got)
	}
	cited := h.query("SELECT COUNT(*) FROM runs WHERE provider = 'anthropic' AND cited_own = 1 AND citation_count = 2")
	defer cited.close()
	if cited.int64() != 2 {
		t.Errorf("the cited fixture should record cited_own with two citations")
	}

	// Two providers in one run keep separate groups.
	code, stdout, stderr = h.run("run", "--provider", "openai,anthropic", "--repeat", "1", "--session", "both", "--yes")
	if code != ExitOK {
		t.Fatalf("run both = %d: %s\n%s", code, stderr, stdout)
	}
	if h.openaiHits.Load() != 2 || h.anthropicHits.Load() != 4 {
		t.Fatalf("hits: openai=%d anthropic=%d, want 2 and 4", h.openaiHits.Load(), h.anthropicHits.Load())
	}
}

func TestPerplexityIsARegisteredProvider(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()

	// The Agent API path, its own endpoint version, and the citations it reported
	// (search sources and annotations merged, each URL once).
	code, stdout, stderr := h.run("run", "--provider", "perplexity", "--repeat", "1", "--yes")
	if code != ExitOK {
		t.Fatalf("perplexity run = %d: %s\n%s", code, stderr, stdout)
	}
	if h.perplexityHits.Load() != 2 {
		t.Fatalf("perplexity requests = %d, want 2 (2 questions x 1 repeat)", h.perplexityHits.Load())
	}
	if h.openaiHits.Load() != 0 || h.anthropicHits.Load() != 0 || h.geminiHits.Load() != 0 {
		t.Fatalf("another provider was called (openai=%d gemini=%d anthropic=%d) although only perplexity was selected",
			h.openaiHits.Load(), h.geminiHits.Load(), h.anthropicHits.Load())
	}
	runs := h.query("SELECT COUNT(*) FROM runs WHERE provider = 'perplexity' AND api_endpoint_version = 'agent/v1+web_search'")
	defer runs.close()
	if got := runs.int64(); got != 2 {
		t.Fatalf("perplexity runs = %d, want 2 with the agent/v1+web_search endpoint version", got)
	}
	cited := h.query("SELECT COUNT(*) FROM runs WHERE provider = 'perplexity' AND cited_own = 1 AND citation_count = 2")
	defer cited.close()
	if cited.int64() != 2 {
		t.Errorf("the cited fixture should record cited_own with the two unique citations")
	}

	// Two providers in one run keep separate groups.
	code, stdout, stderr = h.run("run", "--provider", "openai,perplexity", "--repeat", "1", "--session", "both", "--yes")
	if code != ExitOK {
		t.Fatalf("run both = %d: %s\n%s", code, stderr, stdout)
	}
	if h.openaiHits.Load() != 2 || h.perplexityHits.Load() != 4 {
		t.Fatalf("hits: openai=%d perplexity=%d, want 2 and 4", h.openaiHits.Load(), h.perplexityHits.Load())
	}
}

// TestGrokIsARegisteredProvider runs the xAI adapter through the same plan and
// recording path as the other providers: grok is called, nothing else is, and
// the record carries the xAI surface.
func TestGrokIsARegisteredProvider(t *testing.T) {
	h := newHarness(t)

	// init rewrites the configuration from the parsed struct, so a missing block
	// in config.Save would silently drop the harness base URL and send the test
	// at api.x.ai. Read the file back before any call is made.
	saved := readFileString(t, h.configPath)
	for _, want := range []string{
		"[providers.grok]",
		`model = "grok-test"`,
		`api_key_env = "XAI_API_KEY"`,
		fmt.Sprintf("base_url = %q", h.server.URL+"/xai"),
	} {
		if !strings.Contains(saved, want) {
			t.Fatalf("the saved config lost %q:\n%s", want, saved)
		}
	}
	if strings.Contains(saved, "xai-test-dummy") {
		t.Fatal("the credential value was written to the config")
	}

	h.importQuestions()

	code, stdout, stderr := h.run("run", "--provider", "grok", "--repeat", "1", "--yes")
	if code != ExitOK {
		t.Fatalf("run = %d: %s\n%s", code, stderr, stdout)
	}
	if h.grokHits.Load() != 2 {
		t.Fatalf("grok requests = %d, want 2 (2 questions x 1 repeat)", h.grokHits.Load())
	}
	if h.openaiHits.Load() != 0 {
		t.Fatalf("openai was called (%d) although only grok was selected", h.openaiHits.Load())
	}

	runs := h.query("SELECT COUNT(*) FROM runs WHERE provider = 'grok' AND api_endpoint_version = 'responses/v1+web_search+xai'")
	defer runs.close()
	if got := runs.int64(); got != 2 {
		t.Fatalf("grok runs = %d, want 2 with the xAI endpoint version", got)
	}
	// Only the annotations count: the fixture's top-level citations array names
	// three URLs, one of which the answer never cites.
	cited := h.query("SELECT COUNT(*) FROM runs WHERE provider = 'grok' AND cited_own = 1 AND citation_count = 2")
	defer cited.close()
	if cited.int64() != 2 {
		t.Errorf("the cited fixture should record cited_own with two citations")
	}
	usage := h.query("SELECT COUNT(*) FROM usage_records u JOIN runs r ON r.id = u.run_id WHERE r.provider = 'grok' AND u.tool_calls = 1 AND u.total_tokens = 950")
	defer usage.close()
	if usage.int64() != 2 {
		t.Errorf("the usage record should carry the reported tokens and one tool call")
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestVersionListsTheProvidersAndVersions(t *testing.T) {
	h := newHarness(t)

	code, stdout, stderr := h.run("version")
	if code != ExitOK {
		t.Fatalf("version = %d: %s", code, stderr)
	}
	for _, want := range []string{
		"openai", "gemini", "anthropic", "messages/v1",
		"perplexity", "agent/v1+web_search",
		"grok", "responses/v1+web_search+xai",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("version output does not contain %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stdout, "observed once") {
		t.Errorf("the version output must state what was observed live and what was not:\n%s", stdout)
	}
	if !strings.Contains(stdout, "NOT observed: perplexity, grok") {
		t.Errorf("the version output must name the providers that were never called live:\n%s", stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "live API:") && (strings.Contains(line, "perplexity:") || strings.Contains(line, "grok:")) {
			t.Errorf("the live observation line claims an unobserved provider: %q", line)
		}
	}
	if !strings.Contains(stdout, "never call a provider") {
		t.Errorf("the version output must say the tests stay offline:\n%s", stdout)
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

func TestRunCanStoreOnlyTheAnswerHash(t *testing.T) {
	h := newHarness(t)
	h.importQuestions()

	// The plan says which mode is in effect before anything is sent.
	code, stdout, stderr := h.run("run", "--provider", "openai", "--repeat", "1", "--store", "hash-only", "--dry-run")
	if code != ExitOK {
		t.Fatalf("dry-run = %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "store:     hash-only") {
		t.Errorf("the plan must name the store mode:\n%s", stdout)
	}

	code, _, stderr = h.run("run", "--provider", "openai", "--repeat", "1", "--store", "hash-only", "--yes")
	if code != ExitOK {
		t.Fatalf("run = %d: %s", code, stderr)
	}
	kept := h.query(`SELECT COUNT(*) FROM responses
		WHERE body_stored = 0 AND answer_text = '' AND raw_response_json = ''
		  AND answer_sha256 <> ''`)
	defer kept.close()
	if got := kept.int64(); got != 2 {
		t.Fatalf("hash-only rows = %d, want 2 (body empty, hash kept)", got)
	}
	if code, stdout, stderr := h.run("verify"); code != ExitOK {
		t.Fatalf("verify = %d: %s\n%s", code, stderr, stdout)
	}

	// An unknown mode is a usage error, not a silent full store.
	code, _, stderr = h.run("run", "--provider", "openai", "--repeat", "1", "--store", "partial", "--yes")
	if code != ExitConfig {
		t.Fatalf("exit = %d, want %d for an unknown store mode: %s", code, ExitConfig, stderr)
	}
}
