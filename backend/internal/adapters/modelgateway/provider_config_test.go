package modelgateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNewRegistryRegistersClaudeAndDeepSeekFromExplicitConfig(t *testing.T) {
	registry, err := NewRegistry(
		ProviderConfig{Provider: ports.ModelProviderClaude, BaseURL: "https://claude.example.invalid/v1", DefaultModel: "claude-custom"},
		ProviderConfig{Provider: ports.ModelProviderDeepSeek, BaseURL: "https://deepseek.example.invalid/v1", DefaultModel: "deepseek-custom"},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if len(registry.Providers()) != 2 {
		t.Fatalf("registered providers = %v, want two", registry.Providers())
	}
	for _, provider := range []ports.ModelProvider{ports.ModelProviderClaude, ports.ModelProviderDeepSeek} {
		adapter, err := registry.Provider(provider)
		if err != nil {
			t.Fatalf("Provider(%q): %v", provider, err)
		}
		request, err := adapter.MapChatRequest("", []ChatMessage{{Role: "user", Content: "offline"}}, 0)
		if err != nil {
			t.Fatalf("MapChatRequest(%q): %v", provider, err)
		}
		wantModel := "claude-custom"
		if provider == ports.ModelProviderDeepSeek {
			wantModel = "deepseek-custom"
		}
		if request.Model != wantModel {
			t.Errorf("default model for %q = %q, want %q", provider, request.Model, wantModel)
		}
		if request.MaxTokens == nil || *request.MaxTokens != DefaultProviderMaxTokens {
			t.Errorf("default max tokens for %q = %v, want %d", provider, request.MaxTokens, DefaultProviderMaxTokens)
		}
	}
}

func TestNewRegistryRequiresCallerSuppliedBaseURLAndModel(t *testing.T) {
	for _, config := range []ProviderConfig{
		{Provider: ports.ModelProviderClaude, DefaultModel: "configured-model"},
		{Provider: ports.ModelProviderDeepSeek, BaseURL: "https://provider.example.invalid/v1"},
	} {
		if _, err := NewRegistry(config); err == nil {
			t.Fatalf("NewRegistry(%s) succeeded, want validation error", config)
		}
	}
}

func TestNewRegistryValidatesProviderAndDuplicates(t *testing.T) {
	base := ProviderConfig{BaseURL: "https://provider.example.invalid/v1", DefaultModel: "model"}
	unsupported := base
	unsupported.Provider = ports.ModelProviderOpenAI
	if _, err := NewRegistry(unsupported); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("unsupported provider error = %v, want ErrUnsupportedProvider", err)
	}
	claude := base
	claude.Provider = ports.ModelProviderClaude
	if _, err := NewRegistry(claude, claude); err == nil {
		t.Fatal("duplicate provider configs succeeded, want error")
	}
	if _, err := NewRegistry(); err == nil {
		t.Fatal("empty registry succeeded, want error")
	}
}

func TestProviderAdapterMapPlanRequest(t *testing.T) {
	adapter := newTestAdapter(t, ProviderConfig{
		Provider: ports.ModelProviderClaude, BaseURL: "https://claude.example.invalid/v1",
		DefaultModel: "custom-plan-model", MaxTokens: 700, Timeout: 5 * time.Second,
	})
	request, err := adapter.MapPlanRequest(ports.PlanRequest{
		Provider: ports.ModelProviderClaude, Task: "build a feature", MemoryContext: "use Go",
	})
	if err != nil {
		t.Fatalf("MapPlanRequest: %v", err)
	}
	if request.Model != "custom-plan-model" || request.MaxTokens == nil || *request.MaxTokens != 700 {
		t.Fatalf("mapped model/token limit = %q/%v, want custom-plan-model/700", request.Model, request.MaxTokens)
	}
	if len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[1].Role != "user" {
		t.Fatalf("mapped messages = %+v, want system and user", request.Messages)
	}
	if !strings.Contains(request.Messages[1].Content, `"task":"build a feature"`) || !strings.Contains(request.Messages[1].Content, `"memory_context":"use Go"`) {
		t.Fatalf("user content does not contain serialized task/context: %s", request.Messages[1].Content)
	}
	if !strings.Contains(request.Messages[0].Content, `"subtasks"`) {
		t.Fatalf("system prompt does not require plan JSON: %s", request.Messages[0].Content)
	}
}

