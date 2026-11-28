package perplexity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/routeflags/ai-citation-monitor/internal/providers"
	"github.com/routeflags/ai-citation-monitor/internal/providers/httpclient"
)

const testAPIKey = "pplx-test-secret-value"

// newTestClient points the adapter at httptest. No real call is ever made: the
// key is a dummy and the base URL is the local server.
func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := httpclient.New(5*time.Second, 0, 0)
	client.BaseDelay = time.Millisecond
	return New(Options{
		APIKey:  testAPIKey,
		BaseURL: server.URL,
		HTTP:    client,
		Secrets: []string{testAPIKey},
	})
}

// serveFixture checks the request shape and serves a documented response shape.
func serveFixture(t *testing.T, status int, path string) http.Handler {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body)
	})
}

func checkRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", r.Method)
	}
	if r.URL.Path != AgentPath {
		t.Errorf("path = %s, want %s (the Agent API, not the OpenAI alias)", r.URL.Path, AgentPath)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
		t.Errorf("authorization = %q, want the configured bearer token", got)
	}
	var payload map[string]any
	if err := decode(r, &payload); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if payload["model"] != "openai/gpt-test" {
		t.Errorf("model = %v", payload["model"])
	}
	if payload["input"] != "q" {
		t.Errorf("input = %v", payload["input"])
	}
	if _, ok := payload["stream"]; ok {
		// A streamed response would be a different surface, and one call must be one
		// non-streaming JSON answer.
		t.Errorf("stream was sent (%v); the adapter reads one non-streaming response", payload["stream"])
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want the web search tool", payload["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != WebSearchToolType {
		t.Errorf("tool type = %v, want %s", tool["type"], WebSearchToolType)
	}
}

func decode(r *http.Request, target any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

func ask(t *testing.T, client *Client) providers.Result {
	t.Helper()
	result, err := client.Ask(context.Background(), providers.Request{
		QuestionKey: "Q01", QuestionText: "q", Model: "openai/gpt-test",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	return result
}

// assertTokens checks the token counts the provider reported. They stay pointers
// in the result because "no usage was sent" is not a zero: a count read as 0
// would be invented evidence.
func assertTokens(t *testing.T, usage providers.Usage, input, output, total int) {
	t.Helper()
	assertCount(t, "input tokens", usage.InputTokens, input)
	assertCount(t, "output tokens", usage.OutputTokens, output)
	assertCount(t, "total tokens", usage.TotalTokens, total)
}

// assertToolCalls checks the web search count and names where it has to come
// from: the adapter reads one documented field and never adds up the others.
func assertToolCalls(t *testing.T, got *int, want int, source string) {
	t.Helper()
	switch {
	case got == nil:
		t.Errorf("tool calls = nil, want %d (%s)", want, source)
	case *got != want:
		t.Errorf("tool calls = %d, want %d (%s)", *got, want, source)
	}
}

func assertCount(t *testing.T, name string, got *int, want int) {
	t.Helper()
	switch {
	case got == nil:
		t.Errorf("%s = nil, want %d", name, want)
	case *got != want:
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

func TestAskMergesAnnotationsWithSearchResults(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/cited.json"))
	result := ask(t, client)

	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s: %s)", result.Status, result.ErrorCode, result.ErrorMessage)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Fatalf("observability = %s, want observed", result.Observability)
	}
	// The search returned two sources and the message annotated them again (one
	// exact duplicate) plus one extra URL. Every unique URL counts once: three.
	if len(result.Citations) != 3 {
		t.Fatalf("citations = %d, want 3 (search_results plus annotations, deduplicated)", len(result.Citations))
	}
	seen := map[string]bool{}
	for _, citation := range result.Citations {
		if seen[citation.RawURL] {
			t.Errorf("the same URL was counted twice: %s", citation.RawURL)
		}
		seen[citation.RawURL] = true
	}
	if !seen["https://example.shop/glossary#top"] {
		t.Errorf("an annotation that is not in search_results is missing from the citations: %v", seen)
	}
	first := result.Citations[0]
	if first.RawURL != "https://example.shop/glossary" || first.Title != "用語集" {
		t.Errorf("first citation = %+v, want the url and the title the provider sent", first)
	}
	if first.StartIndex == nil || *first.StartIndex != 0 || first.EndIndex == nil || *first.EndIndex != 12 {
		t.Errorf("citation offsets = %v..%v, want 0..12", first.StartIndex, first.EndIndex)
	}
	if !strings.Contains(result.AnswerText, "Example Shop の用語集が説明しています") {
		t.Errorf("answer text = %q", result.AnswerText)
	}
	assertTokens(t, result.Usage, 1200, 300, 1500)
	assertToolCalls(t, result.Usage.ToolCalls, 1, "tool_calls_details.search_web.invocation")
	if result.HTTPStatus != http.StatusOK {
		t.Errorf("http status = %d", result.HTTPStatus)
	}
}

func TestAskRecordsTheAPISurface(t *testing.T) {
	client := New(Options{})
	if client.Name() != "perplexity" {
		t.Errorf("name = %q", client.Name())
	}
	if got := client.EndpointVersion(); got != "agent/v1+web_search" {
		t.Errorf("endpoint version = %q, want agent/v1+web_search", got)
	}
}

func TestAskSearchWithoutCitationsIsObservedZero(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/not-cited.json"))
	result := ask(t, client)
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s)", result.Status, result.ErrorCode)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed: the search ran but returned no sources, which is zero citations, not unknown", result.Observability)
	}
	if len(result.Citations) != 0 {
		t.Errorf("citations = %d, want 0", len(result.Citations))
	}
}

