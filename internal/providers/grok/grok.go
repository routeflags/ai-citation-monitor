// Package grok implements the xAI adapter for the Responses API with the
// web_search server-side tool.
//
// API surface used (checked against the published documentation at
// implementation time, not guessed, 2026-11-26):
//
//	POST {base}/responses
//	header Authorization: Bearer <key>
//	{"model": ..., "input": [{"role": "user", "content": ...}],
//	 "tools": [{"type": "web_search"}]}
//
// Citations come only from the message content annotations of type
// `url_citation` (url / title / start_index / end_index). The top-level
// `citations` array is "every URL the search saw", which the citations page
// states is not necessarily what the answer attributed its text to, so it is
// not counted: appearing in the search results is not being cited. A
// `[[1]](url)` markdown link inside the answer text is not a citation either.
//
// The request does not send `include`. Inline citations are on by default for
// the Responses API; asking for `no_inline_citations` would leave the
// annotations as a list of encountered sources without a position in the text,
// which is a different (weaker) claim than "the answer cited this".
package grok

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

const (
	// WebSearchToolType is the request tool type. The tool carries no dated
	// version of its own, so the provider name is what keeps this surface apart
	// from the OpenAI one in the record.
	WebSearchToolType = "web_search"
	// WebCitationType is the only annotation type that counts as a citation.
	WebCitationType = "url_citation"
	// EndpointVersion is the surface this adapter speaks. It is deliberately not
	// OpenAI's `responses/v1+web_search`: the provider differs, so the
	// measurements must not be mixed.
	EndpointVersion = "responses/v1+" + WebSearchToolType + "+xai"
)

