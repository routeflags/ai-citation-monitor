// Package gemini implements the Gemini adapter for generateContent with the
// Google Search grounding tool.
//
// API surface used (checked against the published documentation at
// implementation time, not guessed):
//
//	POST {base}/models/{model}:generateContent
//	header x-goog-api-key: <key>
//	{"contents": [{"role": "user", "parts": [{"text": ...}]}],
//	 "tools": [{"google_search": {}}]}
//
// Citations come only from candidates[].groundingMetadata.groundingChunks[].web.uri.
// A response without groundingMetadata is recorded as citation_observability=unknown
// (the provider returned no citation metadata), never as "zero citations".
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/routeflags/ai-citation-monitor/internal/providers"
	"github.com/routeflags/ai-citation-monitor/internal/providers/httpclient"
)

// Client talks to the Gemini API.
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
func (c *Client) Name() string { return "gemini" }

// EndpointVersion records which API surface produced the run.
func (c *Client) EndpointVersion() string { return "generateContent/v1beta+google_search" }

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
		"contents": []map[string]any{{
			"role":  "user",
			"parts": []map[string]any{{"text": req.QuestionText}},
		}},
		"tools": []map[string]any{{"google_search": map[string]any{}}},
	})
	if err != nil {
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "request_encode"
		result.ErrorMessage = err.Error()
		return finish(), nil
	}

	endpoint := fmt.Sprintf("%s/models/%s:generateContent", c.baseURL, url.PathEscape(req.Model))
	resp, err := c.http.Do(ctx, httpclient.Request{
		Method: http.MethodPost,
		URL:    endpoint,
		Headers: map[string]string{
			"Content-Type":   "application/json",
			"x-goog-api-key": c.apiKey,
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

	// A failed call is decoded through its error envelope first, never through the
	// success shape: one unexpected type in the success fields must not be able to
	// hide the provider's own error code and message.
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

	if payload.PromptFeedback != nil && payload.PromptFeedback.BlockReason != "" {
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "blocked"
		result.ErrorMessage = "prompt blocked: " + payload.PromptFeedback.BlockReason
		return finish(), nil
	}

	if strings.TrimSpace(result.AnswerText) == "" && len(result.Citations) == 0 {
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "empty_answer"
		result.ErrorMessage = "empty answer: no text and no citations (treated as a failed fetch)"
		if payload.finishReason() != "" {
			result.ErrorMessage = fmt.Sprintf("%s (finishReason: %s)", result.ErrorMessage, payload.finishReason())
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
	Candidates     []candidate `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *usagePayload `json:"usageMetadata"`
	Error         *errorPayload `json:"error"`
}

// errorEnvelope is the only shape an error response is decoded into.
type errorEnvelope struct {
	Error *errorPayload `json:"error"`
}

type candidate struct {
	Content *struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"content"`
	FinishReason      string             `json:"finishReason"`
	GroundingMetadata *groundingMetadata `json:"groundingMetadata"`
}

type groundingMetadata struct {
	WebSearchQueries []string `json:"webSearchQueries"`
	GroundingChunks  []struct {
		Web *struct {
			URI    string `json:"uri"`
			Title  string `json:"title"`
			Domain string `json:"domain"`
		} `json:"web"`
	} `json:"groundingChunks"`
}

type usagePayload struct {
	PromptTokenCount     *int `json:"promptTokenCount"`
	CandidatesTokenCount *int `json:"candidatesTokenCount"`
	TotalTokenCount      *int `json:"totalTokenCount"`
}

type errorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func (p *responsePayload) answerText() string {
	parts := make([]string, 0, 2)
	for _, candidate := range p.Candidates {
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func (p *responsePayload) citations() ([]providers.Citation, string) {
	citations := make([]providers.Citation, 0, 4)
	sawMetadata := false
	for _, candidate := range p.Candidates {
		if candidate.GroundingMetadata == nil {
			continue
		}
		sawMetadata = true
		for _, chunk := range candidate.GroundingMetadata.GroundingChunks {
			if chunk.Web == nil || chunk.Web.URI == "" {
				continue
			}
			citation := providers.Citation{RawURL: chunk.Web.URI, Title: chunk.Web.Title}
			if hint := sourceHost(chunk.Web.URI, chunk.Web.Domain, chunk.Web.Title); hint != "" {
				citation.SourceHost = hint
			}
			citations = append(citations, citation)
		}
	}
	if len(p.Candidates) == 0 {
		return citations, providers.ObservabilityUnknown
	}
	return citations, providers.HasCitationReports(sawMetadata)
}

func (p *responsePayload) usage() providers.Usage {
	if p.UsageMetadata == nil {
		return providers.Usage{}
	}
	usage := providers.Usage{
		InputTokens:  p.UsageMetadata.PromptTokenCount,
		OutputTokens: p.UsageMetadata.CandidatesTokenCount,
		TotalTokens:  p.UsageMetadata.TotalTokenCount,
	}
	toolCalls := 0
	for _, candidate := range p.Candidates {
		if candidate.GroundingMetadata != nil {
			toolCalls += len(candidate.GroundingMetadata.WebSearchQueries)
		}
	}
	usage.ToolCalls = &toolCalls
	return usage
}

func (p *responsePayload) finishReason() string {
	for _, candidate := range p.Candidates {
		if candidate.FinishReason != "" {
			return candidate.FinishReason
		}
	}
	return ""
}

func (p *errorPayload) errorCode(status int) string {
	if p == nil {
		return fmt.Sprintf("http_%d", status)
	}
	if p.Status != "" {
		return p.Status
	}
	if p.Code != 0 {
		return fmt.Sprintf("code_%d", p.Code)
	}
	return fmt.Sprintf("http_%d", status)
}

func (p *errorPayload) errorMessage() string {
	if p == nil {
		return ""
	}
	message := strings.TrimSpace(p.Message)
	if p.Status != "" {
		message = fmt.Sprintf("%s (%s)", message, p.Status)
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

// groundingRedirectHost is the host Gemini uses for the opaque redirect that a
// grounding chunk points at. The real source is the chunk's domain/title, not the
// URL: recording the redirect as the cited host would make every citation look
// like Google and no site could ever be "cited".
const groundingRedirectHost = "vertexaisearch.cloud.google.com"

// sourceHost returns the site a grounding chunk refers to, or "" when the URI is
// the page itself. It prefers the chunk's own domain field and falls back to the
// title when the title is a bare host name ("go.dev"), never a sentence.
func sourceHost(uri, domain, title string) string {
	if isGroundingRedirect(uri) {
		if host := strings.TrimSpace(domain); host != "" {
			return host
		}
		return strings.TrimSpace(title)
	}
	if strings.TrimSpace(domain) != "" && !hostOf(uri, strings.TrimSpace(domain)) {
		return strings.TrimSpace(domain)
	}
	return ""
}

func isGroundingRedirect(uri string) bool {
	parsed, err := url.Parse(uri)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), groundingRedirectHost)
}

func hostOf(uri, host string) bool {
	parsed, err := url.Parse(uri)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), host)
}
