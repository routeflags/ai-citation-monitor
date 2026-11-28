// Package httpclient is the shared HTTP layer: one client, one transport, a
// context on every request, a rate limiter, and retries only for the failures
// that can succeed on a second attempt.
//
// It is deliberately the only package that dials the network. Adapters use it so
// connection reuse, timeouts and retry policy stay in one place.
package httpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// Request is one HTTP call.
type Request struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// Response is the final response (after any retries).
type Response struct {
	Status   int
	Header   http.Header
	Body     []byte
	Attempts int
}

// Client wraps http.Client with retry and pacing.
type Client struct {
	HTTP       *http.Client
	Limiter    *rate.Limiter
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Now        func() time.Time
	Sleep      func(ctx context.Context, d time.Duration) error
	Logf       func(format string, args ...any)
}

// New builds a client with connection reuse and an optional RPM limit.
func New(timeout time.Duration, rpm int, maxRetries int) *Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
		ForceAttemptHTTP2:     true,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	client := &Client{
		HTTP:       &http.Client{Transport: transport, Timeout: timeout},
		MaxRetries: maxRetries,
		BaseDelay:  2 * time.Second,
		MaxDelay:   30 * time.Second,
		Now:        time.Now,
		Sleep:      sleepContext,
	}
	if rpm > 0 {
		// A small burst keeps a run started without waiting for the first slot.
		burst := rpm / 4
		if burst < 1 {
			burst = 1
		}
		client.Limiter = rate.NewLimiter(rate.Limit(float64(rpm)/60.0), burst)
	}
	return client
}

// maxBodyBytes caps a response body: a provider answer with its citations is far
// smaller, and the cap keeps a misbehaving endpoint from making the tool read an
// unbounded stream into memory.
const maxBodyBytes = 16 << 20

// ErrResponseTooLarge reports a response body above maxBodyBytes. It is returned
// without a retry: the same call would return the same oversized body, and a
// truncated body would reach the adapter as a JSON error that hides the real
// cause.
var ErrResponseTooLarge = errors.New("response body exceeds the size limit")

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Do performs the request, retrying only on 429 / 500 / 502 / 503 / 504 and on
// transport failures. Authentication and validation failures are returned at
// once: retrying them only wastes quota and time.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	c.applyFallbacks()
	var lastErr error
	var pendingDelay time.Duration
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := c.pauseBeforeRetry(ctx, attempt, &pendingDelay); err != nil {
				return nil, err
			}
		}
		if err := c.waitForSlot(ctx); err != nil {
			return nil, err
		}

		httpReq, err := buildRequest(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}

		resp, err := c.HTTP.Do(httpReq)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("http: %w", err)
			if !retryableTransportError(err) {
				return nil, lastErr
			}
			continue
		}

		result, retry, err := c.readAttempt(resp, attempt)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}
		lastErr = retry.err
		pendingDelay = retry.delay
	}
	return nil, lastErr
}

// applyFallbacks fills the fields a caller may leave out. They are filled on the
// client and not on a copy, because the zero value has to work too.
func (c *Client) applyFallbacks() {
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 90 * time.Second}
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
}

// pauseBeforeRetry waits before the next attempt. A delay the server asked for
// with Retry-After wins over the exponential backoff, and is consumed once.
func (c *Client) pauseBeforeRetry(ctx context.Context, attempt int, pendingDelay *time.Duration) error {
	delay := *pendingDelay
	*pendingDelay = 0
	if delay <= 0 {
		delay = c.backoff(attempt)
	}
	if c.Logf != nil {
		c.Logf("retry %d/%d in %s", attempt, c.MaxRetries, delay.Round(time.Millisecond))
	}
	return c.Sleep(ctx, delay)
}

func (c *Client) waitForSlot(ctx context.Context) error {
	if c.Limiter == nil {
		return nil
	}
	return c.Limiter.Wait(ctx)
}

// retryDecision is why one attempt is retried: the error to report if the last
// attempt fails, and the delay the server asked for (zero means backoff).
type retryDecision struct {
	err   error
	delay time.Duration
}

// readAttempt reads one response and decides what happens next: a final response,
// a retry (with its reason and any requested delay), or an error that ends the
// call. A nil response and a nil retry mean the attempt is the last one.
func (c *Client) readAttempt(resp *http.Response, attempt int) (*Response, *retryDecision, error) {
	body, err := readBody(resp)
	if err != nil {
		return nil, &retryDecision{err: fmt.Errorf("read body: %w", err)}, nil
	}
	if retryableStatus(resp.StatusCode) && attempt < c.MaxRetries {
		// The retry decision comes before the size check: an answer that says "the
		// server is busy" is worth another attempt whatever its body size, and the
		// body of a retried attempt is discarded anyway.
		decision := &retryDecision{err: fmt.Errorf("http %d: %s", resp.StatusCode, snippet(body))}
		if wait, ok := retryAfter(resp.Header, c.Now()); ok {
			if c.Logf != nil {
				c.Logf("server asked to wait %s (HTTP %d)", wait.Round(time.Millisecond), resp.StatusCode)
			}
			decision.delay = wait
		}
		return nil, decision, nil
	}
	if len(body) > maxBodyBytes {
		// One byte past the cap is read, so the real size is only known to be
		// larger than the limit. The body is not retried: its size does not depend
		// on the attempt.
		return nil, nil, fmt.Errorf("%w: more than %d bytes", ErrResponseTooLarge, maxBodyBytes)
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body, Attempts: attempt + 1}, nil, nil
}

// readBody reads at most one byte past the cap, so the caller can tell an
// oversized body from one that fits.
func readBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
}

func buildRequest(ctx context.Context, req Request) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return nil, err
	}
	for key, value := range req.Headers {
		httpReq.Header.Set(key, value)
	}
	httpReq.Header.Set("Accept", "application/json")
	return httpReq, nil
}

func (c *Client) backoff(attempt int) time.Duration {
	base := c.BaseDelay
	if base <= 0 {
		base = 2 * time.Second
	}
	delay := base * time.Duration(1<<uint(attempt-1))
	max := c.MaxDelay
	if max <= 0 {
		max = 30 * time.Second
	}
	if delay > max {
		delay = max
	}
	// Jitter avoids a thundering herd when several workers retry together.
	jitter := time.Duration(rand.Int63n(int64(delay/4) + 1))
	return delay + jitter
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// retryableTransportError retries only the failures where a second attempt can
// plausibly help. A DNS or TLS failure is reported at once.
func retryableTransportError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || strings.Contains(strings.ToLower(netErr.Error()), "temporary")
	}
	return false
}

func retryAfter(header http.Header, now time.Time) (time.Duration, bool) {
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := when.Sub(now)
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}
	return 0, false
}

func snippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		text = text[:300] + "..."
	}
	return text
}
