package claudeorchestrator

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// agentTurn is what the simulated agent does in one turn: it may edit its
// worktree, then file a report and/or settle into an activity state.
type agentTurn struct {
	report   domain.ReportState
	note     string
	activity domain.ActivityState
}

// fakeAgentAPI stands in for the AO session and report services. Spawn
// creates a real linked worktree on the requested branch, as AO does, and the
// test's turn function plays the agent.
type fakeAgentAPI struct {
	t        *testing.T
	project  string
	sessions string
	turn     func(worktree string, turn int) agentTurn

	mu      sync.Mutex
	records map[domain.SessionID]*domain.Session
	turns   map[domain.SessionID]int
	reports []domain.ReportRecord
	spawned []ports.SpawnConfig
	sent    []string
	killed  []domain.SessionID
}

func newFakeAgentAPI(t *testing.T, project string, turn func(string, int) agentTurn) *fakeAgentAPI {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &fakeAgentAPI{
		t: t, project: project, sessions: dir, turn: turn,
		records: map[domain.SessionID]*domain.Session{}, turns: map[domain.SessionID]int{},
	}
}

func (f *fakeAgentAPI) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spawned = append(f.spawned, cfg)
	id := domain.SessionID(fmt.Sprintf("ao-%d", len(f.spawned)))
	path := filepath.Join(f.sessions, string(id))
	runGit(f.t, f.project, "worktree", "add", "--quiet", path, cfg.Branch)
	session := &domain.Session{}
	session.ID = id
	session.ProjectID = cfg.ProjectID
	session.Kind = cfg.Kind
	session.Harness = cfg.Harness
	session.Metadata.Branch = cfg.Branch
	session.Metadata.WorkspacePath = path
	f.records[id] = session
	f.playTurnLocked(id)
	return *session, 0, 0, nil
}

func (f *fakeAgentAPI) playTurnLocked(id domain.SessionID) {
	f.turns[id]++
	session := f.records[id]
	result := f.turn(session.Metadata.WorkspacePath, f.turns[id])
	if result.report != "" {
		f.reports = append(f.reports, domain.ReportRecord{
			ID: fmt.Sprintf("rpt-%d", len(f.reports)+1), SessionID: id, ProjectID: session.ProjectID,
			State: result.report, Note: result.note, CreatedAt: time.Now(),
		})
	}
	if result.activity != "" {
		session.Activity = domain.Activity{State: result.activity, LastActivityAt: time.Now()}
	}
}

func (f *fakeAgentAPI) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.records[id]
	if !ok {
		return domain.Session{}, errors.New("unknown session")
	}
	return *session, nil
}

func (f *fakeAgentAPI) Send(_ context.Context, id domain.SessionID, message string, _ *ports.SpawnAttachment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, message)
	f.playTurnLocked(id)
	return nil
}

func (f *fakeAgentAPI) Kill(_ context.Context, id domain.SessionID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, id)
	if session, ok := f.records[id]; ok {
		session.IsTerminated = true
	}
	return true, nil
}

func (f *fakeAgentAPI) ListProject(_ context.Context, projectID domain.ProjectID) ([]domain.ReportRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ReportRecord
	for _, report := range f.reports {
		if report.ProjectID == projectID {
			out = append(out, report)
		}
	}
	return out, nil
}

