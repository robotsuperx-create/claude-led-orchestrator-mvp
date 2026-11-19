package modelgateway

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Design-tier subtasks are written by the design model on the same provider;
// everything else stays with the writer model.
func TestDesignTierSubtasksAreWrittenByTheDesignModel(t *testing.T) {
	server, body := chatServer(t, `{"paths":["index.html"]}`)
	writers, err := NewRegistry(ProviderConfig{Provider: ports.ModelProviderClaude, BaseURL: server.URL, DefaultModel: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	designers, err := NewRegistry(ProviderConfig{Provider: ports.ModelProviderClaude, BaseURL: server.URL, DefaultModel: "claude-fable-5-2"})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewAuthorRouter(writers, ports.ModelProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	router = router.WithDesignWriters(designers)
	request := ports.CodeAuthorRequest{Instructions: "x", RepositoryFiles: []string{"index.html"}}

	for tier, want := range map[string]string{"": "claude-opus-5-5", ports.SubtaskTierDesign: "claude-fable-5-2"} {
		request.Tier = tier
		if _, err := router.SelectFiles(context.Background(), request); err != nil {
			t.Fatalf("tier %q: %v", tier, err)
		}
		if !strings.Contains(*body, `"model":"`+want+`"`) {
			t.Fatalf("tier %q asked %s, want model %s", tier, *body, want)
		}
		if strings.Contains(*body, `"tier"`) {
			t.Fatalf("the tier was sent to the model: %s", *body)
		}
	}
}

func TestWithoutADesignModelDesignTierUsesTheWriterModel(t *testing.T) {
	server, body := chatServer(t, `{"paths":[]}`)
	writers, err := NewRegistry(ProviderConfig{Provider: ports.ModelProviderClaude, BaseURL: server.URL, DefaultModel: "claude-opus-5-5"})
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewAuthorRouter(writers, ports.ModelProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.WithDesignWriters(nil).SelectFiles(context.Background(), ports.CodeAuthorRequest{Tier: ports.SubtaskTierDesign}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*body, `"model":"claude-opus-5-5"`) {
		t.Fatalf("asked %s", *body)
	}
}

func TestThePlannerIsToldWhichSubtasksAreDesignWork(t *testing.T) {
	if !strings.Contains(planningSystemPrompt, "tier is \"design\"") || !strings.Contains(planningSystemPrompt, "\"tier\":string") {
		t.Fatal("the planning prompt does not explain the design tier")
	}
}
