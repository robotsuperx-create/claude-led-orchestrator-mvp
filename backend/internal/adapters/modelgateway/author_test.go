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
	server, body := chatServer(t, "```json\n"+`{"summary":"add greeting","edits":[{"path":"greet.go","content":"package main\n"},{"path":"old.go","delete":true},{"path":"main.go","replace":[{"old":"package main","new":"package app"}]}]}`+"\n```")
	adapter := newTestAdapter(t, ProviderConfig{Provider: ports.ModelProviderDeepSeek, BaseURL: server.URL, DefaultModel: "m"})

	proposal, err := adapter.ProposeEdits(context.Background(), ports.CodeAuthorRequest{
		Provider: ports.ModelProviderDeepSeek, Instructions: "add a greeting", Attempt: 2,
		PreviousFailure: "tests failed", RepositoryFiles: []string{"main.go"},
		Files:  []ports.FileSnapshot{{Path: "main.go", Content: "package main"}},
		Skills: []ports.SkillGuidance{{Name: "design-taste", Guidance: "One accent colour."}},
	})
	if err != nil {
		t.Fatalf("ProposeEdits: %v", err)
	}
	if !strings.Contains(*body, `\"skills\":[{\"name\":\"design-taste\",\"guidance\":\"One accent colour.\"}]`) {
		t.Fatalf("the edit request did not carry the subtask's skills: %s", *body)
	}
	if proposal.Summary != "add greeting" || len(proposal.Edits) != 3 ||
		proposal.Edits[0].Path != "greet.go" || proposal.Edits[0].Content != "package main\n" || !proposal.Edits[1].Delete ||
		len(proposal.Edits[2].Replace) != 1 || proposal.Edits[2].Replace[0] != (ports.TextReplacement{Old: "package main", New: "package app"}) {
		t.Fatalf("proposal = %+v, want decoded edits from a fenced JSON answer", proposal)
	}
	if !strings.Contains(*body, "use replace") {
		t.Fatalf("the system prompt does not ask for targeted replacements: %s", *body)
	}
	for _, want := range []string{`\"instructions\":\"add a greeting\"`, `\"previous_failure\":\"tests failed\"`, `\"attempt\":2`, `\"files\":[{\"path\":\"main.go\"`} {
		if !strings.Contains(*body, want) {
			t.Fatalf("provider request missing %s: %s", want, *body)
		}
	}
	// The listing was only needed to choose files; writing does not resend it.
	if strings.Contains(*body, `\"repository_files\"`) {
		t.Fatalf("the edit request resent the repository listing: %s", *body)
	}
	if strings.Contains(*body, `\"Provider\"`) || strings.Contains(*body, `\"provider\":\"deepseek\"`) {
		t.Fatalf("routing field leaked into the model input: %s", *body)
	}
}

// Choosing files sees the whole repository grouped by directory, which names
// each directory once.
func TestSelectFilesSendsTheRepositoryGroupedByDirectory(t *testing.T) {
	server, body := chatServer(t, `{"paths":["backend/x/a.go"]}`)
	adapter := newTestAdapter(t, ProviderConfig{Provider: ports.ModelProviderDeepSeek, BaseURL: server.URL, DefaultModel: "m"})
	selection, err := adapter.SelectFiles(context.Background(), ports.CodeAuthorRequest{
		Provider: ports.ModelProviderDeepSeek, Instructions: "x",
		RepositoryFiles: []string{"README.md", "backend/x/a.go", "backend/x/b.go", "go.mod"},
		Skills:          []ports.SkillGuidance{{Name: "design-taste", Guidance: "One accent colour."}},
	})
	if err != nil || len(selection.Paths) != 1 || selection.Paths[0] != "backend/x/a.go" {
		t.Fatalf("SelectFiles = %+v, %v", selection, err)
	}
	want := `\"repository_files\":{\".\":[\"README.md\",\"go.mod\"],\"backend/x\":[\"a.go\",\"b.go\"]}`
	if !strings.Contains(*body, want) {
		t.Fatalf("provider request missing %s: %s", want, *body)
	}
	// Skills shape what is written, not which files are read.
	if strings.Contains(*body, "One accent colour.") {
		t.Fatalf("choosing files was sent the skills: %s", *body)
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
