package modelgateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTimeout         = 30 * time.Second
	defaultMaxRequestBytes = 1 << 20
	// writerMaxRequestBytes allows a writer's files and up to maxWriterImages
	// screenshots in one request; the provider accepts far more.
	writerMaxRequestBytes   = 16 << 20
	defaultMaxResponseBytes = 4 << 20
)

// Busy providers are asked again, briefly. Several writers sharing one key
// can hit a rate limit together, and a provider can be momentarily overloaded;
// neither should fail a whole subtask. A Retry-After header is honored up to
// maxRetryDelay; otherwise the wait doubles from firstRetryDelay.
const (
	defaultMaxRetries = 3
	firstRetryDelay   = 2 * time.Second
	maxRetryDelay     = 30 * time.Second
)

// Errors returned by Client. Provider response bodies are never included.
var (
	ErrRequestTooLarge  = errors.New("model gateway request body exceeds configured limit")
	ErrResponseTooLarge = errors.New("model gateway response body exceeds configured limit")
	ErrRequestFailed    = errors.New("model gateway request failed")
	ErrResponseRead     = errors.New("model gateway response could not be read")
	ErrResponseDecode   = errors.New("model gateway response is not valid JSON")
)

// Config configures an OpenAI-compatible chat-completions endpoint. BaseURL is
// the API root (for example, https://example.invalid/v1); the client appends
// /chat/completions. HTTPClient may provide a custom transport, but its timeout
// is set from Timeout so every request has a finite deadline.
type Config struct {
	BaseURL          string
	APIKey           string
	Model            string
	Timeout          time.Duration
	MaxRequestBytes  int64
	MaxResponseBytes int64
	HTTPClient       *http.Client
}

// Client sends chat-completions requests using only net/http and encoding/json.
type Client struct {
	endpoint         string
	apiKey           string
	model            string
	httpClient       *http.Client
	maxRequestBytes  int64
	maxResponseBytes int64
	// maxRetries is how many times a busy answer is retried; sleep waits
	// between tries and returns early with the context's error.
	maxRetries int
	sleep      func(context.Context, time.Duration) error
}

// NewClient validates configuration and creates a client. APIKey is optional
// for compatible local or otherwise unauthenticated endpoints.
func NewClient(config Config) (*Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed == nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("model gateway base URL must be an http(s) URL without user info, query, or fragment")
	}
	if strings.ContainsAny(config.APIKey, "\r\n") {
		return nil, errors.New("model gateway API key contains invalid characters")
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	maxRequestBytes := config.MaxRequestBytes
	if maxRequestBytes <= 0 {
		maxRequestBytes = defaultMaxRequestBytes
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultMaxResponseBytes
	}
	if maxResponseBytes == math.MaxInt64 {
		return nil, errors.New("model gateway response body limit is too large")
	}

	httpClient := &http.Client{}
	if config.HTTPClient != nil {
		copyOfClient := *config.HTTPClient
		httpClient = &copyOfClient
	}
	httpClient.Timeout = timeout
	// Do not follow redirects: this avoids forwarding credentials or request
	// bodies to a different endpoint and keeps the configured base URL boundary.
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{
		endpoint:         baseURL + "/chat/completions",
		apiKey:           config.APIKey,
		model:            config.Model,
		httpClient:       httpClient,
		maxRequestBytes:  maxRequestBytes,
		maxResponseBytes: maxResponseBytes,
		maxRetries:       defaultMaxRetries,
		sleep:            sleepContext,
	}, nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryable reports whether a status means "busy, ask again shortly": rate
// limited (429), overloaded (529, Anthropic's), or a passing server fault.
func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return true
	default:
		return false
	}
}

// retryDelay prefers the provider's Retry-After (seconds or an HTTP date),
// capped, and otherwise doubles from firstRetryDelay with a little jitter so
// writers sharing a key do not all come back at once.
func retryDelay(header http.Header, attempt int, now time.Time) time.Duration {
	if value := strings.TrimSpace(header.Get("Retry-After")); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
			return min(time.Duration(seconds)*time.Second, maxRetryDelay)
		}
		if when, err := http.ParseTime(value); err == nil {
			return min(max(when.Sub(now), 0), maxRetryDelay)
		}
	}
	delay := firstRetryDelay << attempt
	if delay <= 0 || delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	return delay - time.Duration(rand.Int64N(int64(delay/4)+1)) //nolint:gosec // Retry jitter is not security-sensitive.
}

// ChatMessage is one message in a chat-completions request. Content is a
// string for portable text-only compatibility across OpenAI-compatible APIs.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Images are JPEG pictures sent after the text, in order. A message with
	// images is sent as content parts, the OpenAI-compatible way.
	Images [][]byte `json:"-"`
}

