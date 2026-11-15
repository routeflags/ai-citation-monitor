package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
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
