package grok

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

const testAPIKey = "xai-test-secret-value"

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

// serveFixture checks the request shape and serves a captured response shape.
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
	if r.URL.Path != "/responses" {
		t.Errorf("path = %s, want /responses", r.URL.Path)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
		t.Errorf("authorization = %q, want a Bearer header with the configured key", got)
	}
	var payload map[string]any
	if err := decode(r, &payload); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if payload["model"] != "grok-test" {
		t.Errorf("model = %v", payload["model"])
	}
	input, ok := payload["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %v, want exactly one user turn", payload["input"])
	}
	turn, _ := input[0].(map[string]any)
	if turn["role"] != "user" {
		t.Errorf("role = %v", turn["role"])
	}
	if _, ok := turn["content"].(string); !ok {
		t.Errorf("content = %v, want the question text", turn["content"])
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want the web search tool", payload["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != WebSearchToolType {
		t.Errorf("tool type = %v, want %s", tool["type"], WebSearchToolType)
	}
	// Exactly one key: a domain filter or an image option here would change what
	// a measurement means, so the request stays at the plain tool.
	if len(tool) != 1 {
		t.Errorf("tool = %v, want only the type key", tool)
	}
	// Inline citations are on by default and the annotations only mean "the
	// answer cited this" while they are. Asking for no_inline_citations would
	// change what a citation is, so the request must not send `include`.
	if _, ok := payload["include"]; ok {
		t.Errorf("include = %v, want the request to leave inline citations at the default", payload["include"])
	}
	if _, ok := payload["tool_choice"]; ok {
		t.Errorf("tool_choice = %v, want the model to decide whether it searches", payload["tool_choice"])
	}
}

func decode(r *http.Request, target any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

func ask(t *testing.T, client *Client) providers.Result {
	t.Helper()
	result, err := client.Ask(context.Background(), providers.Request{
		QuestionKey: "Q01", QuestionText: "THCVとは何ですか？", Model: "grok-test",
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

func TestAskTakesCitationsFromAnnotations(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/cited.json"))
	result := ask(t, client)

	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s: %s)", result.Status, result.ErrorCode, result.ErrorMessage)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Fatalf("observability = %s, want observed", result.Observability)
	}
	if len(result.Citations) != 3 {
		t.Fatalf("citations = %d, want 3 (from the annotations)", len(result.Citations))
	}
	first := result.Citations[0]
	if first.RawURL != "https://example.shop/guide" {
		t.Errorf("first citation url = %q", first.RawURL)
	}
	if first.Title != "1" {
		t.Errorf("title = %q, want the provider's citation label", first.Title)
	}
	if first.StartIndex == nil || *first.StartIndex != 29 {
		t.Errorf("start index = %v, want the annotation position", first.StartIndex)
	}
	if first.EndIndex == nil || *first.EndIndex != 70 {
		t.Errorf("end index = %v, want the annotation position", first.EndIndex)
	}
	if !strings.Contains(result.AnswerText, "Example Shop explains THCV") {
		t.Errorf("answer text = %q", result.AnswerText)
	}
	assertTokens(t, result.Usage, 1204, 268, 1472)
	assertToolCalls(t, result.Usage.ToolCalls, 1, "server_side_tool_usage_details.web_search_calls")
	if result.HTTPStatus != http.StatusOK {
		t.Errorf("http status = %d", result.HTTPStatus)
	}
	if result.StartedAt.IsZero() || result.CompletedAt.IsZero() {
		t.Error("the adapter must stamp the call with the injected clock")
	}
}

func TestAskRecordsTheAPISurface(t *testing.T) {
	client := New(Options{})
	if client.Name() != "grok" {
		t.Errorf("name = %q", client.Name())
	}
	got := client.EndpointVersion()
	for _, want := range []string{"responses/v1", WebSearchToolType, "xai"} {
		if !strings.Contains(got, want) {
			t.Errorf("endpoint version %q does not carry %q", got, want)
		}
	}
	// The OpenAI surface is a different provider on the same API family: the
	// version strings must not collide, or the records would mix.
	if got == "responses/v1+web_search" {
		t.Errorf("endpoint version %q collides with the OpenAI one", got)
	}
}

func TestAskSearchWithoutCitationsIsObservedZero(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/not-cited.json"))
	result := ask(t, client)
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s)", result.Status, result.ErrorCode)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed: an empty annotations array means zero citations, not unknown", result.Observability)
	}
	if len(result.Citations) != 0 {
		t.Errorf("citations = %d, want 0", len(result.Citations))
	}
	assertToolCalls(t, result.Usage.ToolCalls, 1, "server_side_tool_usage_details.web_search_calls")
}