// Client talks to the xAI Responses API.
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
func (c *Client) Name() string { return "grok" }

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
		"model": req.Model,
		// The published curl example sends a role/content array; a bare string
		// is also accepted, but the documented shape is the one to speak.
		"input": []map[string]any{{
			"role":    "user",
			"content": req.QuestionText,
		}},
		"tools": []map[string]any{{
			"type": WebSearchToolType,
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

	// A failed call is decoded through its error envelope first, and only that.
	// Reading a 401 as a success body is how an invalid key turns into "the
	// response is not JSON" and loses the provider's own reason.
	if resp.Status >= 400 {
		var envelope errorEnvelope
		if err := json.Unmarshal(resp.Body, &envelope); err == nil && envelope.recognised() {
			result.Status = providers.StatusError
			result.ErrorKind = providers.ErrorKindProvider
			result.ErrorCode = envelope.code(resp.Status)
			result.ErrorMessage = envelope.message()
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
		result.ErrorCode = payload.Error.code(resp.Status)
		result.ErrorMessage = payload.Error.message()
		if result.ErrorMessage == "" {
			result.ErrorMessage = fmt.Sprintf("HTTP %d: %s", resp.Status, truncate(string(resp.Body)))
		}
		return finish(), nil
	}

	result.AnswerText = payload.answerText()
	result.Citations, result.Observability = payload.citations()
	result.Usage = payload.usage()

	if refusal := payload.refusal(); refusal != "" {
		// A refusal is not a zero-citation answer: counting it would lower every
		// citation rate for a reason the measurement did not observe.
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = "refusal"
		result.ErrorMessage = "the model declined to answer: " + truncate(refusal)
		return finish(), nil
	}

	if code, message := payload.statusError(); code != "" {
		// A cut-off, unfinished or unknown status is not a short answer: the
		// citation set cannot be trusted, so it is recorded as a failure.
		result.Status = providers.StatusError
		result.ErrorKind = providers.ErrorKindProvider
		result.ErrorCode = code
		result.ErrorMessage = message
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

// responsePayload is the success body. The top-level `citations` array is
// deliberately not decoded: it lists the URLs the search encountered, not the
// ones the answer cited (see the package comment).
type responsePayload struct {
	ID                string        `json:"id"`
	Model             string        `json:"model"`
	Status            string        `json:"status"`
	Output            []outputItem  `json:"output"`
	Usage             *usagePayload `json:"usage"`
	Error             *errorPayload `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type outputItem struct {
	Type    string        `json:"type"`
	Status  string        `json:"status"`
	Content []contentItem `json:"content"`
}

// contentItem is one block of a message. Annotations is a pointer so "the
// provider sent no annotations key" (unknown) stays different from
// "annotations: []" (observed, zero citations).
type contentItem struct {
	Type        string            `json:"type"`
	Text        string            `json:"text"`
	Refusal     string            `json:"refusal"`
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
	ServerSide   *struct {
		WebSearchCalls *int `json:"web_search_calls"`
	} `json:"server_side_tool_usage_details"`
}

// errorEnvelope accepts the two shapes a failed call may arrive in: the nested
// one ({"error": {"code", "message"}}, OpenAI compatible) and the flat one
// ({"code": ..., "error": "message"}). xAI publishes no error-body schema, so
// betting on one of them would turn an unrecognised envelope into the provider
// code the operator needs.
type errorEnvelope struct {
	Code  flexibleString `json:"code"`
	Error *errorPayload  `json:"error"`
}

// errorPayload is either object or string on the wire.
type errorPayload struct {
	Code    string
	Type    string
	Message string
}

func (p *errorPayload) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		p.Message = text
		return nil
	}
	var object struct {
		Code    flexibleString `json:"code"`
		Type    string         `json:"type"`
		Message string         `json:"message"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		// An unknown envelope is not a decode failure: the caller falls back to
		// the HTTP status and the start of the body.
		return nil
	}
	p.Code = string(object.Code)
	p.Type = object.Type
	p.Message = object.Message
	return nil
}

// flexibleString accepts a JSON string or number (an error code may be either).
type flexibleString string

func (s *flexibleString) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*s = flexibleString(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err == nil {
		*s = flexibleString(number.String())
		return nil
	}
	*s = ""
	return nil
}

// recognised reports whether the body actually looked like an error envelope.
func (e *errorEnvelope) recognised() bool {
	return e.Error != nil || e.Code != ""
}

// code is the provider's own error code, which is the most useful thing to read
// in the record; the HTTP status stays in http_status.
func (e *errorEnvelope) code(status int) string {
	if e != nil && e.Error != nil && e.Error.Code != "" {
		return e.Error.Code
	}
	if e != nil && e.Code != "" {
		return string(e.Code)
	}
	return fmt.Sprintf("http_%d", status)
}

func (e *errorEnvelope) message() string {
	if e == nil || e.Error == nil {
		return ""
	}
	return e.Error.message()
}

func (p *errorPayload) code(status int) string {
	if p == nil {
		return fmt.Sprintf("http_%d", status)
	}
	if p.Code != "" {
		return p.Code
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
	if message == "" {
		message = strings.TrimSpace(p.Type)
	} else if p.Type != "" {
		message = fmt.Sprintf("%s (%s)", message, p.Type)
	}
	return message
}

func (p *responsePayload) answerText() string {
	parts := make([]string, 0, 2)
	for _, item := range p.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type != "output_text" || strings.TrimSpace(content.Text) == "" {
				continue
			}
			parts = append(parts, content.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (p *responsePayload) refusal() string {
	for _, item := range p.Output {
		for _, content := range item.Content {
			if content.Type == "refusal" && strings.TrimSpace(content.Refusal) != "" {
				return content.Refusal
			}
		}
	}
	return ""
}

// citations returns the cited URLs and whether the response carried citation
// metadata at all. An annotations array that is present but empty is "observed,
// zero citations"; a response with no annotations field and no search item is
// "unknown". The top-level citations array is not metadata about the answer, so
// it does not make a response observed.
func (p *responsePayload) citations() ([]providers.Citation, string) {
	citations := make([]providers.Citation, 0, 4)
	sawMetadata := false
	sawMessage := false
	for _, item := range p.Output {
		switch item.Type {
		case "message":
			sawMessage = true
		case "web_search_call":
			// The search ran, so the provider reported citation metadata even
			// when it attributed nothing.
			sawMetadata = true
		}
		for _, content := range item.Content {
			if content.Annotations == nil {
				continue
			}
			sawMetadata = true
			for _, annotation := range *content.Annotations {
				if annotation.Type != WebCitationType || annotation.URL == "" {
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
		// The API reports a total; it is recorded only when it is there, never
		// added up here.
		usage.TotalTokens = p.Usage.TotalTokens
		if p.Usage.ServerSide != nil && p.Usage.ServerSide.WebSearchCalls != nil {
			usage.ToolCalls = p.Usage.ServerSide.WebSearchCalls
		}
	}
	if usage.ToolCalls == nil {
		// The searches are visible as web_search_call items, so they are counted
		// even when the usage block is missing (tokens stay nil, never zero).
		count := 0
		for _, item := range p.Output {
			if item.Type == "web_search_call" {
				count++
			}
		}
		usage.ToolCalls = &count
	}
	return usage
}

// statusError maps the statuses that make an answer unusable to an error code.
// `completed` (and an absent status) is the only success; anything else means
// the response is not a final, complete answer.
func (p *responsePayload) statusError() (string, string) {
	switch p.Status {
	case "", "completed":
		return "", ""
	case "incomplete":
		reason := ""
		if p.IncompleteDetails != nil {
			reason = p.IncompleteDetails.Reason
		}
		if reason == "" {
			reason = "incomplete"
		}
		return reason, fmt.Sprintf("status=incomplete (incomplete_details.reason=%s): %s", reason, incompleteHint(reason))
	case "in_progress":
		return "in_progress", "status=in_progress: the response is not final, and this tool sends one non-streaming request per call, so nothing complete was measured"
	default:
		return p.Status, fmt.Sprintf("status=%s: the provider did not complete the answer, so the citation set cannot be trusted", p.Status)
	}
}

func incompleteHint(reason string) string {
	switch reason {
	case "max_output_tokens":
		return "the answer was cut off, so the citation set is incomplete (raise max_output_tokens or shorten the question)"
	case "max_prompt_tokens":
		return "the input exceeded the context window, so nothing complete was measured"
	case "max_time_limit":
		return "the request hit the server time limit, so nothing complete was measured"
	case "content_filter":
		return "the answer was blocked by the content filter (a blocked answer is not a zero-citation answer)"
	default:
		return "the provider marked the answer incomplete, so it is not a measurement"
	}
}

func truncate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 300 {
		return value[:300] + "..."
	}
	return value
}
