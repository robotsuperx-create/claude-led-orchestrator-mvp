package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/claudeorchestrator"
)

const localDemoWorkerOutput = "created by fake DeepSeek worker"

type localDemoEventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *localDemoEventLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *localDemoEventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

type localDemoModel struct {
	mu             sync.Mutex
	worktreePath   string
	events         *localDemoEventLog
	planProvider   ports.ModelProvider
	reviewProvider ports.ModelProvider
	planCalls      int
	reviewCalls    int
}

func (m *localDemoModel) Plan(_ context.Context, request ports.PlanRequest) (ports.ExecutionPlan, error) {
	m.events.add("claude.plan")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.planCalls++
	m.planProvider = request.Provider
	return ports.ExecutionPlan{
		Summary: "local fake plan",
		Subtasks: []ports.PlannedSubtask{{
			ID:           "local-demo-subtask",
			Title:        "Write a demo artifact",
			Instructions: "Create worker-output.txt in the selected local worktree",
			WorkerID:     "deepseek-fake",
			Provider:     ports.ModelProviderDeepSeek,
			// A model has no authority over where commands run. This hostile
			// value must be replaced by the server-configured worktree.
			Metadata: map[string]string{
				ports.SubtaskMetadataKeyWorktreePath: filepath.Join(filepath.Dir(m.worktreePath), "model-chosen-elsewhere"),
			},
		}},
	}, nil
}

func (m *localDemoModel) Review(_ context.Context, request ports.ReviewRequest) (ports.ReviewDecision, error) {
	m.events.add("claude.review")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reviewCalls++
	m.reviewProvider = request.Provider
	if !request.Validation.Passed {
		return ports.ReviewDecision{}, fmt.Errorf("review called without passing validation")
	}
	if strings.Contains(strings.ToLower(request.Task), "hold") {
		return ports.ReviewDecision{
			Decision: ports.ReviewOutcomeRequestChanges,
			Summary:  "demo hold requested",
			Issues:   []string{"review requests changes"},
		}, nil
	}
	return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove, Summary: "demo approved"}, nil
}

func (m *localDemoModel) snapshot() (ports.ModelProvider, ports.ModelProvider, int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.planProvider, m.reviewProvider, m.planCalls, m.reviewCalls
}

type localDemoWorktree struct {
	root        string
	path        string
	events      *localDemoEventLog
	mu          sync.Mutex
	statusCalls int
	createCalls int
	removeCalls int
	lastStatus  ports.WorktreeStatusRequest
}

func (w *localDemoWorktree) Create(context.Context, ports.WorktreeCreateRequest) (ports.WorktreeCreateResult, error) {
	w.mu.Lock()
	w.createCalls++
	w.mu.Unlock()
	return ports.WorktreeCreateResult{}, fmt.Errorf("unexpected worktree creation in local demo")
}

func (w *localDemoWorktree) Remove(context.Context, ports.WorktreeRemoveRequest) (ports.WorktreeRemoveResult, error) {
	w.mu.Lock()
	w.removeCalls++
	w.mu.Unlock()
	return ports.WorktreeRemoveResult{}, fmt.Errorf("unexpected worktree removal in local demo")
}

func (w *localDemoWorktree) Status(_ context.Context, request ports.WorktreeStatusRequest) (ports.WorktreeStatusResult, error) {
	w.events.add("worktree.status")
	w.mu.Lock()
	defer w.mu.Unlock()
	w.statusCalls++
	w.lastStatus = request
	if request.ProjectRoot != w.root || request.Path != w.path {
		return ports.WorktreeStatusResult{}, fmt.Errorf("unexpected fake worktree path")
	}
	return ports.WorktreeStatusResult{Path: w.path, Branch: "local-demo", Clean: true, Output: "## local-demo"}, nil
}

func (w *localDemoWorktree) snapshot() (int, int, int, ports.WorktreeStatusRequest) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.statusCalls, w.createCalls, w.removeCalls, w.lastStatus
}

type localDemoWorker struct {
	worktrees ports.WorktreeManager
	events    *localDemoEventLog
	mu        sync.Mutex
	calls     int
	provider  ports.ModelProvider
}