func TestAskDoesNotCountTheTopLevelCitationsArray(t *testing.T) {
	// The response lists the URLs the search encountered and the answer cites
	// none of them: the top-level array is a candidate list, not an attribution.
	// The Responses schema does not declare the field, so this fixture is a guard
	// as much as an example: if it ever appears, it must not become a citation.
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/candidates-only.json"))
	result := ask(t, client)
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s: %s)", result.Status, result.ErrorCode, result.ErrorMessage)
	}
	if len(result.Citations) != 0 {
		t.Fatalf("citations = %d, want 0: a URL the search saw is not a citation", len(result.Citations))
	}
	for _, citation := range result.Citations {
		if strings.Contains(citation.RawURL, "example.shop") || strings.Contains(citation.RawURL, "rival.example") {
			t.Errorf("a search candidate was recorded as a citation: %s", citation.RawURL)
		}
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed (the search item is metadata)", result.Observability)
	}
}

func TestAskUnpositionedAnnotationsAreObservedZero(t *testing.T) {
	// A live grok-4.7 answer (2026-11-28) returned 96 url_citation annotations
	// whose spans were all 0..0, with no inline markers in the text. They are the
	// sources the search saw, not citations the answer made.
	body := `{"status":"completed","output":[{"type":"web_search_call","id":"ws_1","status":"completed"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer without markers","annotations":[{"type":"url_citation","url":"https://example.com/a","title":"https://example.com/a","start_index":0,"end_index":0},{"type":"url_citation","url":"https://example.com/b","title":"https://example.com/b","start_index":0,"end_index":0}]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})
	result := ask(t, newTestClient(t, handler))
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s)", result.Status, result.ErrorCode)
	}
	if len(result.Citations) != 0 {
		t.Fatalf("citations = %d, want 0: a 0..0 span has no position in the answer", len(result.Citations))
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed", result.Observability)
	}
}

func TestAskRecordsProviderError(t *testing.T) {
	// Retrying an authentication failure only burns quota: the shared client
	// retries 429/5xx and transport errors, and the adapter must not add its own.
	body, err := os.ReadFile("testdata/error-auth.json")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRequest(t, r)
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write(body)
	})
	client := newTestClient(t, handler)
	result := ask(t, client)
	if hits.Load() != 1 {
		t.Errorf("attempts = %d, want 1: a 401 is not retried", hits.Load())
	}
	if result.Status != providers.StatusError {
		t.Fatalf("status = %s, want error", result.Status)
	}
	if result.ErrorKind != providers.ErrorKindProvider {
		t.Errorf("error kind = %s, want provider (401 is not a transport failure)", result.ErrorKind)
	}
	if result.ErrorCode != "invalid_api_key" {
		t.Errorf("error code = %q, want the provider's own code", result.ErrorCode)
	}
	if !strings.Contains(result.ErrorMessage, "Incorrect API key provided") {
		t.Errorf("error message = %q", result.ErrorMessage)
	}
	if strings.Contains(result.ErrorMessage, testAPIKey) || strings.Contains(string(result.RawJSON), testAPIKey) {
		t.Fatal("the credential leaked into the record")
	}
	if result.HTTPStatus != http.StatusUnauthorized {
		t.Errorf("http status = %d", result.HTTPStatus)
	}
}

func TestAskRecordsTheFlatErrorEnvelope(t *testing.T) {
	// xAI documents no error-body schema. The flat shape ({"code": ...,
	// "error": "message"}) must survive as well as the nested one, and the key
	// echoed inside the message is redacted.
	client := newTestClient(t, serveFixture(t, http.StatusForbidden, "testdata/error-auth-flat.json"))
	result := ask(t, client)
	if result.Status != providers.StatusError || result.ErrorKind != providers.ErrorKindProvider {
		t.Fatalf("status = %s kind = %s, want a provider error", result.Status, result.ErrorKind)
	}
	if result.ErrorCode != "invalid_api_key" {
		t.Errorf("error code = %q, want the top-level code", result.ErrorCode)
	}
	if !strings.Contains(result.ErrorMessage, "Incorrect API key provided") {
		t.Errorf("error message = %q, want the provider's message", result.ErrorMessage)
	}
	if strings.Contains(result.ErrorMessage, testAPIKey) {
		t.Errorf("the credential leaked into the message: %q", result.ErrorMessage)
	}
	if !strings.Contains(result.ErrorMessage, "[redacted]") {
		t.Errorf("error message = %q, want the credential redacted", result.ErrorMessage)
	}
	if strings.Contains(string(result.RawJSON), testAPIKey) {
		t.Error("the credential leaked into the stored raw JSON")
	}
	if result.HTTPStatus != http.StatusForbidden {
		t.Errorf("http status = %d", result.HTTPStatus)
	}
}

func TestAskUnknownErrorBodyFallsBackToTheStatus(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRequest(t, r)
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("<html><body>bad gateway</body></html>"))
	})
	client := newTestClient(t, handler)
	result := ask(t, client)
	if result.Status != providers.StatusError || result.ErrorKind != providers.ErrorKindProvider {
		t.Fatalf("status = %s kind = %s", result.Status, result.ErrorKind)
	}
	if result.ErrorCode != "http_502" {
		t.Errorf("error code = %q, want http_502", result.ErrorCode)
	}
	if !strings.Contains(result.ErrorMessage, "bad gateway") {
		t.Errorf("error message = %q, want the start of the body", result.ErrorMessage)
	}
}

func TestAskRetriesA429AndThenSucceeds(t *testing.T) {
	// 429 and 5xx are the shared retry policy's job: the adapter must not swallow
	// them or retry on its own.
	body, err := os.ReadFile("testdata/not-cited.json")
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"code":"rate_limit_exceeded","error":"slow down"}`))
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

	result, err := adapter.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "grok-test"})
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
	if result.Observability != providers.ObservabilityUnknown {
		t.Errorf("observability = %s, want unknown when there is no output to read", result.Observability)
	}
}

