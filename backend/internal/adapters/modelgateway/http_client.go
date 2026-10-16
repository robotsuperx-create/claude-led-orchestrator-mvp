package modelgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout          = 30 * time.Second
	defaultMaxRequestBytes  = 1 << 20
	defaultMaxResponseBytes = 4 << 20
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
	}, nil
}

// ChatMessage is one message in a chat-completions request. Content is a
// string for portable text-only compatibility across OpenAI-compatible APIs.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return empty, errors.New("model gateway request could not be created")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return empty, fmt.Errorf("model gateway request interrupted: %w", ctxErr)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return empty, fmt.Errorf("model gateway request timed out: %w", context.DeadlineExceeded)
		}
		if errors.Is(err, context.Canceled) {
			return empty, fmt.Errorf("model gateway request interrupted: %w", context.Canceled)
		}
		return empty, ErrRequestFailed
	}
	defer func() { _ = response.Body.Close() }()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if err != nil {
		return empty, ErrResponseRead
	}
	if int64(len(responseBody)) > c.maxResponseBytes {
		return empty, ErrResponseTooLarge
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return empty, &HTTPError{StatusCode: response.StatusCode}
	}
	if err := json.Unmarshal(responseBody, &empty); err != nil {
		return ChatResponse{}, ErrResponseDecode
	}
	return empty, nil
}
