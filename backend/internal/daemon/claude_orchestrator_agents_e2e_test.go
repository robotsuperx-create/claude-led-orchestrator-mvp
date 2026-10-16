package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	worktreesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/worktree"
)

// e2eAgents plays AO's session and report services: Spawn checks out the
// requested branch in a new linked worktree, like AO's gitworktree adapter,
// and each turn writes greet.txt and files an `ao report --done`.
type e2eAgents struct {
	t       *testing.T
	repo    string
	root    string
	mu      sync.Mutex
	spawned []ports.SpawnConfig
	sent    []string
	paths   map[domain.SessionID]string
	reports []domain.ReportRecord
}

func (a *e2eAgents) turn(id domain.SessionID, content string) {
	if err := os.WriteFile(filepath.Join(a.paths[id], "greet.txt"), []byte(content), 0o644); err != nil {
		a.t.Error(err)
	}
	a.reports = append(a.reports, domain.ReportRecord{
		ID: fmt.Sprintf("rpt-%d", len(a.reports)+1), SessionID: id, ProjectID: "proj-greeter",
		State: domain.ReportDone, Note: "wrote greet.txt", CreatedAt: time.Now(),
	})
}

func (a *e2eAgents) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.spawned = append(a.spawned, cfg)
	id := domain.SessionID(fmt.Sprintf("greeter-%d", len(a.spawned)))
	path := filepath.Join(a.root, string(id))
	e2eGit(a.t, a.repo, "worktree", "add", "--quiet", path, cfg.Branch)
	a.paths[id] = path
	a.turn(id, "goodbye\n") // the first attempt is wrong
	return a.sessionLocked(id, cfg.Branch), 0, 0, nil
}

func (a *e2eAgents) sessionLocked(id domain.SessionID, branch string) domain.Session {
	session := domain.Session{}
	session.ID = id
	session.Activity = domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: time.Now()}
	session.Metadata.WorkspacePath = a.paths[id]
	session.Metadata.Branch = branch
	return session
}

func (a *e2eAgents) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.paths[id]; !ok {
		return domain.Session{}, errors.New("unknown session")
	}
	return a.sessionLocked(id, a.spawned[0].Branch), nil
}

func (a *e2eAgents) Send(_ context.Context, id domain.SessionID, message string, _ *ports.SpawnAttachment) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, message)
	a.turn(id, "hello world\n")
	return nil
}

func (a *e2eAgents) Kill(context.Context, domain.SessionID) (bool, error) { return true, nil }

func (a *e2eAgents) ListProject(_ context.Context, projectID domain.ProjectID) ([]domain.ReportRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if projectID != "proj-greeter" {
		return nil, nil
	}
	return append([]domain.ReportRecord(nil), a.reports...), nil
}

type e2eProjects struct{ path string }

func (p e2eProjects) List(context.Context) ([]projectsvc.Summary, error) {
	return []projectsvc.Summary{
		{ID: "proj-other", Path: filepath.Join(p.path, "elsewhere"), Kind: domain.ProjectKindSingleRepo},
		{ID: "proj-greeter", Path: p.path, Kind: domain.ProjectKindSingleRepo},
	}, nil
}