func TestAskStatusesThatMakeAnAnswerUnusable(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantCode   string
		wantInText string
	}{
		{
			name:       "incomplete with a reason",
			body:       `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`,
			wantCode:   "max_output_tokens",
			wantInText: "max_output_tokens",
		},
		{
			name:       "incomplete without a reason",
			body:       `{"status":"incomplete","output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`,
			wantCode:   "incomplete",
			wantInText: "incomplete",
		},
		{
			name:       "still in progress",
			body:       `{"status":"in_progress","output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`,
			wantCode:   "in_progress",
			wantInText: "in_progress",
		},
		{
			name:       "an unknown status",
			body:       `{"status":"failed","output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`,
			wantCode:   "failed",
			wantInText: "failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				checkRequest(t, r)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tc.body))
			})
			result := ask(t, newTestClient(t, handler))
			if result.Status != providers.StatusError {
				t.Fatalf("status = %s, want error: an unfinished answer is not a measurement", result.Status)
			}
			if result.ErrorCode != tc.wantCode {
				t.Errorf("error code = %q, want %q", result.ErrorCode, tc.wantCode)
			}
			if result.ErrorKind != providers.ErrorKindProvider {
				t.Errorf("error kind = %q", result.ErrorKind)
			}
			if !strings.Contains(result.ErrorMessage, tc.wantInText) {
				t.Errorf("the message must name the status: %q", result.ErrorMessage)
			}
		})
	}
}

