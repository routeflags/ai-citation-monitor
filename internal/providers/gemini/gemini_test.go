package gemini

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

const testAPIKey = "gemini-test-key-value"

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := httpclient.New(5*time.Second, 0, 0)
	return New(Options{
		APIKey:  testAPIKey,
		BaseURL: server.URL,
		HTTP:    client,
		Secrets: []string{testAPIKey},
	})
}

func serveFixture(t *testing.T, status int, path string) http.Handler {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models/gemini-test:generateContent" {
			t.Errorf("path = %s, want /models/gemini-test:generateContent", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != testAPIKey {
			t.Errorf("x-goog-api-key was not forwarded")
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("request body: %v", err)
		}
		tools, ok := payload["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Errorf("tools = %v, want google_search", payload["tools"])
		} else if tool, ok := tools[0].(map[string]any); !ok {
			t.Errorf("tool = %v", tools[0])
		} else if _, ok := tool["google_search"]; !ok {
			t.Errorf("tool = %v, want google_search", tool)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write(body)
	})
}

func TestAskExtractsGroundingChunks(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/cited.json"))
	result, err := client.Ask(context.Background(), providers.Request{
		QuestionKey: "Q13", QuestionText: "HHBDとは何ですか？", Model: "gemini-test",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s (%s)", result.Status, result.ErrorMessage)
	}
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed", result.Observability)
	}
	if len(result.Citations) != 2 {
		t.Fatalf("citations = %d, want 2", len(result.Citations))
	}
	if !strings.Contains(result.Citations[0].RawURL, "thch-vape.shop") {
		t.Errorf("first citation = %q", result.Citations[0].RawURL)
	}
	if result.Usage.InputTokens == nil || *result.Usage.InputTokens != 740 {
		t.Errorf("prompt tokens were not read: %v", result.Usage.InputTokens)
	}
	if result.Usage.ToolCalls == nil || *result.Usage.ToolCalls != 2 {
		t.Errorf("tool calls = %v, want 2 search queries", result.Usage.ToolCalls)
	}
}

func TestAskWithoutGroundingMetadataIsUnknown(t *testing.T) {
	client := newTestClient(t, serveFixture(t, http.StatusOK, "testdata/not-cited.json"))
	result, _ := client.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "gemini-test"})
	if result.Status != providers.StatusSuccess {
		t.Fatalf("status = %s", result.Status)
	}
	if result.Observability != providers.ObservabilityUnknown {
		t.Errorf("observability = %s, want unknown: no groundingMetadata means the provider reported no citation metadata", result.Observability)
	}
	if len(result.Citations) != 0 {
		t.Errorf("citations = %d, want 0", len(result.Citations))
	}
}

func TestAskEmptyGroundingChunksIsObservedZero(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"an answer"}]},"groundingMetadata":{"groundingChunks":[]}}]}`))
	})
	client := newTestClient(t, handler)
	result, _ := client.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "gemini-test"})
	if result.Observability != providers.ObservabilityObserved {
		t.Errorf("observability = %s, want observed with zero citations", result.Observability)
	}
	if len(result.Citations) != 0 {
		t.Errorf("citations = %d, want 0", len(result.Citations))
	}
}

func TestAskRecordsAPIError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"code":403,"message":"API key not valid","status":"PERMISSION_DENIED"}}`))
	})
	client := newTestClient(t, handler)
	result, _ := client.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "gemini-test"})
	if result.Status != providers.StatusError {
		t.Fatalf("status = %s", result.Status)
	}
	if result.ErrorKind != providers.ErrorKindProvider {
		t.Errorf("error kind = %s", result.ErrorKind)
	}
	if result.ErrorCode != "PERMISSION_DENIED" {
		t.Errorf("error code = %q", result.ErrorCode)
	}
	if strings.Contains(result.ErrorMessage, testAPIKey) {
		t.Errorf("the credential leaked into the error message")
	}
}

func TestAskBlockedPromptIsError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`))
	})
	client := newTestClient(t, handler)
	result, _ := client.Ask(context.Background(), providers.Request{QuestionText: "q", Model: "gemini-test"})
	if result.Status != providers.StatusError || result.ErrorCode != "blocked" {
		t.Fatalf("status=%s code=%s, want error/blocked", result.Status, result.ErrorCode)
	}
}
