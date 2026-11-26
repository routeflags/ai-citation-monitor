// Package perplexity implements the Perplexity adapter for the Agent API with the
// server-side web_search tool.
//
// API surface used (checked against the published documentation at implementation
// time, not guessed, 2026-11-26):
//
//	POST {base}/agent
//	Authorization: Bearer <key>
//	{"model": ..., "input": ..., "tools": [{"type": "web_search"}]}
//
// The response is the Responses-shaped object documented in the API reference:
// `output[]` carries a `search_results` item (the sources the search returned) and
// a `message` item (content parts of type `output_text`).
//
// Perplexity documents the `search_results` entries' `id` and `url` as the source
// of truth for citations, whether or not the model inserted `[N]` markers in the
// text. They are therefore citations, together with any `url_citation` annotations
// the message carries; the same URL is counted once. The answer text is never
// parsed: a URL that merely appears inside it is not a citation.
//
// The Agent API is multi-provider: `model` may name `openai/...` or
// `anthropic/...`. The `provider` column therefore means "the Perplexity API
// surface", not "the model vendor".
package perplexity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/routeflags/ai-citation-monitor/internal/providers"
	"github.com/routeflags/ai-citation-monitor/internal/providers/httpclient"
)

// Values that describe the API surface.
const (
	// WebSearchToolType is the server-side search tool this adapter sends.
	WebSearchToolType = "web_search"
	// AgentPath is appended to the configured base URL.
	AgentPath = "/agent"
	// EndpointVersion is the surface this adapter speaks. A different surface
	// (another path, another tool) would produce a different string, so old and new
	// measurements never mix.
	EndpointVersion = "agent/v1+" + WebSearchToolType
)

// Client talks to the Perplexity Agent API.
type Client struct {
	apiKey  string
	baseURL string
	http    *httpclient.Client
	secrets []string
	now     func() time.Time
}

// Options configures the adapter.
type Options struct {
	APIKey  string
	BaseURL string
	HTTP    *httpclient.Client
	Secrets []string
	Now     func() time.Time
}

// New builds the adapter.
func New(opts Options) *Client {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		apiKey:  opts.APIKey,
		baseURL: strings.TrimRight(opts.BaseURL, "/"),
		http:    opts.HTTP,
		secrets: opts.Secrets,
		now:     now,
	}
}

// Name is the provider id.
func (c *Client) Name() string { return "perplexity" }

// EndpointVersion records which API surface produced the run.
func (c *Client) EndpointVersion() string { return EndpointVersion }

// Ask performs exactly one round trip.
func (c *Client) Ask(ctx context.Context, req providers.Request) (providers.Result, error) {
	result := providers.Result{Status: providers.StatusSuccess, StartedAt: c.now().UTC()}
	finish := func() providers.Result {
		result.CompletedAt = c.now().UTC()
		result.ErrorMessage = providers.Sanitize(result.ErrorMessage, c.secrets)
		result.RawJSON = providers.SanitizeBytes(result.RawJSON, c.secrets)
		return result
	}

	// `stream` is left out: the default is a single non-streaming JSON response, and
	// `background` is not used. One call is one run.
	body, err := json.Marshal(map[string]any{
		"model": req.Model,
		"input": req.QuestionText,
		"tools": []map[string]any{{"type": WebSearchToolType}},
	})
	if err != nil {
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "request_encode"
		result.ErrorMessage = err.Error()
		return finish(), nil
	}

	resp, err := c.http.Do(ctx, httpclient.Request{
		Method: http.MethodPost,
		URL:    c.baseURL + AgentPath,
		Headers: map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + c.apiKey,
		},
		Body: body,
	})
	if err != nil {
		// Mutate first: finish() returns a copy, so ordering matters here.
		c.mapTransportError(ctx, &result, err)
		return finish(), nil
	}
	result.HTTPStatus = resp.Status
	result.RawJSON = resp.Body

	// A failed call is decoded through its error envelope first, and only that
	// ({"error":{"code":...,"message":...,"type":...}}). Reading it as a success
	// body is how a 401 turns into "the response is not JSON" and loses the
	// provider's own reason.
	if resp.Status >= 400 {
		var envelope errorEnvelope
		if err := json.Unmarshal(resp.Body, &envelope); err == nil && envelope.Error != nil {
			result.Status = providers.StatusError
			result.ErrorKind = providers.ErrorKindProvider
			result.ErrorCode = envelope.Error.errorCode(resp.Status)
			result.ErrorMessage = envelope.Error.errorMessage()
			if result.ErrorMessage == "" {
				result.ErrorMessage = fmt.Sprintf("HTTP %d: %s", resp.Status, truncate(string(resp.Body)))
			}
			return finish(), nil
		}
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = fmt.Sprintf("http_%d", resp.Status)
		result.ErrorMessage = fmt.Sprintf("HTTP %d: %s", resp.Status, truncate(string(resp.Body)))
		return finish(), nil
	}

	var payload responsePayload
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "response_not_json"
		result.ErrorMessage = fmt.Sprintf("HTTP %d: response is not JSON: %s", resp.Status, truncate(string(resp.Body)))
		return finish(), nil
	}

	if payload.Error != nil {
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = payload.Error.errorCode(resp.Status)
		result.ErrorMessage = payload.Error.errorMessage()
		if result.ErrorMessage == "" {
			result.ErrorMessage = fmt.Sprintf("HTTP %d: %s", resp.Status, truncate(string(resp.Body)))
		}
		return finish(), nil
	}

	result.AnswerText = payload.answerText()
	result.Citations, result.Observability = payload.citations()
	result.Usage = payload.usage()

	if code := payload.statusError(); code != "" {
		// A failed, cut-off, queued or cancelled response is not a zero-citation
		// answer: it is recorded as a failure and stays out of every denominator.
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = code
		result.ErrorMessage = payload.statusMessage()
		return finish(), nil
	}

	if strings.TrimSpace(result.AnswerText) == "" && len(result.Citations) == 0 {
		// An empty answer is a failed fetch, not "no citation".
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "empty_answer"
		result.ErrorMessage = "empty answer: no text and no citations (treated as a failed fetch)"
	}
	return finish(), nil
}

