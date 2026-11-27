// Package providers defines the boundary between the measurement core and the
// AI services. Provider-specific JSON never leaves the adapter packages: the
// runner only sees Result.
package providers

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Status values of a single call.
const (
	StatusSuccess   = "success"
	StatusError     = "error"
	StatusCancelled = "cancelled"
)

// ErrorKinds of a failed call. They drive the process exit code: transport and
// provider failures are exit 4 when nothing succeeded, cancellation is exit 1.
const (
	ErrorKindNone      = ""
	ErrorKindProvider  = "provider"
	ErrorKindTransport = "transport"
	ErrorKindCancelled = "cancelled"
)

// Observability values for citations. `unknown` means the response carried no
// citation metadata at all; it is not the same as "no citation" and must not be
// counted as zero.
const (
	ObservabilityObserved = "observed"
	ObservabilityUnknown  = "unknown"
)

// Request is one question asked of one model.
type Request struct {
	QuestionKey  string
	QuestionText string
	Model        string
}

// Citation is a source the provider itself attributed the answer to.
type Citation struct {
	RawURL     string
	Title      string
	StartIndex *int
	EndIndex   *int
	// SourceHost is the host a provider reports next to an opaque citation URL
	// (Gemini's grounding chunks point at a redirect and name the real site in the
	// chunk). Empty when the URL is the page itself.
	SourceHost string
}

// Usage carries the numbers the provider reported. Nil means "not reported".
type Usage struct {
	InputTokens  *int
	OutputTokens *int
	TotalTokens  *int
	ToolCalls    *int
	Raw          json.RawMessage
}

// Result is the outcome of one call, success or failure.
type Result struct {
	Status        string
	ErrorKind     string
	ErrorCode     string
	ErrorMessage  string
	HTTPStatus    int
	AnswerText    string
	Citations     []Citation
	Observability string
	Usage         Usage
	RawJSON       []byte
	StartedAt     time.Time
	CompletedAt   time.Time
}

// Provider is one AI service adapter.
type Provider interface {
	// Name is the provider id written into every run row
	// (openai / gemini / anthropic / perplexity / grok).
	Name() string
	// EndpointVersion names the API surface this adapter speaks, so a future
	// change of surface does not silently mix measurements.
	EndpointVersion() string
	// Ask performs exactly one round trip.
	Ask(ctx context.Context, req Request) (Result, error)
}

// LiveObservation is the one live call each adapter has had: provider -> model
// and date. It is a fact about this build, not a test: CI never calls a provider,
// and one observed model is not a supported-model list.
var LiveObservation = map[string]string{
	"openai":     "gpt-6.1-sol, 2026-11-15",
	"gemini":     "gemini-3.1-pro-preview, 2026-11-15",
	"anthropic":  "claude-sonnet-5-5, 2026-11-15",
	"perplexity": "openai/gpt-5.6-sol, 2026-11-28",
	"grok":       "grok-4.7, 2026-11-28",
}

// HasCitationReports reports whether the response carried citation metadata at
// all. Empty metadata ("the provider reports no citations for this answer") is
// observed; absent metadata is unknown.
func HasCitationReports(sawMetadata bool) string {
	if sawMetadata {
		return ObservabilityObserved
	}
	return ObservabilityUnknown
}

// Sanitize removes credential values from a string before it is stored or
// logged. Adapters call it on error messages and on the raw JSON body.
func Sanitize(value string, secrets []string) string {
	for _, secret := range secrets {
		if len(secret) < 8 {
			continue // too short to be a real credential; redacting it would mangle the text
		}
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}
	return value
}

// SanitizeBytes is Sanitize for a raw body.
func SanitizeBytes(value []byte, secrets []string) []byte {
	if len(secrets) == 0 {
		return value
	}
	return []byte(Sanitize(string(value), secrets))
}

// intPointer is a helper for adapters that decode optional numbers.
func IntPointer(value *int) *int { return value }