func (w *localDemoWorker) Execute(ctx context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
	w.events.add("deepseek.fake-worker")
	w.mu.Lock()
	w.calls++
	w.provider = request.Task.Provider
	w.mu.Unlock()
	if request.Task.Provider != ports.ModelProviderDeepSeek {
		return ports.WorkerExecution{}, fmt.Errorf("fake worker expected DeepSeek provider")
	}
	path := request.Task.Metadata[ports.SubtaskMetadataKeyWorktreePath]
	status, err := w.worktrees.Status(ctx, ports.WorktreeStatusRequest{ProjectRoot: filepath.Dir(path), Path: path})
	if err != nil {
		return ports.WorkerExecution{}, err
	}
	if status.Path != path {
		return ports.WorkerExecution{}, fmt.Errorf("fake worktree returned a different path")
	}
	if err := os.WriteFile(filepath.Join(path, "worker-output.txt"), []byte(localDemoWorkerOutput+"\n"), 0o600); err != nil {
		return ports.WorkerExecution{}, fmt.Errorf("write fake worker output: %w", err)
	}
	return ports.WorkerExecution{
		Status:  ports.WorkerOutcomeCompleted,
		Summary: "fake DeepSeek worker completed",
		Output:  localDemoWorkerOutput,
	}, nil
}

func (w *localDemoWorker) snapshot() (int, ports.ModelProvider) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls, w.provider
}

type localDemoValidator struct {
	worktreePath string
	events       *localDemoEventLog
	mu           sync.Mutex
	calls        int
}

func (v *localDemoValidator) Validate(_ context.Context, request ports.ValidationRequest) (ports.ValidationReport, error) {
	v.events.add("validator")
	v.mu.Lock()
	v.calls++
	v.mu.Unlock()
	if request.Task == "" || len(request.Results) != 1 || request.Results[0].FinalExecution.Status != ports.WorkerOutcomeCompleted {
		return ports.ValidationReport{Issues: []string{"expected one completed worker result"}}, nil
	}
	output, err := os.ReadFile(filepath.Join(v.worktreePath, "worker-output.txt"))
	if errors.Is(err, fs.ErrNotExist) {
		// A missing artifact is a validation finding, not an infrastructure
		// error, so it is reported as a failed report.
		return ports.ValidationReport{Issues: []string{"worker output file is missing"}}, nil
	}
	if err != nil {
		return ports.ValidationReport{}, fmt.Errorf("read worker output: %w", err)
	}
	if strings.TrimSpace(string(output)) != localDemoWorkerOutput {
		return ports.ValidationReport{Issues: []string{"worker output did not match"}}, nil
	}
	return ports.ValidationReport{Passed: true}, nil
}

func (v *localDemoValidator) snapshot() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls
}

type localDemoMemory struct {
	events   *localDemoEventLog
	recorded chan ports.MemoryOutcome
}

func (m *localDemoMemory) ReadContext(context.Context, ports.MemoryContextRequest) (ports.MemoryContext, error) {
	m.events.add("project-memory.read")
	return ports.MemoryContext{Content: "local demo context"}, nil
}

func (m *localDemoMemory) RecordOutcome(_ context.Context, outcome ports.MemoryOutcome) error {
	m.events.add("project-memory.record")
	m.recorded <- outcome
	return nil
}

func TestClaudeOrchestratorLocalDemoSmoke(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		task      string
		wantState ports.RunState
		wantMerge ports.MergeOutcome
	}{
		{name: "merge recommendation", task: "Write the local demo artifact", wantState: ports.RunStateCompleted, wantMerge: ports.MergeOutcomeMerge},
		{name: "hold recommendation", task: "Hold this local demo for changes", wantState: ports.RunStateHeld, wantMerge: ports.MergeOutcomeHold},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			localDemoRunThroughRouter(t, scenario.task, scenario.wantState, scenario.wantMerge)
		})
	}
}