func (c *Client) mapTransportError(ctx context.Context, result *providers.Result, err error) {
	if ctx.Err() != nil {
		result.Status = providers.StatusCancelled
		result.ErrorKind = providers.ErrorKindCancelled
		result.ErrorCode = "cancelled"
		result.ErrorMessage = ctx.Err().Error()
		return
	}
	result.Status = providers.StatusError
	result.ErrorKind = providers.ErrorKindTransport
	result.ErrorCode = "transport_error"
	result.ErrorMessage = err.Error()
}

type responsePayload struct {
	ID     string        `json:"id"`
	Object string        `json:"object"`
	Status string        `json:"status"`
	Model  string        `json:"model"`
	Output []outputItem  `json:"output"`
	Usage  *usagePayload `json:"usage"`
	Error  *errorPayload `json:"error"`
}

// errorEnvelope is the only shape an error response is decoded into.
type errorEnvelope struct {
	Error *errorPayload `json:"error"`
}

type errorPayload struct {
	Code    any    `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// outputItem is one entry of `output[]`: a `message`, a `search_results` item, or
// a tool item this adapter ignores.
type outputItem struct {
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Content []contentPart  `json:"content"`
	Results []searchResult `json:"results"`
}

// searchResult is one source the search returned. Perplexity documents this entry
// as a citation source, so its URL and title are recorded.
type searchResult struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// contentPart is one part of a message. Annotations is a pointer so "the provider
// sent no annotations key" (unknown) stays different from "annotations: []"
// (observed, zero citations).
type contentPart struct {
	Type        string            `json:"type"`
	Text        string            `json:"text"`
	Annotations *[]annotationItem `json:"annotations"`
}

type annotationItem struct {
	Type       string `json:"type"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	StartIndex *int   `json:"start_index"`
	EndIndex   *int   `json:"end_index"`
}

// usagePayload carries only the numbers the adapter records. The provider's own
// `cost` fields are left out: this tool does not price a call (D11).
type usagePayload struct {
	InputTokens      *int                       `json:"input_tokens"`
	OutputTokens     *int                       `json:"output_tokens"`
	TotalTokens      *int                       `json:"total_tokens"`
	ToolCallsDetails map[string]*toolCallDetail `json:"tool_calls_details"`
}

type toolCallDetail struct {
	Invocation *int `json:"invocation"`
}