func TestAskSearchResultsAreCitationsWithoutAnnotations(t *testing.T) {
	// The message carries no annotations, but Perplexity documents each
	// search_results entry as a citation source, so the source is recorded.
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/search-results-only.json"))
	result := ask(t, client)
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s)", result.Status, result.ErrorCode)
	}
	if len(result.Citations) != 1 {
		t.Fatalf("citations = %d, want 1: a search_results entry is a citation source", len(result.Citations))
	}
	if got := result.Citations[0]; got.RawURL != "https://example.shop/guide" || got.Title != "guide" {
		t.Errorf("citation = %+v, want the search result's url and title", got)
	}
	if result.Citations[0].StartIndex != nil || result.Citations[0].EndIndex != nil {
		t.Errorf("offsets = %v..%v, want nil: a search result carries no text position", result.Citations[0].StartIndex, result.Citations[0].EndIndex)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed: the search ran", result.Observability)
	}
	// No tool_calls_details, so the visible search_results item is counted.
	assertToolCalls(t, result.Usage.ToolCalls, 1, "counted from the search_results item")
}

func TestAskAnnotationsWithoutSearchResults(t *testing.T) {
	body := `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"cited","annotations":[{"type":"url_citation","url":"https://example.shop/a","title":"a","start_index":0,"end_index":5}]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})
	result := ask(t, newTestClient(t, handler))
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s)", result.Status, result.ErrorCode)
	}
	if len(result.Citations) != 1 || result.Citations[0].RawURL != "https://example.shop/a" {
		t.Fatalf("citations = %+v, want the annotated URL", result.Citations)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed", result.Observability)
	}
}

func TestAskRecordsProviderError(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusUnauthorized, "testdata/error-auth.json"))
	result := ask(t, client)
	if result.Status != providers.StatusError {
		t.Fatalf("status = %s, want error", result.Status)
	}
	if result.ErrorKind != providers.ErrorKindProvider {
		t.Errorf("error kind = %s, want provider (401 is not a transport failure)", result.ErrorKind)
	}
	if result.ErrorCode != "invalid_api_key" {
		t.Errorf("error code = %q, want the provider's own code", result.ErrorCode)
	}
	if !strings.Contains(result.ErrorMessage, "Invalid API key") {
		t.Errorf("error message = %q", result.ErrorMessage)
	}
	if strings.Contains(result.ErrorMessage, testAPIKey) || strings.Contains(string(result.RawJSON), testAPIKey) {
		t.Fatal("the credential leaked into the record")
	}
	if result.HTTPStatus != http.StatusUnauthorized {
		t.Errorf("http status = %d", result.HTTPStatus)
	}
}

func TestAskRedactsAKeyEchoedInTheErrorBody(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"code":"permission_denied","message":"key ` + testAPIKey + ` may not use this model"}}`))
	})
	client := newTestClient(t, handler)
	result := ask(t, client)
	if result.Status != providers.StatusError || result.ErrorKind != providers.ErrorKindProvider {
		t.Fatalf("status = %s kind = %s", result.Status, result.ErrorKind)
	}
	if strings.Contains(result.ErrorMessage, testAPIKey) {
		t.Fatalf("the credential leaked into the error message: %q", result.ErrorMessage)
	}
	if !strings.Contains(result.ErrorMessage, "[redacted]") {
		t.Errorf("the message should keep the text with the key redacted: %q", result.ErrorMessage)
	}
}

func TestAskRetriesA429AndThenSucceeds(t *testing.T) {
	body, err := os.ReadFile("testdata/not-cited.json")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":{"code":"rate_limit","message":"slow down","type":"rate_limit_error"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := httpclient.New(5*time.Second, 0, 1)
	client.BaseDelay = time.Millisecond
	adapter := New(Options{APIKey: testAPIKey, BaseURL: server.URL, HTTP: client, Secrets: []string{testAPIKey}})

	result, err := adapter.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "openai/gpt-test"})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if hits.Load() != 2 {
		t.Fatalf("attempts = %d, want 2 (one 429, one success)", hits.Load())
	}
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s: %s)", result.Status, result.ErrorCode, result.ErrorMessage)
	}
}

