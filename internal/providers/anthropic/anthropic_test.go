package anthropic

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

const testAPIKey = "sk-ant-test-secret-value"

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
	if r.URL.Path != "/v1/messages" {
		t.Errorf("path = %s, want /v1/messages", r.URL.Path)
	}
	if got := r.Header.Get("x-api-key"); got != testAPIKey {
		t.Errorf("x-api-key = %q, want the configured key", got)
	}
	if got := r.Header.Get("anthropic-version"); got != APIVersion {
		t.Errorf("anthropic-version = %q, want %q", got, APIVersion)
	}
	var payload map[string]any
	if err := decode(r, &payload); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if payload["model"] != "claude-test" {
		t.Errorf("model = %v", payload["model"])
	}
	maxTokens, ok := payload["max_tokens"].(float64)
	if !ok || maxTokens <= 0 {
		t.Errorf("max_tokens = %v, want the number the API requires", payload["max_tokens"])
	}
	messages, ok := payload["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %v, want exactly one user turn", payload["messages"])
	}
	turn, _ := messages[0].(map[string]any)
	if turn["role"] != "user" {
		t.Errorf("role = %v", turn["role"])
	}
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want the web search tool", payload["tools"])
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != WebSearchToolType {
		t.Errorf("tool type = %v, want %s", tool["type"], WebSearchToolType)
	}
	if tool["name"] != "web_search" {
		t.Errorf("tool name = %v, want web_search", tool["name"])
	}
	if _, ok := tool["max_uses"].(float64); !ok {
		t.Errorf("max_uses = %v, want a number", tool["max_uses"])
	}
}

