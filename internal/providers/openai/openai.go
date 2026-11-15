// Package openai implements the OpenAI adapter for the Responses API with the
// web search tool.
//
// API surface used (checked against the published documentation at
// implementation time, not guessed):
//
//	POST {base}/responses
//	{"model": ..., "input": ..., "tools": [{"type": "web_search"}]}
//
// Citations come only from the message content annotations of type
// `url_citation` (url / title / start_index / end_index). A URL that merely
// appears inside the answer text is not a citation. `output_text` is the
// convenience join and carries no links, so annotations are the source of truth.
package openai

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

// Client talks to the OpenAI Responses API.
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
func (c *Client) Name() string { return "openai" }

// EndpointVersion records which API surface produced the run.
func (c *Client) EndpointVersion() string { return "responses/v1+web_search" }

// Ask performs exactly one round trip.
func (c *Client) Ask(ctx context.Context, req providers.Request) (providers.Result, error) {
	result := providers.Result{Status: providers.StatusSuccess, StartedAt: c.now().UTC()}
	finish := func() providers.Result {
		result.CompletedAt = c.now().UTC()
		result.ErrorMessage = providers.Sanitize(result.ErrorMessage, c.secrets)
		result.RawJSON = providers.SanitizeBytes(result.RawJSON, c.secrets)
		return result
	}

	body, err := json.Marshal(map[string]any{
		"model": req.Model,
		"input": req.QuestionText,
		"tools": []map[string]any{{"type": "web_search"}},
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
		URL:    c.baseURL + "/responses",
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

	var payload responsePayload
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "response_not_json"
		result.ErrorMessage = fmt.Sprintf("HTTP %d: response is not JSON: %s", resp.Status, truncate(string(resp.Body)))
		return finish(), nil
	}

	if resp.Status >= 400 || payload.Error != nil {
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = payload.errorCode(resp.Status)
		result.ErrorMessage = payload.errorMessage()
		if result.ErrorMessage == "" {
			result.ErrorMessage = fmt.Sprintf("HTTP %d: %s", resp.Status, truncate(string(resp.Body)))
		}
		return finish(), nil
	}

	result.AnswerText = payload.answerText()
	result.Citations, result.Observability = payload.citations()
	result.Usage = payload.usage()

	if payload.Status == "failed" {
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "failed"
		result.ErrorMessage = payload.errorMessage()
		if result.ErrorMessage == "" {
			result.ErrorMessage = "provider reported status=failed"
		}
		return finish(), nil
	}

	if strings.TrimSpace(result.AnswerText) == "" && len(result.Citations) == 0 {
		// An empty answer is a failed fetch, not "no citation".
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "empty_answer"
		result.ErrorMessage = "empty answer: no text and no citations (treated as a failed fetch)"
		if payload.IncompleteDetails != nil && payload.IncompleteDetails.Reason != "" {
			result.ErrorMessage = fmt.Sprintf("%s (incomplete: %s)", result.ErrorMessage, payload.IncompleteDetails.Reason)
		}
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
	ID                string        `json:"id"`
	Model             string        `json:"model"`
	Status            string        `json:"status"`
	OutputText        string        `json:"output_text"`
	Output            []outputItem  `json:"output"`
	Usage             *usagePayload `json:"usage"`
	Error             *errorPayload `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type outputItem struct {
	Type    string          `json:"type"`
	Status  string          `json:"status"`
	Content []contentItem   `json:"content"`
	Action  json.RawMessage `json:"action"`
}

type contentItem struct {
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

type usagePayload struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
	TotalTokens  *int `json:"total_tokens"`
}

type errorPayload struct {
	Code    any    `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (p *responsePayload) answerText() string {
	if strings.TrimSpace(p.OutputText) != "" {
		return p.OutputText
	}
	parts := make([]string, 0, 2)
	for _, item := range p.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if strings.TrimSpace(content.Text) != "" {
				parts = append(parts, content.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// citations returns the cited URLs and whether the response carried citation
// metadata at all. An annotations array that is present but empty is "observed,
// zero citations"; a response with no annotations field anywhere is "unknown".
func (p *responsePayload) citations() ([]providers.Citation, string) {
	citations := make([]providers.Citation, 0, 4)
	sawMetadata := false
	sawMessage := false
	for _, item := range p.Output {
		if item.Type == "message" {
			sawMessage = true
		}
		if item.Type == "web_search_call" {
			sawMetadata = true
		}
		for _, content := range item.Content {
			if content.Annotations == nil {
				continue
			}
			sawMetadata = true
			for _, annotation := range *content.Annotations {
				if annotation.Type != "url_citation" || annotation.URL == "" {
					continue
				}
				citations = append(citations, providers.Citation{
					RawURL:     annotation.URL,
					Title:      annotation.Title,
					StartIndex: annotation.StartIndex,
					EndIndex:   annotation.EndIndex,
				})
			}
		}
	}
	if !sawMessage {
		return citations, providers.ObservabilityUnknown
	}
	return citations, providers.HasCitationReports(sawMetadata)
}

func (p *responsePayload) usage() providers.Usage {
	usage := providers.Usage{}
	if p.Usage != nil {
		usage.InputTokens = p.Usage.InputTokens
		usage.OutputTokens = p.Usage.OutputTokens
		usage.TotalTokens = p.Usage.TotalTokens
	}
	// The search calls are visible in the output items, so they are counted even
	// when the provider omits the usage block (tokens stay nil, never zero).
	toolCalls := 0
	for _, item := range p.Output {
		if item.Type == "web_search_call" {
			toolCalls++
		}
	}
	usage.ToolCalls = &toolCalls
	return usage
}

func (p *responsePayload) errorCode(status int) string {
	if p.Error == nil {
		return fmt.Sprintf("http_%d", status)
	}
	switch code := p.Error.Code.(type) {
	case string:
		if code != "" {
			return code
		}
	case float64:
		return fmt.Sprintf("%d", int(code))
	}
	if p.Error.Type != "" {
		return p.Error.Type
	}
	return fmt.Sprintf("http_%d", status)
}

func (p *responsePayload) errorMessage() string {
	if p.Error == nil {
		return ""
	}
	message := p.Error.Message
	if message == "" {
		message = p.Error.Type
	}
	if p.Error.Code != nil {
		if code, ok := p.Error.Code.(string); ok && code != "" {
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