func TestProviderAdapterMapReviewRequest(t *testing.T) {
	adapter := newTestAdapter(t, ProviderConfig{
		Provider: ports.ModelProviderDeepSeek, BaseURL: "https://deepseek.example.invalid/v1",
		DefaultModel: "review-model", MaxTokens: 900,
	})
	request, err := adapter.MapReviewRequest(ports.ReviewRequest{
		Provider:   ports.ModelProviderDeepSeek,
		Task:       "review this",
		Validation: ports.ValidationReport{Passed: false, Issues: []string{"missing test"}},
	})
	if err != nil {
		t.Fatalf("MapReviewRequest: %v", err)
	}
	if request.Model != "review-model" || request.MaxTokens == nil || *request.MaxTokens != 900 {
		t.Fatalf("mapped model/token limit = %q/%v, want review-model/900", request.Model, request.MaxTokens)
	}
	if !strings.Contains(request.Messages[1].Content, `"Task":"review this"`) || !strings.Contains(request.Messages[1].Content, `"Passed":false`) {
		t.Fatalf("review user message missing request fields: %s", request.Messages[1].Content)
	}
}

func TestProviderAdapterValidatesModelAndTokenLimit(t *testing.T) {
	adapter := newTestAdapter(t, ProviderConfig{
		Provider: ports.ModelProviderClaude, BaseURL: "https://claude.example.invalid/v1",
		DefaultModel: "claude-default", AllowedModels: []string{"claude-default", "claude-alt"}, MaxTokens: 128,
	})
	request, err := adapter.MapChatRequest("claude-alt", []ChatMessage{{Role: "user", Content: "hi"}}, 128)
	if err != nil {
		t.Fatalf("MapChatRequest with allowed override: %v", err)
	}
	if request.Model != "claude-alt" || *request.MaxTokens != 128 {
		t.Fatalf("request = %+v, want alternate model and token cap", request)
	}
	for _, model := range []string{"unknown-model", "\ninvalid", "  "} {
		if _, err := adapter.MapChatRequest(model, nil, 1); !errors.Is(err, ErrInvalidModel) {
			t.Errorf("MapChatRequest(model %q) error = %v, want ErrInvalidModel", model, err)
		}
	}
	for _, tokens := range []int{-1, 129} {
		if _, err := adapter.MapChatRequest("", nil, tokens); !errors.Is(err, ErrTokenLimitExceeded) {
			t.Errorf("MapChatRequest(tokens %d) error = %v, want ErrTokenLimitExceeded", tokens, err)
		}
	}
}

func TestProviderAdapterValidatesProviderSelection(t *testing.T) {
	adapter := newTestAdapter(t, ProviderConfig{
		Provider: ports.ModelProviderClaude, BaseURL: "https://claude.example.invalid/v1", DefaultModel: "claude-default",
	})
	if _, err := adapter.MapPlanRequest(ports.PlanRequest{Provider: ports.ModelProviderDeepSeek}); !errors.Is(err, ErrProviderMismatch) {
		t.Fatalf("provider mismatch error = %v, want ErrProviderMismatch", err)
	}
	if _, err := adapter.MapReviewRequest(ports.ReviewRequest{Provider: ports.ModelProviderOpenAI}); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("unsupported request provider error = %v, want ErrUnsupportedProvider", err)
	}
	if _, err := adapter.ValidateModel("claude-default"); err != nil {
		t.Fatalf("ValidateModel(default): %v", err)
	}
}

func TestProviderConfigRedactsAPIKey(t *testing.T) {
	const secret = "do-not-log-this-api-key"
	config := ProviderConfig{
		Provider: ports.ModelProviderClaude, BaseURL: "https://claude.example.invalid/v1",
		APIKey: secret, DefaultModel: "claude-default",
	}
	for label, formatted := range map[string]string{
		"String":  config.String(),
		"fmt %v":  fmt.Sprintf("%v", config),
		"fmt %#v": fmt.Sprintf("%#v", config),
	} {
		if strings.Contains(formatted, secret) {
			t.Errorf("%s exposed API key: %s", label, formatted)
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "APIKey") {
		t.Errorf("JSON representation exposed API key: %s", encoded)
	}
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))
	logger.Info("provider configured", "provider", config)
	if strings.Contains(logOutput.String(), secret) {
		t.Errorf("structured log exposed API key: %s", logOutput.String())
	}
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	adapter, err := registry.Provider(ports.ModelProviderClaude)
	if err != nil {
		t.Fatalf("Provider: %v", err)
	}
	for label, formatted := range map[string]string{
		"adapter fmt %#v": fmt.Sprintf("%#v", adapter),
		"client fmt %#v":  fmt.Sprintf("%#v", adapter.client),
	} {
		if strings.Contains(formatted, secret) {
			t.Errorf("%s exposed API key: %s", label, formatted)
		}
	}
	logOutput.Reset()
	logger.Info("adapter configured", "adapter", adapter, "client", adapter.client)
	if strings.Contains(logOutput.String(), secret) {
		t.Errorf("adapter/client structured log exposed API key: %s", logOutput.String())
	}
}

func newTestAdapter(t *testing.T, config ProviderConfig) *ProviderAdapter {
	t.Helper()
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	adapter, err := registry.Provider(config.Provider)
	if err != nil {
		t.Fatalf("Provider(%q): %v", config.Provider, err)
	}
	return adapter
}