func decode(r *http.Request, target any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

func ask(t *testing.T, client *Client) providers.Result {
	t.Helper()
	result, err := client.Ask(context.Background(), providers.Request{
		QuestionKey: "Q01", QuestionText: "THCVとは何ですか？", Model: "claude-test",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	return result
}

func TestAskExtractsCitationsFromTextBlocks(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/cited.json"))
	result := ask(t, client)

	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s: %s)", result.Status, result.ErrorCode, result.ErrorMessage)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Fatalf("observability = %s, want observed", result.Observability)
	}
	// The fixture cites three URLs and the search returned three results, one of
	// which the answer never used. Only the citations count.
	if len(result.Citations) != 3 {
		t.Fatalf("citations = %d, want 3 (from citations[], not from the search results)", len(result.Citations))
	}
	for _, citation := range result.Citations {
		if strings.Contains(citation.RawURL, "third.example") {
			t.Errorf("a search result that was never cited was recorded as a citation: %s", citation.RawURL)
		}
	}
	if !strings.Contains(result.AnswerText, "Example Shop explains it") || !strings.Contains(result.AnswerText, "I'll search") {
		t.Errorf("answer text should join the text blocks: %q", result.AnswerText)
	}
	if result.Usage.InputTokens == nil || *result.Usage.InputTokens != 6039 {
		t.Errorf("input tokens = %v", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens == nil || *result.Usage.OutputTokens != 931 {
		t.Errorf("output tokens = %v", result.Usage.OutputTokens)
	}
	if result.Usage.TotalTokens != nil {
		t.Errorf("total tokens = %v, want nil: the API reports no total and the tool does not invent one", *result.Usage.TotalTokens)
	}
	if result.Usage.ToolCalls == nil || *result.Usage.ToolCalls != 1 {
		t.Errorf("tool calls = %v, want 1 (usage.server_tool_use.web_search_requests)", result.Usage.ToolCalls)
	}
	if result.HTTPStatus != http.StatusOK {
		t.Errorf("http status = %d", result.HTTPStatus)
	}
}

func TestAskRecordsTheAPISurface(t *testing.T) {
	client := New(Options{})
	if client.Name() != "anthropic" {
		t.Errorf("name = %q", client.Name())
	}
	got := client.EndpointVersion()
	for _, want := range []string{"messages/v1", WebSearchToolType, APIVersion} {
		if !strings.Contains(got, want) {
			t.Errorf("endpoint version %q does not carry %q", got, want)
		}
	}
}

func TestAskSearchWithoutCitationsIsObservedZero(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/not-cited.json"))
	result := ask(t, client)
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s", result.Status)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed: an empty citations array means zero citations, not unknown", result.Observability)
	}
	if len(result.Citations) != 0 {
		t.Errorf("citations = %d, want 0", len(result.Citations))
	}
}

func TestAskSearchErrorBlockIsStillObserved(t *testing.T) {
	// A web search error arrives inside a 200 response: content is a single error
	// object instead of a list of results.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRequest(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
		  "id": "msg_search_error",
		  "type": "message",
		  "role": "assistant",
		  "content": [
		    {"type": "server_tool_use", "id": "srvtoolu_x", "name": "web_search", "input": {"query": "q"}},
		    {"type": "web_search_tool_result", "tool_use_id": "srvtoolu_x",
		     "content": {"type": "web_search_tool_result_error", "error_code": "max_uses_exceeded"}},
		    {"type": "text", "text": "I answered without more searches.", "citations": []}
		  ],
		  "stop_reason": "end_turn",
		  "usage": {"input_tokens": 100, "output_tokens": 20, "server_tool_use": {"web_search_requests": 5}}
		}`))
	})
	client := newTestClient(t, handler)
	result := ask(t, client)
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s)", result.Status, result.ErrorCode)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed (the search block is metadata)", result.Observability)
	}
	if len(result.Citations) != 0 {
		t.Errorf("citations = %d, want 0: a search error cites nothing", len(result.Citations))
	}
	if result.Usage.ToolCalls == nil || *result.Usage.ToolCalls != 5 {
		t.Errorf("tool calls = %v, want the reported 5", result.Usage.ToolCalls)
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
	if result.ErrorCode != "authentication_error" {
		t.Errorf("error code = %q, want the provider's own type", result.ErrorCode)
	}
	if !strings.Contains(result.ErrorMessage, "invalid x-api-key") {
		t.Errorf("error message = %q", result.ErrorMessage)
	}
	if strings.Contains(result.ErrorMessage, testAPIKey) || strings.Contains(string(result.RawJSON), testAPIKey) {
		t.Fatal("the credential leaked into the record")
	}
	if result.HTTPStatus != http.StatusUnauthorized {
		t.Errorf("http status = %d", result.HTTPStatus)
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
			w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
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

	result, err := adapter.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "claude-test"})
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
	if result.Usage.ToolCalls == nil || *result.Usage.ToolCalls != 0 {
		t.Errorf("tool calls = %v, want 0 (no search ran)", result.Usage.ToolCalls)
	}
}

func TestAskStopReasonsThatMakeAnAnswerUnusable(t *testing.T) {
	cases := []struct {
		stopReason string
		wantCode   string
	}{
		{"max_tokens", "max_tokens"},
		{"refusal", "refusal"},
		{"pause_turn", "pause_turn"},
		{"tool_use", "unexpected_tool_use"},
	}
	for _, tc := range cases {
		t.Run(tc.stopReason, func(t *testing.T) {
			body := `{"id":"msg_x","type":"message","role":"assistant","content":[{"type":"text","text":"partial"}],"stop_reason":"` + tc.stopReason + `","usage":{"input_tokens":1,"output_tokens":1}}`
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(body))
			})
			client := newTestClient(t, handler)
			result := ask(t, client)
			if result.Status != providers.StatusError {
				t.Fatalf("status = %s, want error (a cut-off or declined answer is not a measurement)", result.Status)
			}
			if result.ErrorCode != tc.wantCode {
				t.Errorf("error code = %q, want %q", result.ErrorCode, tc.wantCode)
			}
			if result.ErrorKind != providers.ErrorKindProvider {
				t.Errorf("error kind = %q", result.ErrorKind)
			}
			if !strings.Contains(result.ErrorMessage, tc.stopReason) {
				t.Errorf("the message must name the stop reason: %q", result.ErrorMessage)
			}
		})
	}
}

func TestAskHonoursTheConfiguredNumbers(t *testing.T) {
	var seen map[string]any
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := decode(r, &seen); err != nil {
			t.Fatalf("body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := httpclient.New(5*time.Second, 0, 0)
	adapter := New(Options{APIKey: testAPIKey, BaseURL: server.URL, HTTP: client, MaxTokens: 512, MaxUses: 2})
	if _, err := adapter.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if seen["max_tokens"] != float64(512) {
		t.Errorf("max_tokens = %v, want the configured 512", seen["max_tokens"])
	}
	tools, _ := seen["tools"].([]any)
	tool, _ := tools[0].(map[string]any)
	if tool["max_uses"] != float64(2) {
		t.Errorf("max_uses = %v, want the configured 2", tool["max_uses"])
	}
}
