package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// liveServiceFake is a run service that also reports live work and a site.
type liveServiceFake struct {
	*claudeOrchestratorAPIFake
	work ports.LiveWork
	site ports.RunSite
}

func (f *liveServiceFake) LiveFor(context.Context, string) (ports.LiveWork, bool) {
	return f.work, true
}

func (f *liveServiceFake) SiteFor(string) (ports.RunSite, bool) {
	return f.site, f.site.Root != ""
}

// The deck sees each step and the code as it lands: bounded, with indentation
// kept, secrets removed, and no path that leaves the worktree.
func TestClaudeOrchestratorAPI_LiveShowsStepsAndCodeSafely(t *testing.T) {
	fake := &liveServiceFake{claudeOrchestratorAPIFake: newClaudeOrchestratorAPIFake()}
	fake.result = ports.OrchestrationResult{State: ports.RunStateCompleted, MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeMerge}}
	fake.work = ports.LiveWork{
		Steps: []ports.LiveStep{
			{Index: 0, ID: "read", Title: "Read the parser", State: ports.LiveStepDone, Attempt: 1},
			{Index: 1, ID: "fix", Title: "Fix the quoting", State: ports.LiveStepRunning, Attempt: 2},
			{Index: 2, ID: "odd", Title: "Unknown", State: "exploded"},
		},
		Files: []ports.LiveFileChange{
			{Path: "parser.go", Added: 2, Removed: 1, Lines: []string{"\tif quoted {", "\t\ttoken := \"sk-ant-api03-" + strings.Repeat("y", 30) + "\""}},
			{Path: "../outside.txt", Added: 1, Lines: []string{"nope"}},
			{Path: "/etc/passwd", Added: 1},
			{Path: "C:/Windows/win.ini", Added: 1},
		},
	}
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	runID := finishedRun(t, api, fake.claudeOrchestratorAPIFake)
	r := newClaudeOrchestratorRouter(api)

	rec := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/"+runID+"/live", "", "127.0.0.1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("live status = %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); strings.Contains(body, "sk-ant-api03") || strings.Contains(body, "outside") || strings.Contains(body, "passwd") || strings.Contains(body, "win.ini") {
		t.Fatalf("live work leaked a secret or a path: %s", body)
	}
	var shown ClaudeOrchestratorLive
	if err := json.Unmarshal(rec.Body.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}
	if len(shown.Steps) != 2 || shown.Steps[1].State != "running" || shown.Steps[1].Attempt != 2 || shown.Steps[1].Title != "Fix the quoting" {
		t.Fatalf("steps = %+v", shown.Steps)
	}
	if len(shown.Files) != 1 || shown.Files[0].Path != "parser.go" || shown.Files[0].Lines[0] != "\tif quoted {" || shown.Files[0].Removed != 1 {
		t.Fatalf("files = %+v", shown.Files)
	}

	rec = serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/"+runID+"/live", "", "127.0.0.1", "https://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a browser origin read live work: %d", rec.Code)
	}
	rec = serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/claude-run-"+strings.Repeat("0", 32)+"/live", "", "127.0.0.1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown run's live work = %d, want 404", rec.Code)
	}
}

// The site route tells the desktop's main process where the run's pages are;
// a browser can never ask.
func TestClaudeOrchestratorAPI_SiteIsForTheMainProcessOnly(t *testing.T) {
	fake := &liveServiceFake{claudeOrchestratorAPIFake: newClaudeOrchestratorAPIFake()}
	fake.result = ports.OrchestrationResult{State: ports.RunStateCompleted, MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeMerge}}
	root := t.TempDir()
	fake.site = ports.RunSite{Root: root, Pages: []string{"index.html", "../up.html", "docs/about.html"}}
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	runID := finishedRun(t, api, fake.claudeOrchestratorAPIFake)
	r := newClaudeOrchestratorRouter(api)

	rec := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/"+runID+"/site", "", "127.0.0.1", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("site = %d %q: %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	var site ClaudeOrchestratorSite
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil {
		t.Fatal(err)
	}
	if site.Root != root || strings.Join(site.Pages, ",") != "index.html,docs/about.html" {
		t.Fatalf("site = %+v", site)
	}
	rec = serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/"+runID+"/site", "", "127.0.0.1", "null")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a page with an opaque origin read the site root: %d", rec.Code)
	}
	fake.site = ports.RunSite{}
	rec = serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/"+runID+"/site", "", "127.0.0.1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a run without pages = %d, want 404", rec.Code)
	}
}
