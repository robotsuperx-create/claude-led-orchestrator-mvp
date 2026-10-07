package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// DefaultProviderTimeout is used when a caller leaves Timeout unset.
	DefaultProviderTimeout = 30 * time.Second
	// DefaultProviderMaxTokens caps generated output when MaxTokens is unset.
	DefaultProviderMaxTokens = 4096
)

// Errors returned by the provider registry and adapters.
var (
	ErrUnsupportedProvider   = errors.New("unsupported model provider")
	ErrProviderNotConfigured = errors.New("model provider is not configured")
	ErrInvalidModel          = errors.New("model name is invalid or not allowed")
	ErrTokenLimitExceeded    = errors.New("requested token limit exceeds provider configuration")
	ErrProviderMismatch      = errors.New("request provider does not match configured provider")
)

// ProviderConfig describes one explicitly configured provider endpoint. BaseURL
// is required and is never populated from environment variables or defaults.
// The endpoint must implement the OpenAI-compatible chat-completions API used by
// Client (including for Claude-compatible gateways). APIKey is never serialized
// or included in this type's formatted/logged representations.
type ProviderConfig struct {
	Provider     ports.ModelProvider `json:"provider"`
	BaseURL      string              `json:"base_url"`
	APIKey       string              `json:"-"`
	DefaultModel string              `json:"default_model"`
	// AllowedModels, when non-empty, restricts caller-selected model names.
	AllowedModels []string      `json:"allowed_models,omitempty"`
	Timeout       time.Duration `json:"timeout,omitempty"`
	// MaxTokens is the maximum output-token budget for a single call.
	MaxTokens int `json:"max_tokens,omitempty"`
	// HTTPClient permits caller-supplied transports; its timeout is set from Timeout.
	HTTPClient *http.Client `json:"-"`
}

// String intentionally omits APIKey so ordinary fmt/log calls cannot disclose it.
func (c ProviderConfig) String() string {
	return fmt.Sprintf("ProviderConfig{Provider:%q, BaseURL:%q, DefaultModel:%q, AllowedModels:%v, Timeout:%s, MaxTokens:%d}",
		c.Provider, c.BaseURL, c.DefaultModel, c.AllowedModels, c.Timeout, c.MaxTokens)
}

// GoString keeps %#v formatting redacted as well.
func (c ProviderConfig) GoString() string { return c.String() }

// LogValue provides a safe structured-logging representation.
func (c ProviderConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("provider", string(c.Provider)),
		slog.String("base_url", c.BaseURL),
		slog.String("default_model", c.DefaultModel),
		slog.Any("allowed_models", c.AllowedModels),
		slog.Duration("timeout", c.Timeout),
		slog.Int("max_tokens", c.MaxTokens),
	)
}

// String intentionally omits credentials if a client is formatted for logging.
func (c *Client) String() string {
	if c == nil {
		return "ModelGatewayClient<nil>"
	}
	return fmt.Sprintf("ModelGatewayClient{Model:%q}", c.model)
}

// GoString keeps %#v formatting redacted for clients as well.
func (c *Client) GoString() string { return c.String() }

// LogValue prevents structured logging from exposing the client's API key.
func (c *Client) LogValue() slog.Value {
	if c == nil {
		return slog.StringValue("ModelGatewayClient<nil>")
	}
	return slog.GroupValue(slog.String("model", c.model))
}

// Registry contains only providers supplied by its caller. A base URL is
// required for each config; there are no built-in endpoints or env lookups.
type Registry struct {
	providers map[ports.ModelProvider]*ProviderAdapter
}

// ProviderAdapter validates provider/model selection, maps the orchestration
// ports to portable chat-completions messages, and calls the configured client.
type ProviderAdapter struct {
	config ProviderConfig
	client *Client
}

// String intentionally reports only non-secret adapter metadata.
func (a *ProviderAdapter) String() string {
	if a == nil {
		return "ProviderAdapter<nil>"
	}
	return fmt.Sprintf("ProviderAdapter{Provider:%q, DefaultModel:%q}", a.config.Provider, a.config.DefaultModel)
}

// GoString keeps %#v formatting redacted for adapters as well.
func (a *ProviderAdapter) GoString() string { return a.String() }

// LogValue is safe for slog even though the adapter retains API credentials.
func (a *ProviderAdapter) LogValue() slog.Value {
	if a == nil {
		return slog.StringValue("ProviderAdapter<nil>")
	}
	return slog.GroupValue(
		slog.String("provider", string(a.config.Provider)),
		slog.String("default_model", a.config.DefaultModel),
	)
}

