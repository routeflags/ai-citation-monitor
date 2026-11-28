package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastClient(maxRetries int) *Client {
	client := New(5*time.Second, 0, maxRetries)
	client.BaseDelay = time.Millisecond
	client.MaxDelay = 5 * time.Millisecond
	return client
}

func TestRetriesOn429AndHonoursRetryAfter(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := fastClient(2)
	response, err := client.Do(context.Background(), Request{Method: http.MethodGet, URL: server.URL})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if response.Status != http.StatusOK {
		t.Fatalf("status = %d", response.Status)
	}
	if response.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", response.Attempts)
	}
	if calls.Load() != 2 {
		t.Fatalf("server saw %d calls, want 2", calls.Load())
	}
}

func TestDoesNotRetryAuthenticationFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer server.Close()

	client := fastClient(3)
	response, err := client.Do(context.Background(), Request{Method: http.MethodGet, URL: server.URL})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if response.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Status)
	}
	if calls.Load() != 1 {
		t.Fatalf("authentication failures must not be retried (calls = %d)", calls.Load())
	}
}

func TestRetriesOn5xxUpToTheLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`upstream`))
	}))
	defer server.Close()

	client := fastClient(2)
	response, err := client.Do(context.Background(), Request{Method: http.MethodGet, URL: server.URL})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if response.Status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Status)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3 (first + 2 retries)", calls.Load())
	}
}

func TestCancellationStopsTheRetryLoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := fastClient(5)
	client.BaseDelay = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Do(ctx, Request{Method: http.MethodGet, URL: server.URL}); err == nil {
		t.Fatal("a cancelled context must return an error")
	}
}

// zeroReader fills every buffer it is given, so a test can serve a body of a
// given size without allocating it first.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

func bodyServer(size int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(size))
		w.WriteHeader(http.StatusOK)
		_, _ = io.CopyN(w, zeroReader{}, int64(size))
	}))
}

// A body at the cap is a normal response. Before this check the cap was applied
// with LimitReader alone, so an oversized body was silently truncated and the
// adapter reported a JSON error instead of the size.
func TestBodyAtTheLimitIsAccepted(t *testing.T) {
	server := bodyServer(maxBodyBytes)
	defer server.Close()

	response, err := fastClient(0).Do(context.Background(), Request{Method: http.MethodGet, URL: server.URL})
	if err != nil {
		t.Fatalf("a body at the limit must be accepted: %v", err)
	}
	if len(response.Body) != maxBodyBytes {
		t.Fatalf("body = %d bytes, want %d", len(response.Body), maxBodyBytes)
	}
}

// A retryable status wins over the size check: an answer saying the server is
// busy is worth another attempt even when its body is oversized, and a retried
// body is discarded.
func TestRetryableStatusIsRetriedBeforeTheSizeCheck(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(maxBodyBytes+1))
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.CopyN(w, zeroReader{}, int64(maxBodyBytes)+1)
	}))
	defer server.Close()

	client := fastClient(1)
	client.Sleep = func(context.Context, time.Duration) error { return nil }
	_, err := client.Do(context.Background(), Request{Method: http.MethodGet, URL: server.URL})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge on the last attempt", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2 (a 503 is retried even with a large body)", got)
	}
}

// One byte over the cap is an error that names the size, and it is not retried:
// the same call would return the same oversized body.
func TestBodyAboveTheLimitIsRejectedWithoutRetrying(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(maxBodyBytes+1))
		w.WriteHeader(http.StatusOK)
		_, _ = io.CopyN(w, zeroReader{}, int64(maxBodyBytes)+1)
	}))
	defer server.Close()

	client := fastClient(2)
	client.Sleep = func(context.Context, time.Duration) error { return nil }
	_, err := client.Do(context.Background(), Request{Method: http.MethodGet, URL: server.URL})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}
	if !strings.Contains(err.Error(), strconv.Itoa(maxBodyBytes)) {
		t.Errorf("the error must name the limit: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("requests = %d, want 1 (an oversized body must not be retried)", got)
	}
}