func (p *responsePayload) answerText() string {
	parts := make([]string, 0, 2)
	for _, item := range p.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type != "output_text" || strings.TrimSpace(part.Text) == "" {
				continue
			}
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// citations returns the cited URLs and whether the response carried citation
// metadata at all. Perplexity documents the `search_results` entries as the source
// of truth for citations, so each one is a citation even when the message carries
// no inline markers; a `url_citation` annotation is a citation too. The same URL
// from both places is counted once, and the annotation's text offsets win because
// they are the more precise record. An empty `results` array or `annotations`
// array is "observed, zero citations"; a response with neither field is "unknown".
func (p *responsePayload) citations() ([]providers.Citation, string) {
	annotations := make([]providers.Citation, 0, 4)
	results := make([]providers.Citation, 0, 4)
	sawMetadata := false
	sawMessage := false
	for _, item := range p.Output {
		switch item.Type {
		case "message":
			sawMessage = true
		case "search_results":
			// The search ran, so the provider reported citation metadata even when it
			// returned no usable entries.
			sawMetadata = true
			for _, result := range item.Results {
				results = append(results, providers.Citation{RawURL: result.URL, Title: result.Title})
			}
		}
		for _, part := range item.Content {
			if part.Annotations == nil {
				continue
			}
			sawMetadata = true
			for _, annotation := range *part.Annotations {
				if annotation.Type != "url_citation" {
					continue
				}
				annotations = append(annotations, providers.Citation{
					RawURL:     annotation.URL,
					Title:      annotation.Title,
					StartIndex: annotation.StartIndex,
					EndIndex:   annotation.EndIndex,
				})
			}
		}
	}
	citations := make([]providers.Citation, 0, len(annotations)+len(results))
	seen := make(map[string]bool, len(annotations)+len(results))
	add := func(citation providers.Citation) {
		if citation.RawURL == "" || seen[citation.RawURL] {
			return
		}
		seen[citation.RawURL] = true
		citations = append(citations, citation)
	}
	for _, citation := range annotations {
		add(citation)
	}
	for _, citation := range results {
		add(citation)
	}
	if !sawMessage {
		return citations, providers.ObservabilityUnknown
	}
	return citations, providers.HasCitationReports(sawMetadata)
}

func (p *responsePayload) usage() providers.Usage {
	usage := providers.Usage{}
	reported := (*int)(nil)
	if p.Usage != nil {
		usage.InputTokens = p.Usage.InputTokens
		usage.OutputTokens = p.Usage.OutputTokens
		usage.TotalTokens = p.Usage.TotalTokens
		// The documented key for the search tool is `search_web`; `web_search` is
		// accepted too so a rename does not silently drop the number.
		for _, key := range []string{"search_web", "web_search"} {
			if details := p.Usage.ToolCallsDetails[key]; details != nil {
				reported = details.Invocation
				break
			}
		}
	}
	// The searches are visible as `search_results` items, so they are counted even
	// when the usage block is missing (tokens stay nil, never zero).
	if reported == nil {
		count := 0
		for _, item := range p.Output {
			if item.Type == "search_results" {
				count++
			}
		}
		reported = &count
	}
	usage.ToolCalls = reported
	return usage
}

// statusError maps the statuses that make an answer unusable to an error code.
// Anything but `completed` is a failure: a cut-off, queued or cancelled response
// is not a zero-citation answer.
func (p *responsePayload) statusError() string {
	switch p.Status {
	case "", "completed":
		return ""
	case "failed":
		if p.Error != nil {
			if code := p.Error.errorCode(0); code != "http_0" {
				return code
			}
		}
		return "failed"
	case "incomplete":
		return "incomplete"
	case "in_progress":
		return "in_progress"
	case "queued":
		return "queued"
	case "cancelled":
		// The provider cancelled the response, which is not this tool's Ctrl+C
		// cancellation (ErrorKindCancelled): the run still failed at the provider.
		return "cancelled_by_provider"
	default:
		return "status_" + p.Status
	}
}

func (p *responsePayload) statusMessage() string {
	if p.Error != nil {
		if message := p.Error.errorMessage(); message != "" {
			return fmt.Sprintf("status=%s: %s", p.Status, message)
		}
	}
	switch p.Status {
	case "incomplete":
		return "status=incomplete: the provider stopped before the answer was complete, so the citation set is incomplete (a cut-off answer is not a measurement)"
	case "in_progress":
		return "status=in_progress: the response is still running and this tool sends one request per call, so nothing complete was measured"
	case "queued":
		return "status=queued: the response was queued and this tool does not poll, so nothing was measured"
	case "cancelled":
		return "status=cancelled: the provider cancelled the response"
	default:
		return "status=" + p.Status
	}
}

func (p *errorPayload) errorCode(status int) string {
	if p == nil {
		return fmt.Sprintf("http_%d", status)
	}
	switch code := p.Code.(type) {
	case string:
		if code != "" {
			return code
		}
	case float64:
		return fmt.Sprintf("%d", int(code))
	}
	if p.Type != "" {
		return p.Type
	}
	return fmt.Sprintf("http_%d", status)
}

func (p *errorPayload) errorMessage() string {
	if p == nil {
		return ""
	}
	message := p.Message
	if message == "" {
		message = p.Type
	}
	if p.Code != nil {
		if code, ok := p.Code.(string); ok && code != "" && !strings.Contains(message, code) {
			message = fmt.Sprintf("%s (code=%s)", message, code)
		}
	}
	return strings.TrimSpace(message)
}

func truncate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 300 {
		return value[:300] + "..."
	}
	return value
}