func TestAskRefusalIsAnError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"I can't help with that."}]}]}`))
	})
	result := ask(t, newTestClient(t, handler))
	if result.Status != providers.StatusError || result.ErrorCode != "refusal" {
		t.Fatalf("status=%s code=%s, want error/refusal: a refusal is not a zero-citation answer", result.Status, result.ErrorCode)
	}
	if !strings.Contains(result.ErrorMessage, "declined") {
		t.Errorf("error message = %q", result.ErrorMessage)
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
	// The fixture reports no total_tokens: the adapter records what the provider
	// sent and never adds one up.
	if result.Usage.TotalTokens != nil {
		t.Errorf("total tokens = %d, want nil when the provider does not report it", *result.Usage.TotalTokens)
	}
}

func TestAskCountsVisibleSearchItemsWhenUsageOmitsThem(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"completed","output":[
			{"type":"web_search_call","id":"ws_1","status":"completed"},
			{"type":"web_search_call","id":"ws_2","status":"completed"},
			{"type":"message","content":[{"type":"output_text","text":"ok","annotations":[]}]}
		],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}`))
	})
	result := ask(t, newTestClient(t, handler))
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s)", result.Status, result.ErrorCode)
	}
	assertToolCalls(t, result.Usage.ToolCalls, 2, "the two visible web_search_call items")
}

func TestAskUsesTheWebSearchCountNotTheToolTotal(t *testing.T) {
	// num_server_side_tools_used counts every server-side tool and num_sources_used
	// counts sources the search saw: neither is the number of web searches, and
	// neither is a citation count.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"completed","output":[
			{"type":"message","content":[{"type":"output_text","text":"ok","annotations":[]}]}
		],"usage":{
			"input_tokens":10,"output_tokens":2,"total_tokens":12,
			"num_sources_used":7,"num_server_side_tools_used":3,
			"server_side_tool_usage_details":{"web_search_calls":1}
		},"citations":["https://example.shop/a","https://rival.example/b"]}`))
	})
	result := ask(t, newTestClient(t, handler))
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s)", result.Status, result.ErrorCode)
	}
	assertToolCalls(t, result.Usage.ToolCalls, 1, "web_search_calls, not the server-side tool total")
	if len(result.Citations) != 0 {
		t.Errorf("citations = %d, want 0: a source the search saw is not a citation", len(result.Citations))
	}
}

func TestAskNonJSONBodyIsRecordedAsSuch(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRequest(t, r)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>proxy error</body></html>"))
	})
	result := ask(t, newTestClient(t, handler))
	if result.Status != providers.StatusError || result.ErrorCode != "response_not_json" {
		t.Fatalf("status=%s code=%s", result.Status, result.ErrorCode)
	}
	if !strings.Contains(result.ErrorMessage, "HTTP 200") {
		t.Errorf("error message = %q, want the status", result.ErrorMessage)
	}
}

func TestAskUsesTheInjectedClock(t *testing.T) {
	pinned := time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)
	server := httptest.NewServer(serveFixture(t, http.StatusOK, "testdata/cited.json"))
	t.Cleanup(server.Close)
	adapter := New(Options{
		APIKey: testAPIKey, BaseURL: server.URL,
		HTTP: httpclient.New(5*time.Second, 0, 0), Now: func() time.Time { return pinned },
	})
	result, err := adapter.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "grok-test"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.StartedAt.Equal(pinned) || !result.CompletedAt.Equal(pinned) {
		t.Errorf("started=%s completed=%s, want the injected clock", result.StartedAt, result.CompletedAt)
	}
}