// TestClaudeOrchestratorAgentsModeEndToEnd drives agents mode through the
// production composition: Claude plans over HTTP, each subtask runs in an AO
// agent session on its own branch, the agent's commits are fast-forwarded
// into the run worktree, the validator's failure goes back to the same agent,
// and Claude reviews. No DeepSeek API is configured.
func TestClaudeOrchestratorAgentsModeEndToEnd(t *testing.T) {
	for _, tool := range []string{"git", "grep", "test"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("# greeter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, project, "init", "--quiet")
	e2eGit(t, project, "add", "--all")
	e2eGit(t, project, "-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "--quiet", "-m", "initial")
	mainHead := e2eGit(t, project, "rev-parse", "HEAD")

	claude := &fakeProvider{t: t, answer: func(system, _ string, _ int) string {
		if strings.Contains(system, "execution plan") {
			return `{"summary":"Add greeting file","subtasks":[{"id":"greet","title":"Create greet.txt",` +
				`"instructions":"Create greet.txt containing exactly: hello world","worker_id":"coder","provider":"deepseek"}]}`
		}
		return `{"decision":"approve","summary":"greet.txt is correct","issues":[]}`
	}}
	claudeServer := httptest.NewServer(claude)
	t.Cleanup(claudeServer.Close)

	cfg := config.Config{
		DataDir: filepath.Join(t.TempDir(), "ao"),
		ClaudeOrchestrator: config.ClaudeOrchestratorConfig{
			FeatureEnabled: true,
			ClaudeProvider: modelgateway.ProviderConfig{Provider: ports.ModelProviderClaude, BaseURL: claudeServer.URL, DefaultModel: "claude-test"},
			Worker: config.ClaudeOrchestratorWorkerConfig{
				ProjectRoot:     project,
				DefaultProvider: ports.ModelProviderDeepSeek,
				Mode:            config.ClaudeOrchestratorWorkerModeAgents,
				AgentHarnesses: map[ports.ModelProvider]domain.AgentHarness{
					ports.ModelProviderClaude:   domain.HarnessClaudeCode,
					ports.ModelProviderDeepSeek: domain.HarnessDeepSeek,
				},
				Commands: []config.ClaudeOrchestratorCommandConfig{{Name: "exists", Argv: []string{"test", "-f", "greet.txt"}}},
				Timeout:  30 * time.Second,
			},
			Validator: config.ClaudeOrchestratorValidatorConfig{
				Commands: []config.ClaudeOrchestratorCommandConfig{{Name: "content", Argv: []string{"grep", "-qx", "hello world", "greet.txt"}}},
				Timeout:  30 * time.Second,
			},
		},
	}
	if err := validateClaudeOrchestratorConfig(cfg.ClaudeOrchestrator); err != nil {
		t.Fatalf("agents mode should not need DeepSeek API settings: %v", err)
	}
	wiring, err := newClaudeOrchestratorWiring(cfg, claudeOrchestratorBuildDeps{
		Worktrees: worktreesvc.NewManagerWithManagedRoot(nil, claudeOrchestratorManagedRoot(cfg)),
	})
	if err != nil {
		t.Fatalf("newClaudeOrchestratorWiring: %v", err)
	}
	sessionsRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agents := &e2eAgents{t: t, repo: project, root: sessionsRoot, paths: map[domain.SessionID]string{}}
	if err := wiring.bindAgentSessions(cfg.ClaudeOrchestrator, agents, agents, e2eProjects{path: project}); err != nil {
		t.Fatalf("bindAgentSessions: %v", err)
	}
	router := httpd.NewRouterWithControl(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{ClaudeOrchestrator: wiring.service}, httpd.ControlDeps{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	info, err := http.Get(server.URL + "/internal/claude-orchestrator")
	if err != nil {
		t.Fatal(err)
	}
	var described httpd.ClaudeOrchestratorInfo
	_ = json.NewDecoder(info.Body).Decode(&described)
	_ = info.Body.Close()
	if described.WorkerMode != "agents" || described.WorkerModel != "deepseek-harness" {
		t.Fatalf("info = %+v, want agents mode with the DeepSeek harness", described)
	}

	response, err := http.Post(server.URL+"/internal/claude-orchestrator/runs", "application/json",
		bytes.NewBufferString(`{"task":"Add a greeting file","explicitOptIn":true,"maxRetries":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var started httpd.ClaudeOrchestratorRunResponse
	if err := json.NewDecoder(response.Body).Decode(&started); err != nil || response.StatusCode != http.StatusAccepted {
		t.Fatalf("start = %d, %+v, %v", response.StatusCode, started, err)
	}
	_ = response.Body.Close()

	var final httpd.ClaudeOrchestratorRunResponse
	deadline := time.Now().Add(60 * time.Second)
	for {
		poll, err := http.Get(server.URL + "/internal/claude-orchestrator/runs/" + started.RunID)
		if err != nil {
			t.Fatal(err)
		}
		final = httpd.ClaudeOrchestratorRunResponse{}
		_ = json.NewDecoder(poll.Body).Decode(&final)
		_ = poll.Body.Close()
		if final.State == ports.RunStateHeld || final.State == ports.RunStateFailed || final.State == ports.RunStateCanceled ||
			(final.State == ports.RunStateCompleted && final.Commit != "") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %+v", final)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if final.State != ports.RunStateCompleted || final.Recommendation != ports.MergeOutcomeMerge {
		t.Fatalf("final status = %+v, want completed with a merge recommendation", final)
	}
	if got := e2eGit(t, project, "show", final.Commit+":greet.txt"); got != "hello world" {
		t.Fatalf("committed greet.txt = %q, want the agent's retried content", got)
	}
	if e2eGit(t, project, "rev-parse", final.Branch) != final.Commit {
		t.Fatalf("run branch %s does not point at the reported commit", final.Branch)
	}
	if head := e2eGit(t, project, "rev-parse", "HEAD"); head != mainHead {
		t.Fatal("the run moved the main checkout")
	}

	// One DeepSeek agent session did both attempts; the retry carried the
	// validator's failure.
	if len(agents.spawned) != 1 {
		t.Fatalf("spawned %d sessions, want one", len(agents.spawned))
	}
	spawn := agents.spawned[0]
	if spawn.Harness != domain.HarnessDeepSeek || spawn.ProjectID != "proj-greeter" || spawn.Kind != domain.KindWorker {
		t.Fatalf("spawn = %+v", spawn)
	}
	if spawn.Branch != "ao/claude-orchestrator/"+started.RunID+"--greet" {
		t.Fatalf("agent branch = %q", spawn.Branch)
	}
	if len(agents.sent) != 1 || !strings.Contains(agents.sent[0], "content") {
		t.Fatalf("retry messages = %q, want the validator's failure", agents.sent)
	}
	for _, input := range claude.calls() {
		if strings.Contains(input, cfg.DataDir) || strings.Contains(input, project) || strings.Contains(input, sessionsRoot) {
			t.Fatalf("a local path was sent to Claude: %s", input)
		}
	}
}
