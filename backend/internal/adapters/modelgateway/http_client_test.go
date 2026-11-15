package modelgateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCreateChatCompletion(t *testing.T) {
	const testKey = "test-only-not-a-real-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("authorization header = %q, want configured bearer token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q, want application/json", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		if got := string(body); !strings.Contains(got, `"model":"test-model"`) || !strings.Contains(got, `"role":"user"`) {
			t.Errorf("request body missing expected fields: %s", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"completion-1","object":"chat.completion","created":123,"model":"test-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL + "/v1", APIKey: testKey, Model: "test-model"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	response, err := client.CreateChatCompletion(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion: %v", err)
	}
	if response.ID != "completion-1" || response.Model != "test-model" || len(response.Choices) != 1 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if response.Choices[0].Message.Content != "hello" || response.Choices[0].FinishReason != "stop" {
		t.Fatalf("unexpected choice: %+v", response.Choices[0])
	}
	if response.Usage.TotalTokens != 3 {
		t.Fatalf("total tokens = %d, want 3", response.Usage.TotalTokens)
	}
}

func TestCreateChatCompletionUsesRequestModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"model":"request-model","choices":[]}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.CreateChatCompletion(context.Background(), ChatRequest{
		Model: "request-model", Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("CreateChatCompletion: %v", err)
	}
	if response.Model != "request-model" {
		t.Fatalf("model = %q, want request-model", response.Model)
	}
}

func TestCreateChatCompletionDoesNotExposeProviderErrorBody(t *testing.T) {
	const sensitiveBody = "provider echoed sensitive test text"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, sensitiveBody)
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateChatCompletion(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	var statusErr *HTTPError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %v, want HTTPError 401", err)
	}
	if strings.Contains(err.Error(), sensitiveBody) {
		t.Fatalf("error exposed provider body: %v", err)
	}
}

func TestCreateChatCompletionEnforcesResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"model":"test-model","padding":"this response is over the limit"}`)
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Model: "test-model", MaxResponseBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateChatCompletion(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("error = %v, want ErrResponseTooLarge", err)
	}
}

func TestCreateChatCompletionEnforcesRequestLimitBeforeSending(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Model: "test-model", MaxRequestBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateChatCompletion(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "this request is too large"}}})
	if !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("error = %v, want ErrRequestTooLarge", err)
	}
	if called {
		t.Fatal("request was sent despite exceeding the configured limit")
	}
}

func TestCreateChatCompletionHonorsContextCancellation(t *testing.T) {
	requestReceived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(requestReceived)
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Model: "test-model", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, requestErr := client.CreateChatCompletion(ctx, ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
		result <- requestErr
	}()
	select {
	case <-requestReceived:
	case <-time.After(time.Second):
		t.Fatal("server did not receive request")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not stop after context cancellation")
	}
}

func TestCreateChatCompletionEnforcesTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Model: "test-model", Timeout: 40 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateChatCompletion(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestNewClientRejectsURLCredentialsAndInvalidBaseURL(t *testing.T) {
	for _, baseURL := range []string{"", "file:///tmp/model", "http://user:pass@example.test/v1", "http://example.test/v1?key=secret", "http://example.test/v1#fragment"} {
		t.Run(baseURL, func(t *testing.T) {
			if _, err := NewClient(Config{BaseURL: baseURL}); err == nil {
				t.Fatalf("NewClient(%q) succeeded, want validation error", baseURL)
			}
		})
	}
}

// busyServer answers with the given statuses in turn, then succeeds.
func busyServer(t *testing.T, statuses []int, retryAfter string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		call := int(calls.Add(1))
		if call <= len(statuses) {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(statuses[call-1])
			_, _ = io.WriteString(w, "busy")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	t.Cleanup(server.Close)
	return server, calls
}

func recordingClient(t *testing.T, baseURL string) (*Client, *[]time.Duration) {
	t.Helper()
	client, err := NewClient(Config{BaseURL: baseURL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	client.sleep = func(ctx context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return ctx.Err()
	}
	return client, &waits
}

func TestCreateChatCompletionRetriesWhenTheProviderIsBusy(t *testing.T) {
	server, calls := busyServer(t, []int{http.StatusTooManyRequests, 529}, "7")
	client, waits := recordingClient(t, server.URL)
	response, err := client.CreateChatCompletion(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if err != nil || len(response.Choices) != 1 || response.Choices[0].Message.Content != "ok" {
		t.Fatalf("CreateChatCompletion = %+v, %v; want the answer after the provider was busy", response, err)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 3", calls.Load())
	}
	if len(*waits) != 2 || (*waits)[0] != 7*time.Second || (*waits)[1] != 7*time.Second {
		t.Fatalf("waits = %v, want Retry-After honored", *waits)
	}
}

func TestCreateChatCompletionBacksOffAndGivesUp(t *testing.T) {
	server, calls := busyServer(t, []int{503, 503, 503, 503, 503}, "")
	client, waits := recordingClient(t, server.URL)
	_, err := client.CreateChatCompletion(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	var statusErr *HTTPError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("error = %v, want the last HTTP 503", err)
	}
	if int(calls.Load()) != defaultMaxRetries+1 || len(*waits) != defaultMaxRetries {
		t.Fatalf("calls = %d, waits = %v; want %d retries", calls.Load(), *waits, defaultMaxRetries)
	}
	for i, wait := range *waits {
		ceiling := firstRetryDelay << i
		if wait > ceiling || wait < ceiling*3/4 {
			t.Fatalf("wait %d = %s, want close to %s", i, wait, ceiling)
		}
	}
	if strings.Contains(err.Error(), "busy") {
		t.Fatalf("error exposed the provider body: %v", err)
	}
}

func TestCreateChatCompletionDoesNotRetryRejections(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden} {
		server, calls := busyServer(t, []int{status}, "1")
		client, waits := recordingClient(t, server.URL)
		_, err := client.CreateChatCompletion(context.Background(), ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
		var statusErr *HTTPError
		if !errors.As(err, &statusErr) || statusErr.StatusCode != status || calls.Load() != 1 || len(*waits) != 0 {
			t.Fatalf("status %d: error = %v, calls = %d, waits = %v; want one call and no retry", status, err, calls.Load(), *waits)
		}
	}
}

func TestCreateChatCompletionStopsWaitingWhenCanceled(t *testing.T) {
	server, calls := busyServer(t, []int{http.StatusTooManyRequests}, "30")
	client, err := NewClient(Config{BaseURL: server.URL, Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	client.sleep = func(ctx context.Context, delay time.Duration) error {
		cancel()
		return sleepContext(ctx, delay)
	}
	_, err = client.CreateChatCompletion(ctx, ChatRequest{Messages: []ChatMessage{{Role: "user", Content: "hi"}}})
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("error = %v, calls = %d; want the wait cut short by cancellation", err, calls.Load())
	}
}

func TestRetryDelayCapsTheProvidersAsk(t *testing.T) {
	now := time.Date(2026, 11, 15, 4, 0, 0, 0, time.UTC)
	header := http.Header{}
	header.Set("Retry-After", "3600")
	if got := retryDelay(header, 0, now); got != maxRetryDelay {
		t.Fatalf("delay = %s, want the %s cap", got, maxRetryDelay)
	}
	header.Set("Retry-After", now.Add(5*time.Second).Format(http.TimeFormat))
	if got := retryDelay(header, 0, now); got != 5*time.Second {
		t.Fatalf("delay = %s, want 5s from an HTTP date", got)
	}
}