func newTestAgentSessions(t *testing.T, api *fakeAgentAPI) *AgentSessions {
	t.Helper()
	agents, err := NewAgentSessions(ExecCommandRunner{}, AgentSessionsConfig{
		Harnesses: map[ports.ModelProvider]domain.AgentHarness{
			ports.ModelProviderDeepSeek: domain.HarnessDeepSeek,
			ports.ModelProviderClaude:   domain.HarnessClaudeCode,
		},
		DefaultProvider: ports.ModelProviderDeepSeek,
		PollInterval:    5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewAgentSessions: %v", err)
	}
	if err := agents.Bind(api, api, func(context.Context) (domain.ProjectID, error) { return "proj-1", nil }); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return agents
}

// newAgentRun returns a project and a real per-run worktree for it.
func newAgentRun(t *testing.T) (project, worktree string) {
	t.Helper()
	project = newGitProject(t)
	workspaces, _ := newTestRunWorkspaces(t, project)
	workspace, err := workspaces.Prepare(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return project, workspace.Path
}

func agentRequest(worktree string, attempt int, failure string) ports.AgentImplementRequest {
	return ports.AgentImplementRequest{
		WorktreePath:    worktree,
		Task:            ports.PlannedSubtask{ID: "sub-1", Title: "Add greeting", Instructions: "write greet.txt", Provider: ports.ModelProviderDeepSeek},
		Attempt:         attempt,
		PreviousFailure: failure,
	}
}

func TestAgentSessionsIntegrateAFinishedAgentsWork(t *testing.T) {
	_, worktree := newAgentRun(t)
	api := newFakeAgentAPI(t, "", func(dir string, _ int) agentTurn {
		mustWrite(t, filepath.Join(dir, "greet.txt"), "hello\n")
		return agentTurn{report: domain.ReportDone, note: "added greet.txt", activity: domain.ActivityWaitingInput}
	})
	api.project = worktree
	agents := newTestAgentSessions(t, api)
	before := runGit(t, worktree, "rev-parse", "HEAD")

	summary, err := agents.Implement(context.Background(), agentRequest(worktree, 1, ""))
	if err != nil {
		t.Fatalf("Implement: %v", err)
	}
	if !strings.Contains(summary, "deepseek-harness") || !strings.Contains(summary, "added greet.txt") {
		t.Fatalf("summary = %q, want harness and the agent's note", summary)
	}
	spawn := api.spawned[0]
	if spawn.Kind != domain.KindWorker || spawn.Harness != domain.HarnessDeepSeek || spawn.ProjectID != "proj-1" {
		t.Fatalf("spawn = %+v, want a deepseek worker in the resolved project", spawn)
	}
	if spawn.Branch != "ao/claude-orchestrator/run-1--sub-1" {
		t.Fatalf("agent branch = %q", spawn.Branch)
	}
	if !strings.Contains(spawn.Prompt, "write greet.txt") || !strings.Contains(spawn.Prompt, "ao report --done") {
		t.Fatalf("prompt = %q, want the instructions and how to report", spawn.Prompt)
	}
	// The agent's work is committed and fast-forwarded into the run worktree.
	if got := mustRead(t, filepath.Join(worktree, "greet.txt")); got != "hello\n" {
		t.Fatalf("run worktree greet.txt = %q", got)
	}
	if parent := runGit(t, worktree, "rev-parse", "HEAD~1"); parent != before {
		t.Fatalf("run HEAD~1 = %s, want the previous run HEAD %s", parent, before)
	}
	if status := runGit(t, worktree, "status", "--porcelain"); status != "" {
		t.Fatalf("run worktree is dirty: %q", status)
	}
}

func TestAgentSessionsRetryMessagesTheSameAgent(t *testing.T) {
	_, worktree := newAgentRun(t)
	api := newFakeAgentAPI(t, worktree, func(dir string, turn int) agentTurn {
		mustWrite(t, filepath.Join(dir, "greet.txt"), fmt.Sprintf("attempt %d\n", turn))
		return agentTurn{report: domain.ReportDone, note: "done", activity: domain.ActivityWaitingInput}
	})
	agents := newTestAgentSessions(t, api)
	if _, err := agents.Implement(context.Background(), agentRequest(worktree, 1, "")); err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	if _, err := agents.Implement(context.Background(), agentRequest(worktree, 2, "go test: greeting is wrong")); err != nil {
		t.Fatalf("attempt 2: %v", err)
	}
	if len(api.spawned) != 1 {
		t.Fatalf("spawned %d sessions, want the retry to reuse one", len(api.spawned))
	}
	if len(api.sent) != 1 || !strings.Contains(api.sent[0], "go test: greeting is wrong") {
		t.Fatalf("sent = %q, want the failure reason delivered to the agent", api.sent)
	}
	if got := mustRead(t, filepath.Join(worktree, "greet.txt")); got != "attempt 2\n" {
		t.Fatalf("run worktree greet.txt = %q, want the retried work", got)
	}
}

func TestAgentSessionsCompleteWhenTheAgentSettlesAtItsPrompt(t *testing.T) {
	_, worktree := newAgentRun(t)
	api := newFakeAgentAPI(t, worktree, func(dir string, _ int) agentTurn {
		mustWrite(t, filepath.Join(dir, "greet.txt"), "hi\n")
		return agentTurn{activity: domain.ActivityWaitingInput}
	})
	agents := newTestAgentSessions(t, api)
	if _, err := agents.Implement(context.Background(), agentRequest(worktree, 1, "")); err != nil {
		t.Fatalf("Implement: %v", err)
	}
	if got := mustRead(t, filepath.Join(worktree, "greet.txt")); got != "hi\n" {
		t.Fatalf("greet.txt = %q", got)
	}
}

func TestAgentSessionsTreatUnusableWorkAsRetryableFailures(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string) agentTurn{
		"stuck report": func(*testing.T, string) agentTurn {
			return agentTurn{report: domain.ReportStuck, note: "cannot find the parser"}
		},
		"ended session": func(*testing.T, string) agentTurn { return agentTurn{activity: domain.ActivityExited} },
		"no changes": func(*testing.T, string) agentTurn {
			return agentTurn{report: domain.ReportDone}
		},
		"protected path": func(t *testing.T, dir string) agentTurn {
			mustWrite(t, filepath.Join(dir, ".env"), "TOKEN=1\n")
			return agentTurn{report: domain.ReportDone}
		},
		"rewritten history": func(t *testing.T, dir string) agentTurn {
			mustWrite(t, filepath.Join(dir, "README.md"), "rewritten\n")
			runGit(t, dir, "-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "--quiet", "--all", "--amend", "-m", "rewrite")
			return agentTurn{report: domain.ReportDone}
		},
	}
	for name, play := range cases {
		t.Run(name, func(t *testing.T) {
			_, worktree := newAgentRun(t)
			api := newFakeAgentAPI(t, worktree, func(dir string, _ int) agentTurn { return play(t, dir) })
			agents := newTestAgentSessions(t, api)
			before := runGit(t, worktree, "rev-parse", "HEAD")
			_, err := agents.Implement(context.Background(), agentRequest(worktree, 1, ""))
			if !errors.Is(err, ports.ErrAgentAttemptFailed) {
				t.Fatalf("Implement error = %v, want a retryable agent failure", err)
			}
			if after := runGit(t, worktree, "rev-parse", "HEAD"); after != before {
				t.Fatalf("run worktree moved to %s despite the failure", after)
			}
		})
	}
}

func TestAgentSessionsNeverTypeIntoABlockedAgent(t *testing.T) {
	_, worktree := newAgentRun(t)
	api := newFakeAgentAPI(t, worktree, func(dir string, turn int) agentTurn {
		mustWrite(t, filepath.Join(dir, "greet.txt"), fmt.Sprintf("%d\n", turn))
		return agentTurn{report: domain.ReportDone, activity: domain.ActivityBlocked}
	})
	agents := newTestAgentSessions(t, api)
	if _, err := agents.Implement(context.Background(), agentRequest(worktree, 1, "")); err != nil {
		t.Fatalf("attempt 1: %v", err)
	}
	_, err := agents.Implement(context.Background(), agentRequest(worktree, 2, "tests failed"))
	if !errors.Is(err, ports.ErrAgentAttemptFailed) {
		t.Fatalf("retry error = %v, want a failure instead of input to a blocked agent", err)
	}
	if len(api.sent) != 0 {
		t.Fatalf("sent %q to a blocked agent", api.sent)
	}
}

func TestAgentSessionsKillTheAgentWhenTheAttemptIsCanceled(t *testing.T) {
	_, worktree := newAgentRun(t)
	api := newFakeAgentAPI(t, worktree, func(string, int) agentTurn { return agentTurn{activity: domain.ActivityActive} })
	agents := newTestAgentSessions(t, api)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := agents.Implement(ctx, agentRequest(worktree, 1, ""))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Implement error = %v, want the deadline", err)
	}
	if len(api.killed) != 1 || api.killed[0] != "ao-1" {
		t.Fatalf("killed = %v, want the agent session stopped", api.killed)
	}
}

func TestAgentSessionsRequireABoundBackendAndKnownHarnesses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := NewAgentSessions(ExecCommandRunner{}, AgentSessionsConfig{
		Harnesses:       map[ports.ModelProvider]domain.AgentHarness{ports.ModelProviderDeepSeek: "not-a-harness"},
		DefaultProvider: ports.ModelProviderDeepSeek,
	}); err == nil {
		t.Fatal("an unknown harness was accepted")
	}
	if _, err := NewAgentSessions(ExecCommandRunner{}, AgentSessionsConfig{
		Harnesses:       map[ports.ModelProvider]domain.AgentHarness{ports.ModelProviderClaude: domain.HarnessClaudeCode},
		DefaultProvider: ports.ModelProviderDeepSeek,
	}); err == nil {
		t.Fatal("a default provider without a harness was accepted")
	}
	agents, err := NewAgentSessions(ExecCommandRunner{}, AgentSessionsConfig{
		Harnesses:       map[ports.ModelProvider]domain.AgentHarness{ports.ModelProviderDeepSeek: domain.HarnessDeepSeek},
		DefaultProvider: ports.ModelProviderDeepSeek,
	})
	if err != nil {
		t.Fatalf("NewAgentSessions: %v", err)
	}
	if got := agents.HarnessFor(ports.ModelProviderClaude); got != domain.HarnessDeepSeek {
		t.Fatalf("HarnessFor(claude) = %q, want the default provider's harness", got)
	}
	if _, err := agents.Implement(context.Background(), agentRequest(t.TempDir(), 1, "")); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("Implement before Bind error = %v", err)
	}
}