func TestAskEmptyAnswerIsError(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/empty.json"))
	result := ask(t, client)
	if result.Status != providers.StatusError || result.ErrorCode != "empty_answer" {
		t.Fatalf("status=%s code=%s, want error/empty_answer", result.Status, result.ErrorCode)
	}
}

func TestAskWithoutCitationMetadataIsUnknown(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/no-citation-metadata.json"))
	result := ask(t, client)
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s)", result.Status, result.ErrorCode)
	}
	if result.Observability != providers.ObservabilityUnknown {
		t.Errorf("observability = %s, want unknown when the response carries no citation metadata", result.Observability)
	}
	assertToolCalls(t, result.Usage.ToolCalls, 0, "no search ran")
}

func TestAskStatusesThatMakeAnAnswerUnusable(t *testing.T) {
	cases := []struct {
		status   string
		wantCode string
	}{
		{"failed", "failed"},
		{"incomplete", "incomplete"},
		{"in_progress", "in_progress"},
		{"queued", "queued"},
		{"cancelled", "cancelled_by_provider"},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			body := `{"id":"resp_x","object":"response","status":"` + tc.status + `","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(body))
			})
			client := newTestClient(t, handler)
			result := ask(t, client)
			if result.Status != providers.StatusError {
				t.Fatalf("status = %s, want error (an unfinished response is not a measurement)", result.Status)
			}
			if result.ErrorCode != tc.wantCode {
				t.Errorf("error code = %q, want %q", result.ErrorCode, tc.wantCode)
			}
			if result.ErrorKind != providers.ErrorKindProvider {
				t.Errorf("error kind = %q", result.ErrorKind)
			}
			if !strings.Contains(result.ErrorMessage, tc.status) {
				t.Errorf("the message must name the status: %q", result.ErrorMessage)
			}
		})
	}
}

func TestAskRecordsOnlyTheNumbersTheProviderReported(t *testing.T) {
	// total_tokens is missing and the search usage uses the documented `search_web`
	// key: the total stays nil (never invented) and the count is read.
	cases := []struct {
		name         string
		usage        string
		wantToolCall *int
		wantTotal    *int
	}{
		{"search_web key", `{"input_tokens":10,"output_tokens":5,"tool_calls_details":{"search_web":{"invocation":2}}}`, intPointer(2), nil},
		{"web_search key", `{"input_tokens":10,"output_tokens":5,"total_tokens":15,"tool_calls_details":{"web_search":{"invocation":3}}}`, intPointer(3), intPointer(15)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":` + tc.usage + `}`
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(body))
			})
			client := newTestClient(t, handler)
			result := ask(t, client)
			if result.Status != providers.StatusSuccess {
				t.Fatalf("status = %s (%s)", result.Status, result.ErrorCode)
			}
			if tc.wantToolCall == nil {
				if result.Usage.ToolCalls != nil {
					t.Errorf("tool calls = %v, want nil", *result.Usage.ToolCalls)
				}
			} else if result.Usage.ToolCalls == nil || *result.Usage.ToolCalls != *tc.wantToolCall {
				t.Errorf("tool calls = %v, want %d", result.Usage.ToolCalls, *tc.wantToolCall)
			}
			if tc.wantTotal == nil {
				if result.Usage.TotalTokens != nil {
					t.Errorf("total tokens = %v, want nil when the provider reported none", *result.Usage.TotalTokens)
				}
			} else if result.Usage.TotalTokens == nil || *result.Usage.TotalTokens != *tc.wantTotal {
				t.Errorf("total tokens = %v, want %d", result.Usage.TotalTokens, *tc.wantTotal)
			}
		})
	}
}

func TestAskRecordsUnreadableAndUnknownErrors(t *testing.T) {
	t.Run("200 with a non-JSON body", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("not json at all"))
		})
		result := ask(t, newTestClient(t, handler))
		if result.Status != providers.StatusError || result.ErrorCode != "response_not_json" {
			t.Fatalf("status=%s code=%s, want error/response_not_json", result.Status, result.ErrorCode)
		}
	})
	t.Run("500 with an unknown body", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("<html>upstream is down</html>"))
		})
		result := ask(t, newTestClient(t, handler))
		if result.Status != providers.StatusError || result.ErrorCode != "http_500" {
			t.Fatalf("status=%s code=%s, want error/http_500", result.Status, result.ErrorCode)
		}
		if !strings.Contains(result.ErrorMessage, "500") {
			t.Errorf("error message should carry the status: %q", result.ErrorMessage)
		}
	})
}

func intPointer(value int) *int { return &value }