// NewRegistry validates and registers one or more Claude/DeepSeek-compatible
// providers. It does not read environment variables or make network requests.
func NewRegistry(configs ...ProviderConfig) (*Registry, error) {
	if len(configs) == 0 {
		return nil, errors.New("at least one model provider must be configured")
	}
	registry := &Registry{providers: make(map[ports.ModelProvider]*ProviderAdapter, len(configs))}
	for _, config := range configs {
		if !supportedProvider(config.Provider) {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedProvider, config.Provider)
		}
		if _, exists := registry.providers[config.Provider]; exists {
			return nil, fmt.Errorf("model provider %q is configured more than once", config.Provider)
		}
		normalized, err := normalizeProviderConfig(config)
		if err != nil {
			return nil, err
		}
		client, err := NewClient(Config{
			BaseURL:          normalized.BaseURL,
			APIKey:           normalized.APIKey,
			Model:            normalized.DefaultModel,
			Timeout:          normalized.Timeout,
			MaxRequestBytes:  defaultMaxRequestBytes,
			MaxResponseBytes: defaultMaxResponseBytes,
			HTTPClient:       normalized.HTTPClient,
		})
		if err != nil {
			// Do not include the config, URL, or key in the error path.
			return nil, fmt.Errorf("invalid configuration for model provider %q", config.Provider)
		}
		registry.providers[config.Provider] = &ProviderAdapter{config: normalized, client: client}
	}
	return registry, nil
}

// Provider returns the adapter for a configured provider.
func (r *Registry) Provider(provider ports.ModelProvider) (*ProviderAdapter, error) {
	if r == nil {
		return nil, ErrProviderNotConfigured
	}
	adapter, ok := r.providers[provider]
	if !ok {
		if !supportedProvider(provider) {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedProvider, provider)
		}
		return nil, fmt.Errorf("%w: %q", ErrProviderNotConfigured, provider)
	}
	return adapter, nil
}

// Providers returns the provider identifiers configured in this registry.
func (r *Registry) Providers() []ports.ModelProvider {
	if r == nil {
		return nil
	}
	result := make([]ports.ModelProvider, 0, len(r.providers))
	for provider := range r.providers {
		result = append(result, provider)
	}
	return result
}

// ValidateModel checks the selected model against this provider's config.
// An empty selection means the configurable default model.
func (a *ProviderAdapter) ValidateModel(model string) (string, error) {
	if a == nil {
		return "", ErrProviderNotConfigured
	}
	if model == "" {
		model = a.config.DefaultModel
	}
	model = strings.TrimSpace(model)
	if model == "" || strings.ContainsAny(model, "\r\n\x00") {
		return "", ErrInvalidModel
	}
	if len(a.config.AllowedModels) > 0 {
		for _, allowed := range a.config.AllowedModels {
			if model == allowed {
				return model, nil
			}
		}
		return "", ErrInvalidModel
	}
	return model, nil
}

// MapPlanRequest converts a PlanRequest into the portable HTTP-client request.
// This method is useful for validation/testing without contacting a provider.
func (a *ProviderAdapter) MapPlanRequest(request ports.PlanRequest) (ChatRequest, error) {
	if err := a.validateRequestProvider(request.Provider); err != nil {
		return ChatRequest{}, err
	}
	input, err := json.Marshal(struct {
		Task          string `json:"task"`
		MemoryContext string `json:"memory_context,omitempty"`
	}{Task: request.Task, MemoryContext: request.MemoryContext})
	if err != nil {
		return ChatRequest{}, errors.New("plan request could not be encoded")
	}
	return a.mapRequest(a.config.DefaultModel, []ChatMessage{
		{Role: "system", Content: "Create a bounded execution plan. Return only valid JSON matching {\"summary\":string,\"subtasks\":[{\"id\":string,\"title\":string,\"instructions\":string,\"worker_id\":string,\"provider\":string}]}. Do not include markdown."},
		{Role: "user", Content: string(input)},
	}, 0)
}

// MapReviewRequest converts a ReviewRequest into the portable HTTP-client request.
func (a *ProviderAdapter) MapReviewRequest(request ports.ReviewRequest) (ChatRequest, error) {
	if err := a.validateRequestProvider(request.Provider); err != nil {
		return ChatRequest{}, err
	}
	input, err := json.Marshal(request)
	if err != nil {
		return ChatRequest{}, errors.New("review request could not be encoded")
	}
	return a.mapRequest(a.config.DefaultModel, []ChatMessage{
		{Role: "system", Content: "Review the supplied execution and validation results. Return only valid JSON matching {\"decision\":\"approve\"|\"request_changes\",\"summary\":string,\"issues\":[string]}. Do not include markdown."},
		{Role: "user", Content: string(input)},
	}, 0)
}

// MapChatRequest validates a model and token budget and builds a portable request.
// A zero token budget selects the provider's configured maximum.
func (a *ProviderAdapter) MapChatRequest(model string, messages []ChatMessage, maxTokens int) (ChatRequest, error) {
	return a.mapRequest(model, messages, maxTokens)
}

func (a *ProviderAdapter) mapRequest(model string, messages []ChatMessage, maxTokens int) (ChatRequest, error) {
	selectedModel, err := a.ValidateModel(model)
	if err != nil {
		return ChatRequest{}, err
	}
	if maxTokens < 0 || (maxTokens > 0 && maxTokens > a.config.MaxTokens) {
		return ChatRequest{}, ErrTokenLimitExceeded
	}
	if maxTokens == 0 {
		maxTokens = a.config.MaxTokens
	}
	return ChatRequest{Model: selectedModel, Messages: append([]ChatMessage(nil), messages...), MaxTokens: &maxTokens}, nil
}