type agentImplementerFake struct {
	summary string
	err     error
	request ports.AgentImplementRequest
}

func (f *agentImplementerFake) Implement(_ context.Context, request ports.AgentImplementRequest) (string, error) {
	f.request = request
	return f.summary, f.err
}

func TestWorkerHandsSubtasksToAgentsBeforeRunningCommands(t *testing.T) {
	worktree := t.TempDir()
	runner := &lsFilesRunner{}
	agent := &agentImplementerFake{summary: "agent ao-1 finished"}
	validator := &workerRuntimeValidatorFake{report: ports.ValidationReport{Passed: true}}
	runtime, err := NewWorkerRuntime(&workerRuntimeWorktreeFake{}, validator, runner, WorkerRuntimeConfig{
		ProjectRoot: filepath.Dir(worktree),
		Commands:    []ValidationCommand{{Name: "test", Argv: []string{"go", "test", "./..."}}},
		Timeout:     time.Second,
		Author:      &authorFake{proposeErr: errors.New("the model author must not run")},
		Agents:      agent,
	})
	if err != nil {
		t.Fatalf("NewWorkerRuntime: %v", err)
	}
	execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil || execution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("Execute = %+v, %v; want completed", execution, err)
	}
	if agent.request.Attempt != 2 || agent.request.PreviousFailure != "tests failed: missing greeting" || agent.request.Task.ID != "t1" {
		t.Fatalf("agent request = %+v", agent.request)
	}
	if len(runner.commands) != 1 || !strings.Contains(execution.Summary, "agent ao-1 finished") {
		t.Fatalf("commands = %v, summary = %q", runner.commands, execution.Summary)
	}

	agent.err = fmt.Errorf("%w: the agent reported stuck", ports.ErrAgentAttemptFailed)
	execution, err = runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil || execution.Status != ports.WorkerOutcomeFailed || !strings.Contains(execution.Error, "reported stuck") {
		t.Fatalf("Execute = %+v, %v; want a retryable failed attempt", execution, err)
	}
}
