// Package anthropic implements the Claude adapter for the Messages API with the
// server-side web search tool.
//
// API surface used (checked against the published documentation at implementation
// time, not guessed, 2026-11-15):
//
//	POST {base}/v1/messages
//	header x-api-key: <key>
//	header anthropic-version: 2026-10-21
//	{"model": ..., "max_tokens": 8192,
//	 "messages": [{"role": "user", "content": ...}],
//	 "tools": [{"type": "web_search_20250305", "name": "web_search", "max_uses": 5}]}
//
// The response is a list of content blocks: text, server_tool_use (the search the
// API ran for us) and web_search_tool_result (what that search returned).
//
// Citations come only from the text blocks' `citations[]` entries of type
// `web_search_result_location`. The URLs inside `web_search_tool_result` are the
// candidates the search returned, not what the answer attributed its text to, so
// they are not counted as citations: appearing in the search results is not being
// cited. A URL that merely appears inside the answer text is not a citation
// either.
package anthropic

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

// Values that describe the API surface. They are constants rather than settings
// because they are what the request and the record mean, not how the tool is
// configured.
const (
	// APIVersion is the anthropic-version header of this build. The API is
	// versioned, so it is written into every run: measurements from different
	// versions do not silently mix.
	APIVersion = "2026-10-21"
	// WebSearchToolType is the dated server tool type (basic web search). A tool
	// version is part of the surface: a newer one would be a different measurement.
	WebSearchToolType = "web_search_20250305"
	// EndpointVersion is the surface this adapter speaks.
	EndpointVersion = "messages/v1+" + WebSearchToolType + "+api_" + APIVersion
)

// Defaults for the two numbers the request needs.
const (
	// DefaultMaxTokens is high enough that a search-backed answer normally
	// finishes inside it: a cut-off answer is an incomplete measurement, so it is
	// recorded as an error rather than as a short answer.
	DefaultMaxTokens = 8192
	// DefaultMaxUses bounds the searches one call may run. The published guidance
	// is 1-3 searches for a single-fact question and 10+ for multi-entity research;
	// 5 leaves room for a follow-up without an unbounded bill or latency.
	DefaultMaxUses = 5
)

// Client talks to the Anthropic Messages API.
type Client struct {
	apiKey    string
	baseURL   string
	maxTokens int
	maxUses   int
	http      *httpclient.Client
	secrets   []string
	now       func() time.Time
}

// Options configures the adapter.
type Options struct {
	APIKey    string
	BaseURL   string
	HTTP      *httpclient.Client
	Secrets   []string
	Now       func() time.Time
	MaxTokens int
	MaxUses   int
}

// New builds the adapter.
func New(opts Options) *Client {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	maxTokens := opts.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	maxUses := opts.MaxUses
	if maxUses <= 0 {
		maxUses = DefaultMaxUses
	}
	return &Client{
		apiKey:    opts.APIKey,
		baseURL:   strings.TrimRight(opts.BaseURL, "/"),
		maxTokens: maxTokens,
		maxUses:   maxUses,
		http:      opts.HTTP,
		secrets:   opts.Secrets,
		now:       now,
	}
}