// MarshalJSON sends a message with images as text and image parts.
func (m ChatMessage) MarshalJSON() ([]byte, error) {
	type plain struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if len(m.Images) == 0 {
		return json.Marshal(plain{Role: m.Role, Content: m.Content})
	}
	type imageURL struct {
		URL string `json:"url"`
	}
	type part struct {
		Type     string    `json:"type"`
		Text     string    `json:"text,omitempty"`
		ImageURL *imageURL `json:"image_url,omitempty"`
	}
	parts := []part{{Type: "text", Text: m.Content}}
	for _, image := range m.Images {
		parts = append(parts, part{Type: "image_url", ImageURL: &imageURL{URL: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(image)}})
	}
	return json.Marshal(struct {
		Role    string `json:"role"`
		Content []part `json:"content"`
	}{Role: m.Role, Content: parts})
}

// UnmarshalJSON reads a reply's message, whose content is text.
func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	var plain struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &plain); err != nil {
		return err
	}
	m.Role, m.Content, m.Images = plain.Role, "", nil
	if len(plain.Content) == 0 || string(plain.Content) == "null" {
		return nil
	}
	if err := json.Unmarshal(plain.Content, &m.Content); err == nil {
		return nil
	}
	// Content parts: keep the text, in order.
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(plain.Content, &parts); err != nil {
		return err
	}
	for _, part := range parts {
		if part.Type == "text" {
			m.Content += part.Text
		}
	}
	return nil
}

// ChatRequest contains the common portable fields accepted by chat-completion
// APIs. Model may be omitted to use the model configured on Client.
type ChatRequest struct {
	Model       string        `json:"model,omitempty"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	TopP        *float64      `json:"top_p,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
}

// ChatResponse is a typed, SDK-independent subset of the OpenAI-compatible
// chat-completions response format.
type ChatResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object,omitempty"`
	Created int64        `json:"created,omitempty"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage,omitempty"`
}

// ChatChoice is a generated completion choice.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

// ChatUsage contains provider-reported token counts when available.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

// HTTPError reports a non-2xx response without exposing provider response
// contents, which may contain credentials or other sensitive data.
type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("model gateway returned HTTP %d", e.StatusCode)
}

// CreateChatCompletion sends a request and returns a typed response. Errors
// intentionally omit URLs, response bodies, and transport error text to avoid
// accidentally disclosing credentials or provider-supplied secrets.
func (c *Client) CreateChatCompletion(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	var empty ChatResponse
	if ctx == nil {
		return empty, errors.New("model gateway context must not be nil")
	}
	if request.Model == "" {
		request.Model = c.model
	}
	if request.Model == "" {
		return empty, errors.New("model gateway model must be configured or provided")
	}
	body, err := json.Marshal(request)
	if err != nil {
		return empty, errors.New("model gateway request could not be encoded")
	}
	if int64(len(body)) > c.maxRequestBytes {
		return empty, ErrRequestTooLarge
	}

	for attempt := 0; ; attempt++ {
		response, retryAfter, err := c.send(ctx, body)
		if err != nil {
			return empty, err
		}
		var status *HTTPError
		if attempt >= c.maxRetries || !errors.As(response.err, &status) || !retryable(status.StatusCode) {
			return response.value, response.err
		}
		if err := c.sleep(ctx, retryDelay(retryAfter, attempt, time.Now())); err != nil {
			return empty, fmt.Errorf("model gateway request interrupted: %w", err)
		}
	}
}

// sendResult is one answer: a response or a provider error such as HTTPError.
type sendResult struct {
	value ChatResponse
	err   error
}

// send makes one request. A transport failure is returned as err; anything the
// provider answered, including an error status, is in the result, with the
// headers needed to decide on a retry.
func (c *Client) send(ctx context.Context, body []byte) (sendResult, http.Header, error) {
	var empty ChatResponse
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return sendResult{}, nil, errors.New("model gateway request could not be created")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return sendResult{}, nil, fmt.Errorf("model gateway request interrupted: %w", ctxErr)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return sendResult{}, nil, fmt.Errorf("model gateway request timed out: %w", context.DeadlineExceeded)
		}
		if errors.Is(err, context.Canceled) {
			return sendResult{}, nil, fmt.Errorf("model gateway request interrupted: %w", context.Canceled)
		}
		return sendResult{}, nil, ErrRequestFailed
	}
	defer func() { _ = response.Body.Close() }()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if err != nil {
		return sendResult{err: ErrResponseRead}, response.Header, nil
	}
	if int64(len(responseBody)) > c.maxResponseBytes {
		return sendResult{err: ErrResponseTooLarge}, response.Header, nil
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return sendResult{err: &HTTPError{StatusCode: response.StatusCode}}, response.Header, nil
	}
	if err := json.Unmarshal(responseBody, &empty); err != nil {
		return sendResult{err: ErrResponseDecode}, response.Header, nil
	}
	return sendResult{value: empty}, response.Header, nil
}
