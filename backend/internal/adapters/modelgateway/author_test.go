package modelgateway

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func newAuthorRegistry(t *testing.T, claudeURL, deepSeekURL string) *Registry {
	t.Helper()
	registry, err := NewRegistry(
		ProviderConfig{Provider: ports.ModelProviderClaude, BaseURL: claudeURL, DefaultModel: "claude-m"},
		ProviderConfig{Provider: ports.ModelProviderDeepSeek, BaseURL: deepSeekURL, DefaultModel: "deepseek-m"},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

// Each subtask is written by the provider its plan names; an empty or
// unknown provider falls back to the configured default worker.
func TestAuthorRouterDispatchesByProviderWithFallback(t *testing.T) {
	claudeServer, claudeBody := chatServer(t, `{"paths":["from-claude.go"]}`)
	deepSeekServer, deepSeekBody := chatServer(t, `{"paths":["from-deepseek.go"]}`)
	router, err := NewAuthorRouter(newAuthorRegistry(t, claudeServer.URL, deepSeekServer.URL), ports.ModelProviderDeepSeek)
	if err != nil {
		t.Fatalf("NewAuthorRouter: %v", err)
	}
	for _, tc := range []struct {
		provider ports.ModelProvider
		want     string
	}{
		{ports.ModelProviderClaude, "from-claude.go"},
		{ports.ModelProviderDeepSeek, "from-deepseek.go"},
		{"", "from-deepseek.go"},
		{"gpt-something", "from-deepseek.go"},
	} {
		*claudeBody, *deepSeekBody = "", ""
		selection, err := router.SelectFiles(context.Background(), ports.CodeAuthorRequest{Provider: tc.provider, Instructions: "x"})
		if err != nil {
			t.Fatalf("SelectFiles(%q): %v", tc.provider, err)
		}
		if len(selection.Paths) != 1 || selection.Paths[0] != tc.want {
			t.Fatalf("SelectFiles(%q) = %+v, want %s", tc.provider, selection, tc.want)
		}
	}
	if _, err := NewAuthorRouter(newAuthorRegistry(t, claudeServer.URL, deepSeekServer.URL), "unconfigured"); err == nil {
		t.Fatal("NewAuthorRouter accepted an unconfigured fallback provider")
	}
}

func TestProposeEditsSendsTaskContextAndDecodesEdits(t *testing.T) {
	server, body := chatServer(t, "```json\n"+`{"summary":"add greeting","edits":[{"path":"greet.go","content":"package main\n"},{"path":"old.go","delete":true}]}`+"\n```")
	adapter := newTestAdapter(t, ProviderConfig{Provider: ports.ModelProviderDeepSeek, BaseURL: server.URL, DefaultModel: "m"})

	proposal, err := adapter.ProposeEdits(context.Background(), ports.CodeAuthorRequest{
		Provider: ports.ModelProviderDeepSeek, Instructions: "add a greeting", Attempt: 2,
		PreviousFailure: "tests failed", RepositoryFiles: []string{"main.go"},
		Files: []ports.FileSnapshot{{Path: "main.go", Content: "package main"}},
	})
	if err != nil {
		t.Fatalf("ProposeEdits: %v", err)
	}
	if proposal.Summary != "add greeting" || len(proposal.Edits) != 2 ||
		proposal.Edits[0].Path != "greet.go" || proposal.Edits[0].Content != "package main\n" || !proposal.Edits[1].Delete {
		t.Fatalf("proposal = %+v, want decoded edits from a fenced JSON answer", proposal)
	}
	for _, want := range []string{`\"instructions\":\"add a greeting\"`, `\"previous_failure\":\"tests failed\"`, `\"repository_files\":[\"main.go\"]`, `\"attempt\":2`} {
		if !strings.Contains(*body, want) {
			t.Fatalf("provider request missing %s: %s", want, *body)
		}
	}
	if strings.Contains(*body, `\"Provider\"`) || strings.Contains(*body, `\"provider\":\"deepseek\"`) {
		t.Fatalf("routing field leaked into the model input: %s", *body)
	}
}

func TestDecodeModelJSONToleratesOneFenceOnly(t *testing.T) {
	var target struct {
		A int `json:"a"`
	}
	for _, input := range []string{`{"a":1}`, "  {\"a\":1}\n", "```json\n{\"a\":1}\n```", "```\n{\"a\":1}\n```"} {
		target.A = 0
		if err := decodeModelJSON(input, &target); err != nil || target.A != 1 {
			t.Errorf("decodeModelJSON(%q) = %d, %v; want 1", input, target.A, err)
		}
	}
	for _, input := range []string{"Here you go: {\"a\":1}", "", "```json\nnot json\n```"} {
		if err := decodeModelJSON(input, &target); err == nil {
			t.Errorf("decodeModelJSON(%q) succeeded, want error", input)
		}
	}
}