// Name is the provider id.
func (c *Client) Name() string { return "anthropic" }

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

	body, err := json.Marshal(map[string]any{
		"model":      req.Model,
		"max_tokens": c.maxTokens,
		"messages": []map[string]any{{
			"role":    "user",
			"content": req.QuestionText,
		}},
		"tools": []map[string]any{{
			"type":     WebSearchToolType,
			"name":     "web_search",
			"max_uses": c.maxUses,
		}},
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
		URL:    c.baseURL + "/v1/messages",
		Headers: map[string]string{
			"Content-Type":      "application/json",
			"x-api-key":         c.apiKey,
			"anthropic-version": APIVersion,
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
	// ({"type":"error","error":{"type":"authentication_error",...}}). Reading it
	// as a success body is how a 401 turns into "the response is not JSON" and
	// loses the provider's own reason.
	if resp.Status >= 400 {
		var envelope errorEnvelope
		if err := json.Unmarshal(resp.Body, &envelope); err == nil && envelope.Error != nil {
			result.Status = providers.StatusError
			result.ErrorKind = providers.ErrorKindProvider
			result.ErrorCode = envelope.Error.code(resp.Status)
			result.ErrorMessage = envelope.Error.message()
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

	var payload messagePayload
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
		result.ErrorCode = payload.Error.code(resp.Status)
		result.ErrorMessage = payload.Error.message()
		return finish(), nil
	}

	result.AnswerText = payload.answerText()
	result.Citations, result.Observability = payload.citations()
	result.Usage = payload.usage()

	if code := payload.stopReasonError(); code != "" {
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = code
		result.ErrorMessage = payload.stopReasonMessage()
		return finish(), nil
	}

	if strings.TrimSpace(result.AnswerText) == "" && len(result.Citations) == 0 {
		// An empty answer is a failed fetch, not "no citation".
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "empty_answer"
		result.ErrorMessage = "empty answer: no text and no citations (treated as a failed fetch)"
		if payload.StopReason != "" {
			result.ErrorMessage = fmt.Sprintf("%s (stop_reason: %s)", result.ErrorMessage, payload.StopReason)
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

type messagePayload struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []contentBlock `json:"content"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        *usagePayload  `json:"usage"`
	Error        *errorPayload  `json:"error"`
}

// errorEnvelope is the only shape an error response is decoded into.
type errorEnvelope struct {
	Type  string        `json:"type"`
	Error *errorPayload `json:"error"`
}

type errorPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// code is the provider's own error type (authentication_error, rate_limit_error,
// ...). It is the most useful thing to read in the record; the HTTP status stays
// in http_status.
func (p *errorPayload) code(status int) string {
	if p == nil {
		return fmt.Sprintf("http_%d", status)
	}
	if p.Type != "" {
		return p.Type
	}
	return fmt.Sprintf("http_%d", status)
}

func (p *errorPayload) message() string {
	if p == nil {
		return ""
	}
	message := strings.TrimSpace(p.Message)
	if p.Type != "" && message != "" {
		message = fmt.Sprintf("%s (%s)", message, p.Type)
	}
	return strings.TrimSpace(message)
}

// contentBlock is one block of the response. Citations is a pointer so "the
// provider sent no citations key" (unknown) stays different from
// "citations: []" (observed, zero citations).
type contentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text"`
	Citations *[]citation `json:"citations"`
	Name      string      `json:"name"`
	ID        string      `json:"id"`
	ToolUseID string      `json:"tool_use_id"`
	// Content of a web_search_tool_result block is either a list of results or a
	// single error object, so it is not decoded here: the block type alone is what
	// decides observability.
	Content json.RawMessage `json:"content"`
}

type citation struct {
	Type           string `json:"type"`
	URL            string `json:"url"`
	Title          string `json:"title"`
	CitedText      string `json:"cited_text"`
	EncryptedIndex string `json:"encrypted_index"`
}

type usagePayload struct {
	InputTokens   *int `json:"input_tokens"`
	OutputTokens  *int `json:"output_tokens"`
	ServerToolUse *struct {
		WebSearchRequests *int `json:"web_search_requests"`
	} `json:"server_tool_use"`
}

func (p *messagePayload) answerText() string {
	parts := make([]string, 0, 2)
	for _, block := range p.Content {
		if block.Type != "text" || strings.TrimSpace(block.Text) == "" {
			continue
		}
		parts = append(parts, block.Text)
	}
	return strings.Join(parts, "\n")
}

// citations returns the cited URLs and whether the response carried citation
// metadata at all.
func (p *messagePayload) citations() ([]providers.Citation, string) {
	citations := make([]providers.Citation, 0, 4)
	sawMetadata := false
	for _, block := range p.Content {
		switch block.Type {
		case "web_search_tool_result":
			// The search ran, so the provider did report citation metadata even when
			// the result list is empty or the search itself errored.
			sawMetadata = true
		case "text":
			if block.Citations == nil {
				continue
			}
			sawMetadata = true
			for _, citation := range *block.Citations {
				if citation.Type != "web_search_result_location" || citation.URL == "" {
					continue
				}
				citations = append(citations, providers.Citation{RawURL: citation.URL, Title: citation.Title})
			}
		}
	}
	if len(p.Content) == 0 {
		return citations, providers.ObservabilityUnknown
	}
	return citations, providers.HasCitationReports(sawMetadata)
}

func (p *messagePayload) usage() providers.Usage {
	usage := providers.Usage{}
	if p.Usage != nil {
		usage.InputTokens = p.Usage.InputTokens
		usage.OutputTokens = p.Usage.OutputTokens
		// The Messages API reports no total, and this tool does not add one up: a
		// guessed number is not a measurement.
		if p.Usage.ServerToolUse != nil {
			usage.ToolCalls = p.Usage.ServerToolUse.WebSearchRequests
		}
	}
	if usage.ToolCalls == nil {
		// The searches are visible as server_tool_use blocks, so they are counted
		// even when the usage block is missing (tokens stay nil, never zero).
		count := 0
		for _, block := range p.Content {
			if block.Type == "server_tool_use" && block.Name == "web_search" {
				count++
			}
		}
		usage.ToolCalls = &count
	}
	return usage
}

// stopReasonError maps the stop reasons that make an answer unusable to an error
// code. A cut-off or declined answer is not a zero-citation answer, so it is
// recorded as a failure and stays out of every denominator.
func (p *messagePayload) stopReasonError() string {
	switch p.StopReason {
	case "max_tokens":
		return "max_tokens"
	case "refusal":
		return "refusal"
	case "pause_turn":
		return "pause_turn"
	case "tool_use":
		return "unexpected_tool_use"
	default:
		return ""
	}
}

func (p *messagePayload) stopReasonMessage() string {
	switch p.StopReason {
	case "max_tokens":
		return "stop_reason=max_tokens: the answer was cut off at max_tokens, so the citation set is incomplete (raise [providers.anthropic] max_tokens)"
	case "refusal":
		return "stop_reason=refusal: the model declined to answer (a refusal is not a zero-citation answer)"
	case "pause_turn":
		return "stop_reason=pause_turn: the turn paused for a long-running search and this tool sends one request per call, so nothing complete was measured"
	case "tool_use":
		return "stop_reason=tool_use: the response asked for a client tool call, and this adapter sends server tools only"
	default:
		return "stop_reason=" + p.StopReason
	}
}

func truncate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 300 {
		return value[:300] + "..."
	}
	return value
}
