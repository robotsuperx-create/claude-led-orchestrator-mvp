package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	selectFilesPrompt = "You are a software engineer about to change a repository. " +
		"From repository_files, choose the files you must read to complete the instructions " +
		"(at most 20, only paths that appear in repository_files). " +
		"Return only valid JSON matching {\"paths\":[string]}. Do not include markdown."

	proposeEditsPrompt = "You are a software engineer completing one task in a repository. " +
		"files holds the current content of the files you asked to read; repository_files lists every tracked file. " +
		"If previous_failure is set, your last attempt failed for that reason: fix it. " +
		"Return only valid JSON matching {\"summary\":string,\"edits\":[{\"path\":string,\"content\":string,\"delete\":boolean}]}. " +
		"Each edit replaces the whole file at path (a slash-separated path relative to the repository root) with content, " +
		"or removes it when delete is true. Never edit .git or secret files. Do not include markdown."
)

var _ ports.CodeAuthor = (*ProviderAdapter)(nil)

// SelectFiles asks this provider which repository files it needs to read.
func (a *ProviderAdapter) SelectFiles(ctx context.Context, request ports.CodeAuthorRequest) (ports.FileSelection, error) {
	var result ports.FileSelection
	if err := a.authorCall(ctx, request.Provider, selectFilesPrompt, request, &result); err != nil {
		return ports.FileSelection{}, err
	}
	return result, nil
}

// ProposeEdits asks this provider for whole-file edits that complete the task.
func (a *ProviderAdapter) ProposeEdits(ctx context.Context, request ports.CodeAuthorRequest) (ports.EditProposal, error) {
	var result ports.EditProposal
	if err := a.authorCall(ctx, request.Provider, proposeEditsPrompt, request, &result); err != nil {
		return ports.EditProposal{}, err
	}
	return result, nil
}

func (a *ProviderAdapter) authorCall(ctx context.Context, provider ports.ModelProvider, system string, request ports.CodeAuthorRequest, target any) error {
	if err := a.validateRequestProvider(provider); err != nil {
		return err
	}
	input, err := json.Marshal(request)
	if err != nil {
		return errors.New("code author request could not be encoded")
	}
	chatRequest, err := a.mapRequest(a.config.DefaultModel, []ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: string(input)},
	}, 0)
	if err != nil {
		return err
	}
	response, err := a.client.CreateChatCompletion(ctx, chatRequest)
	if err != nil {
		return err
	}
	content, err := responseContent(response)
	if err != nil {
		return err
	}
	if err := decodeModelJSON(content, target); err != nil {
		return fmt.Errorf("model gateway returned an invalid %s response", a.config.Provider)
	}
	return nil
}

// AuthorRouter dispatches each code-authoring request to the provider named
// by the subtask, falling back to a configured default worker provider.
type AuthorRouter struct {
	registry *Registry
	fallback ports.ModelProvider
}

var _ ports.CodeAuthor = (*AuthorRouter)(nil)

// NewAuthorRouter routes to providers in registry. fallback must be one of
// them; it serves subtasks whose plan named no provider.
func NewAuthorRouter(registry *Registry, fallback ports.ModelProvider) (*AuthorRouter, error) {
	if registry == nil {
		return nil, errors.New("author router requires a provider registry")
	}
	if _, err := registry.Provider(fallback); err != nil {
		return nil, fmt.Errorf("author router fallback provider: %w", err)
	}
	return &AuthorRouter{registry: registry, fallback: fallback}, nil
}

// SelectFiles implements ports.CodeAuthor.
func (r *AuthorRouter) SelectFiles(ctx context.Context, request ports.CodeAuthorRequest) (ports.FileSelection, error) {
	adapter, request, err := r.route(request)
	if err != nil {
		return ports.FileSelection{}, err
	}
	return adapter.SelectFiles(ctx, request)
}

// ProposeEdits implements ports.CodeAuthor.
func (r *AuthorRouter) ProposeEdits(ctx context.Context, request ports.CodeAuthorRequest) (ports.EditProposal, error) {
	adapter, request, err := r.route(request)
	if err != nil {
		return ports.EditProposal{}, err
	}
	return adapter.ProposeEdits(ctx, request)
}

func (r *AuthorRouter) route(request ports.CodeAuthorRequest) (*ProviderAdapter, ports.CodeAuthorRequest, error) {
	if r == nil || r.registry == nil {
		return nil, request, errors.New("author router is unavailable")
	}
	provider := request.Provider
	if provider == "" {
		provider = r.fallback
	}
	adapter, err := r.registry.Provider(provider)
	if err != nil {
		// A plan naming an unconfigured provider is served by the fallback
		// rather than failing the subtask: the plan is model output.
		adapter, err = r.registry.Provider(r.fallback)
		if err != nil {
			return nil, request, err
		}
		provider = r.fallback
	}
	request.Provider = provider
	return adapter, request, nil
}

// decodeModelJSON decodes a model's JSON answer. Models frequently wrap JSON
// in a Markdown code fence despite instructions, so one surrounding fence is
// tolerated; anything else must be the JSON value itself.
func decodeModelJSON(content string, target any) error {
	text := strings.TrimSpace(content)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```")
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			// Drop an optional language tag such as "json".
			if tag := strings.TrimSpace(text[:newline]); tag == "" || !strings.ContainsAny(tag, "{[\"") {
				text = text[newline+1:]
			}
		}
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "```"))
	}
	return json.Unmarshal([]byte(text), target)
}
