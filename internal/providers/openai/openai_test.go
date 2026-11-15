package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/routeflags/ai-citation-monitor/internal/providers"
	"github.com/routeflags/ai-citation-monitor/internal/providers/httpclient"
)

const testAPIKey = "sk-test-openai-secret-value"

func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
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
	}), server
}

func serveFixture(t *testing.T, status int, path string) http.Handler {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/responses" {
			t.Errorf("path = %s, want /responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testAPIKey {
			t.Errorf("authorization header was not forwarded")
		}
		var payload map[string]any
		if err := decodeJSON(r, &payload); err != nil {
			t.Errorf("request body: %v", err)
		}
		if payload["model"] != "gpt-test" {
			t.Errorf("model = %v", payload["model"])
		}
		tools, ok := payload["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Errorf("tools = %v, want the web_search tool", payload["tools"])
		} else if tool, ok := tools[0].(map[string]any); !ok || tool["type"] != "web_search" {
			t.Errorf("tool = %v, want type web_search", tools[0])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body)
	})
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

func TestAskExtractsCitationsFromAnnotations(t *testing.T) {
	client, _ := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/cited.json"))
	result, err := client.Ask(context.Background(), providers.Request{
		QuestionKey: "Q01", QuestionText: "THCVとTHCの違いは？", Model: "gpt-test",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s: %s)", result.Status, result.ErrorCode, result.ErrorMessage)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Fatalf("observability = %s, want observed (annotations were present)", result.Observability)
	}
	// The fixture has four annotations: two distinct pages, one query/fragment
	// duplicate and one mailto. Folding happens in the normaliser, not here.
	if len(result.Citations) != 4 {
		t.Fatalf("citations = %d, want 4 (the adapter must not drop anything)", len(result.Citations))
	}
	if !strings.Contains(result.AnswerText, "THCV") {
		t.Errorf("answer text was not extracted: %q", result.AnswerText)
	}
	if result.Usage.InputTokens == nil || *result.Usage.InputTokens == 0 {
		t.Errorf("input tokens were not read from usage")
	}
	if result.Usage.ToolCalls == nil || *result.Usage.ToolCalls != 1 {
		t.Errorf("tool calls = %v, want 1 (one web_search_call item)", result.Usage.ToolCalls)
	}
}

func TestAskWithoutAnnotationsIsObservedZero(t *testing.T) {
	client, _ := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/not-cited.json"))
	result, err := client.Ask(context.Background(), providers.Request{
		QuestionKey: "Q01", QuestionText: "q", Model: "gpt-test",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s", result.Status)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed: an empty annotations array means zero citations, not unknown", result.Observability)
	}
	if len(result.Citations) != 0 {
		t.Errorf("citations = %d, want 0", len(result.Citations))
	}
}

func TestAskRecordsProviderError(t *testing.T) {
	client, _ := newTestClient(t, serveFixture(t, http.StatusTooManyRequests, "testdata/error.json"))
	result, _ := client.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "gpt-test"})
	if result.Status != providers.StatusError {
		t.Fatalf("status = %s, want error", result.Status)
	}
	if result.ErrorKind != providers.ErrorKindProvider {
		t.Errorf("error kind = %s", result.ErrorKind)
	}
	if result.ErrorCode != "insufficient_quota" {
		t.Errorf("error code = %q, want insufficient_quota", result.ErrorCode)
	}
	if strings.Contains(result.ErrorMessage, testAPIKey) || strings.Contains(string(result.RawJSON), testAPIKey) {
		t.Fatalf("the credential leaked into the record")
	}
}

func TestAskEmptyAnswerIsError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"x","status":"completed","output":[]}`))
	})
	client, _ := newTestClient(t, handler)
	result, _ := client.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "gpt-test"})
	if result.Status != providers.StatusError || result.ErrorCode != "empty_answer" {
		t.Fatalf("status=%s code=%s, want error/empty_answer (an empty answer is a failed fetch)", result.Status, result.ErrorCode)
	}
}

func TestAskMissingMessageIsUnknownObservability(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"x","status":"completed","output_text":"something"}`))
	})
	client, _ := newTestClient(t, handler)
	result, _ := client.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "gpt-test"})
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s", result.Status)
	}
	if result.Observability != providers.ObservabilityUnknown {
		t.Errorf("observability = %s, want unknown when the response has no citation metadata", result.Observability)
	}
}