func localDemoRunThroughRouter(t *testing.T, task string, wantState ports.RunState, wantMerge ports.MergeOutcome) {
	t.Helper()
	root := t.TempDir()
	worktreePath := filepath.Join(root, "fake-worktree")
	if err := os.Mkdir(worktreePath, 0o700); err != nil {
		t.Fatalf("create temporary fake worktree: %v", err)
	}
	events := &localDemoEventLog{}
	worktrees := &localDemoWorktree{root: root, path: worktreePath, events: events}
	model := &localDemoModel{worktreePath: worktreePath, events: events}
	worker := &localDemoWorker{worktrees: worktrees, events: events}
	validator := &localDemoValidator{worktreePath: worktreePath, events: events}
	memory := &localDemoMemory{events: events, recorded: make(chan ports.MemoryOutcome, 1)}
	service := claudeorchestrator.New(claudeorchestrator.Dependencies{
		Model: model, Worker: worker, Validator: validator, Memory: memory,
		DefaultWorktreePath: worktreePath,
	})
	cfg := config.Config{ClaudeOrchestrator: config.ClaudeOrchestratorConfig{FeatureEnabled: true}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouterWithControl(cfg, logger, nil, httpd.APIDeps{ClaudeOrchestrator: service}, httpd.ControlDeps{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	body, err := json.Marshal(map[string]any{"task": task, "explicitOptIn": true, "maxRetries": 0})
	if err != nil {
		t.Fatalf("marshal start request: %v", err)
	}
	startRequest, err := http.NewRequest(http.MethodPost, server.URL+"/internal/claude-orchestrator/runs", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("create start request: %v", err)
	}
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse, err := server.Client().Do(startRequest)
	if err != nil {
		t.Fatalf("POST run: %v", err)
	}
	startPayload := localDemoDecodeResponse(t, startResponse, http.StatusAccepted)
	runID, ok := startPayload["runId"].(string)
	if !ok || runID == "" || startPayload["state"] != string(ports.RunStatePending) {
		t.Fatalf("start payload = %#v, want opaque run id and pending state", startPayload)
	}

	var finalPayload map[string]any
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		statusResponse, getErr := server.Client().Get(server.URL + "/internal/claude-orchestrator/runs/" + runID)
		if getErr != nil {
			t.Fatalf("GET run status: %v", getErr)
		}
		finalPayload = localDemoDecodeResponse(t, statusResponse, http.StatusOK)
		if finalPayload["state"] == string(ports.RunStateCompleted) || finalPayload["state"] == string(ports.RunStateHeld) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if finalPayload["runId"] != runID || finalPayload["state"] != string(wantState) || finalPayload["recommendation"] != string(wantMerge) {
		t.Fatalf("terminal status = %#v, want runId %q, state %q and recommendation %q", finalPayload, runID, wantState, wantMerge)
	}

	select {
	case outcome := <-memory.recorded:
		if outcome.RunID != runID || outcome.RunState != wantState || outcome.MergeDecision.Decision != wantMerge {
			t.Fatalf("recorded outcome = %+v, want state %q and merge recommendation %q", outcome, wantState, wantMerge)
		}
	case <-time.After(time.Second):
		t.Fatal("orchestrator did not record its final fake outcome")
	}

	planProvider, reviewProvider, planCalls, reviewCalls := model.snapshot()
	if planProvider != ports.ModelProviderClaude || reviewProvider != ports.ModelProviderClaude || planCalls != 1 || reviewCalls != 1 {
		t.Fatalf("Claude fake plan/review = provider %q/%q calls %d/%d", planProvider, reviewProvider, planCalls, reviewCalls)
	}
	workerCalls, workerProvider := worker.snapshot()
	if workerCalls != 1 || workerProvider != ports.ModelProviderDeepSeek {
		t.Fatalf("fake worker = provider %q calls %d, want one DeepSeek worker call", workerProvider, workerCalls)
	}
	if got := validator.snapshot(); got != 1 {
		t.Fatalf("validator calls = %d, want 1", got)
	}
	statusCalls, createCalls, removeCalls, statusRequest := worktrees.snapshot()
	if statusCalls != 1 || createCalls != 0 || removeCalls != 0 || statusRequest.Path != worktreePath {
		t.Fatalf("fake worktree calls/status = %d/%d/%d %+v, want status-only on %q", statusCalls, createCalls, removeCalls, statusRequest, worktreePath)
	}
	wantEvents := []string{"project-memory.read", "claude.plan", "deepseek.fake-worker", "worktree.status", "validator", "claude.review", "project-memory.record"}
	if got := events.snapshot(); !reflect.DeepEqual(got, wantEvents) {
		t.Fatalf("orchestration event order = %v, want %v", got, wantEvents)
	}
	if contents, readErr := os.ReadFile(filepath.Join(worktreePath, "worker-output.txt")); readErr != nil || strings.TrimSpace(string(contents)) != localDemoWorkerOutput {
		t.Fatalf("fake worktree artifact = %q, error = %v", contents, readErr)
	}
}

func localDemoDecodeResponse(t *testing.T, response *http.Response, wantStatus int) map[string]any {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read HTTP response: %v", err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("HTTP status = %d, want %d: %s", response.StatusCode, wantStatus, body)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode HTTP response %q: %v", body, err)
	}
	// Only identity, state, and where the result lives may be returned; no
	// prompt, model, worker, or error text.
	allowed := map[string]bool{"runId": true, "state": true, "branch": true, "commit": true, "recommendation": true}
	for key := range payload {
		if !allowed[key] {
			t.Fatalf("response fields = %#v, unexpected field %q", payload, key)
		}
	}
	if payload["runId"] == nil || payload["state"] == nil {
		t.Fatalf("response fields = %#v, want runId and state", payload)
	}
	return payload
}