func (a *ProviderAdapter) validateRequestProvider(provider ports.ModelProvider) error {
	if provider == "" || provider == a.config.Provider {
		return nil
	}
	if !supportedProvider(provider) {
		return fmt.Errorf("%w: %q", ErrUnsupportedProvider, provider)
	}
	return ErrProviderMismatch
}

// Plan implements ports.ModelGateway with JSON-only plan decoding.
func (a *ProviderAdapter) Plan(ctx context.Context, request ports.PlanRequest) (ports.ExecutionPlan, error) {
	var result ports.ExecutionPlan
	chatRequest, err := a.MapPlanRequest(request)
	if err != nil {
		return result, err
	}
	response, err := a.client.CreateChatCompletion(ctx, chatRequest)
	if err != nil {
		return result, err
	}
	content, err := responseContent(response)
	if err != nil {
		return result, err
	}
	if err := decodeModelJSON(content, &result); err != nil {
		return ports.ExecutionPlan{}, errors.New("model gateway returned an invalid execution plan")
	}
	return result, nil
}

// Review implements ports.ModelGateway with JSON-only review decoding.
func (a *ProviderAdapter) Review(ctx context.Context, request ports.ReviewRequest) (ports.ReviewDecision, error) {
	var result ports.ReviewDecision
	chatRequest, err := a.MapReviewRequest(request)
	if err != nil {
		return result, err
	}
	response, err := a.client.CreateChatCompletion(ctx, chatRequest)
	if err != nil {
		return result, err
	}
	content, err := responseContent(response)
	if err != nil {
		return result, err
	}
	if err := decodeModelJSON(content, &result); err != nil {
		return ports.ReviewDecision{}, errors.New("model gateway returned an invalid review decision")
	}
	if result.Decision != ports.ReviewOutcomeApprove && result.Decision != ports.ReviewOutcomeRequestChanges {
		return ports.ReviewDecision{}, errors.New("model gateway returned an unsupported review decision")
	}
	return result, nil
}

func responseContent(response ChatResponse) (string, error) {
	if len(response.Choices) == 0 {
		return "", errors.New("model gateway returned no completion choices")
	}
	return response.Choices[0].Message.Content, nil
}

func normalizeProviderConfig(config ProviderConfig) (ProviderConfig, error) {
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	config.DefaultModel = strings.TrimSpace(config.DefaultModel)
	if config.BaseURL == "" {
		return ProviderConfig{}, fmt.Errorf("base URL is required for model provider %q", config.Provider)
	}
	if config.DefaultModel == "" || strings.ContainsAny(config.DefaultModel, "\r\n\x00") {
		return ProviderConfig{}, fmt.Errorf("a valid default model is required for model provider %q", config.Provider)
	}
	if config.Timeout <= 0 {
		config.Timeout = DefaultProviderTimeout
	}
	if config.MaxTokens < 0 {
		return ProviderConfig{}, fmt.Errorf("maximum tokens must not be negative for model provider %q", config.Provider)
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = DefaultProviderMaxTokens
	}
	if config.MaxTokens < 1 {
		return ProviderConfig{}, fmt.Errorf("maximum tokens must be positive for model provider %q", config.Provider)
	}
	allowed := make([]string, 0, len(config.AllowedModels))
	seen := make(map[string]struct{}, len(config.AllowedModels))
	for _, model := range config.AllowedModels {
		model = strings.TrimSpace(model)
		if model == "" || strings.ContainsAny(model, "\r\n\x00") {
			return ProviderConfig{}, fmt.Errorf("allowed model name is invalid for model provider %q", config.Provider)
		}
		if _, exists := seen[model]; !exists {
			allowed = append(allowed, model)
			seen[model] = struct{}{}
		}
	}
	if len(allowed) > 0 {
		if _, ok := seen[config.DefaultModel]; !ok {
			return ProviderConfig{}, fmt.Errorf("default model is not in the allowed model list for provider %q", config.Provider)
		}
	}
	config.AllowedModels = allowed
	return config, nil
}

func supportedProvider(provider ports.ModelProvider) bool {
	return provider == ports.ModelProviderClaude || provider == ports.ModelProviderDeepSeek
}

var _ ports.ModelGateway = (*ProviderAdapter)(nil)
var _ fmt.Stringer = ProviderConfig{}
var _ fmt.GoStringer = ProviderConfig{}
var _ slog.LogValuer = ProviderConfig{}
var _ fmt.Stringer = (*Client)(nil)
var _ fmt.GoStringer = (*Client)(nil)
var _ slog.LogValuer = (*Client)(nil)
var _ fmt.Stringer = (*ProviderAdapter)(nil)
var _ fmt.GoStringer = (*ProviderAdapter)(nil)
var _ slog.LogValuer = (*ProviderAdapter)(nil)
