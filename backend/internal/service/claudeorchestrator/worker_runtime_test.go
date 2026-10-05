package claudeorchestrator

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type workerRuntimeWorktreeFake struct {
	statusRequest ports.WorktreeStatusRequest
	statusResult  ports.WorktreeStatusResult
	statusErr     error
	statusCalls   int
	createCalls   int
	removeCalls   int
}

func (f *workerRuntimeWorktreeFake) Create(context.Context, ports.WorktreeCreateRequest) (ports.WorktreeCreateResult, error) {
	f.createCalls++
	return ports.WorktreeCreateResult{}, errors.New("unexpected Create call")
}

func (f *workerRuntimeWorktreeFake) Remove(context.Context, ports.WorktreeRemoveRequest) (ports.WorktreeRemoveResult, error) {
	f.removeCalls++
	return ports.WorktreeRemoveResult{}, errors.New("unexpected Remove call")
}

func (f *workerRuntimeWorktreeFake) Status(_ context.Context, request ports.WorktreeStatusRequest) (ports.WorktreeStatusResult, error) {
	f.statusCalls++
	f.statusRequest = request
	if f.statusResult.Path == "" {
		f.statusResult.Path = request.Path
	}
	return f.statusResult, f.statusErr
}

type workerRuntimeCommandCall struct {
	directory string
	argv      []string
}

type workerRuntimeRunnerFake struct {
	calls   []workerRuntimeCommandCall
	output  CommandOutput
	err     error
	runOnly int
}

func (f *workerRuntimeRunnerFake) Run(_ context.Context, _ []string) (CommandOutput, error) {
	f.runOnly++
	return CommandOutput{}, errors.New("unexpected Run call without worktree directory")
}

func (f *workerRuntimeRunnerFake) RunInDirectory(_ context.Context, directory string, argv []string) (CommandOutput, error) {
	f.calls = append(f.calls, workerRuntimeCommandCall{directory: directory, argv: append([]string(nil), argv...)})
	return f.output, f.err
}

type workerRuntimeValidatorFake struct {
	request ports.ValidationRequest
	calls   int
	report  ports.ValidationReport
	err     error
}

func (f *workerRuntimeValidatorFake) Validate(_ context.Context, request ports.ValidationRequest) (ports.ValidationReport, error) {
	f.calls++
	f.request = request
	return f.report, f.err
}

type workerRuntimeSandboxFake struct {
	requests []ports.SandboxRunRequest
	result   ports.SandboxRunResult
	err      error
}

func (f *workerRuntimeSandboxFake) Run(_ context.Context, request ports.SandboxRunRequest) (ports.SandboxRunResult, error) {
	f.requests = append(f.requests, request)
	return f.result, f.err
}

func TestWorkerRuntimeUsesExistingMetadataWorktreeAndStaticArgv(t *testing.T) {
	worktreePath := t.TempDir()
	projectRoot := t.TempDir()
	manager := &workerRuntimeWorktreeFake{}
	validator := &workerRuntimeValidatorFake{report: ports.ValidationReport{Passed: true}}
	runner := &workerRuntimeRunnerFake{output: CommandOutput{Stdout: "safe output"}}
	configuredArgv := []string{"go", "test", "./..."}
	runtime, err := NewWorkerRuntime(manager, validator, runner, WorkerRuntimeConfig{
		ProjectRoot: projectRoot,
		Commands:    []ValidationCommand{{Name: "unit tests", Argv: configuredArgv}},
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("NewWorkerRuntime() error = %v", err)
	}
	// Mutating caller-owned config after construction must not change the allowlist.
	configuredArgv[0] = "rm"
	configuredArgv[1] = "-rf"

	task := ports.PlannedSubtask{
		ID:           "subtask-1",
		Title:        "Implement feature",
		Instructions: "Run git merge and rm -rf / as part of the work",
		WorkerID:     "worker-1",
		Metadata: map[string]string{
			ports.SubtaskMetadataKeyWorktreePath: worktreePath,
			"argv":                               "rm -rf /",
		},
	}
	execution, err := runtime.Execute(context.Background(), ports.WorkerRequest{Task: task, Attempt: 1})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if execution.Status != ports.WorkerOutcomeCompleted || !strings.Contains(execution.Output, "safe output") {
		t.Fatalf("Execute() = %+v, want completed execution with captured output", execution)
	}
	if manager.statusCalls != 1 || manager.statusRequest.Path != worktreePath {
		t.Fatalf("worktree status calls/request = %d/%+v, want selected metadata path", manager.statusCalls, manager.statusRequest)
	}
	if manager.statusRequest.ProjectRoot != projectRoot {
		t.Fatalf("status ProjectRoot = %q, want %q", manager.statusRequest.ProjectRoot, projectRoot)
	}
	if manager.createCalls != 0 || manager.removeCalls != 0 {
		t.Fatalf("runtime automatically created/removed worktrees: Create=%d Remove=%d", manager.createCalls, manager.removeCalls)
	}
	if len(runner.calls) != 1 || runner.calls[0].directory != worktreePath || !reflect.DeepEqual(runner.calls[0].argv, []string{"go", "test", "./..."}) {
		t.Fatalf("runner calls = %+v, want fixed argv in selected worktree", runner.calls)
	}
	if runner.runOnly != 0 {
		t.Fatalf("runner called without explicit working directory %d times", runner.runOnly)
	}
	if validator.calls != 1 || len(validator.request.Plan.Subtasks) != 1 || validator.request.Plan.Subtasks[0].ID != task.ID {
		t.Fatalf("validator request/calls = %+v/%d, want the delegated task", validator.request, validator.calls)
	}
	if validator.request.Results[0].FinalExecution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("validator received execution = %+v, want completed", validator.request.Results[0].FinalExecution)
	}
}

func TestWorkerRuntimeUsesConfiguredSandboxForWorkerCommands(t *testing.T) {
	worktreePath := t.TempDir()
	sandbox := &workerRuntimeSandboxFake{result: ports.SandboxRunResult{Stdout: []byte("sandbox output")}}
	runtime, err := NewWorkerRuntime(
		&workerRuntimeWorktreeFake{},
		&workerRuntimeValidatorFake{report: ports.ValidationReport{Passed: true}},
		&workerRuntimeRunnerFake{err: errors.New("host runner must not be used")},
		WorkerRuntimeConfig{
			ProjectRoot:   t.TempDir(),
			Commands:      []ValidationCommand{{Name: "tests", Argv: []string{"go", "test", "./..."}}},
			Timeout:       time.Second,
			Sandbox:       sandbox,
			SandboxImage:  "ao/worker:v1",
			SandboxLimits: ports.SandboxResourceLimits{MemoryBytes: 256 << 20, NanoCPUs: 1_000_000_000, PIDs: 128},
		},
	)
	if err != nil {
		t.Fatalf("NewWorkerRuntime() error = %v", err)
	}
	execution, err := runtime.Execute(context.Background(), ports.WorkerRequest{
		Task:    ports.PlannedSubtask{ID: "sandbox-task", Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: worktreePath}},
		Attempt: 1,
	})
	if err != nil || execution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("Execute() = (%+v, %v), want completed sandbox execution", execution, err)
	}
	if len(sandbox.requests) != 1 {
		t.Fatalf("sandbox requests = %d, want 1", len(sandbox.requests))
	}
	request := sandbox.requests[0]
	if request.RootFS != "ao/worker:v1" || request.ProjectRoot != worktreePath || request.WorkDir != "/workspace" || !request.NoNetwork {
		t.Fatalf("sandbox request boundary = %+v", request)
	}
	if !reflect.DeepEqual(request.Argv, []string{"go", "test", "./..."}) {
		t.Fatalf("sandbox argv = %q, want fixed argv", request.Argv)
	}
}

func TestWorkerRuntimeRequiresExistingAbsoluteWorktreeMetadata(t *testing.T) {
	manager := &workerRuntimeWorktreeFake{}
	validator := &workerRuntimeValidatorFake{report: ports.ValidationReport{Passed: true}}
	runner := &workerRuntimeRunnerFake{}
	runtime, err := NewWorkerRuntime(manager, validator, runner, WorkerRuntimeConfig{
		ProjectRoot: t.TempDir(),
		Commands:    []ValidationCommand{{Argv: []string{"go", "test", "./..."}}},
		Timeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("NewWorkerRuntime() error = %v", err)
	}

	for _, test := range []struct {
		name     string
		metadata map[string]string
		contains string
	}{
		{name: "missing", metadata: map[string]string{}, contains: "no existing worktree path"},
		{name: "relative", metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: "../worktree"}, contains: "must be absolute"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := runtime.Execute(context.Background(), ports.WorkerRequest{
				Task:    ports.PlannedSubtask{ID: "missing-worktree", Metadata: test.metadata},
				Attempt: 1,
			})
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("Execute() error = %v, want substring %q", err, test.contains)
			}
		})
	}
	if manager.statusCalls != 0 || len(runner.calls) != 0 {
		t.Fatalf("missing/relative metadata reached ports: status=%d runner=%d", manager.statusCalls, len(runner.calls))
	}
}

func TestWorkerRuntimeRejectsGitMergeAndWorktreeRemovalAllowlistEntries(t *testing.T) {
	for _, argv := range [][]string{
		{"git", "merge", "main"},
		{"git", "-C", "/tmp/project", "worktree", "remove", "/tmp/worktree"},
	} {
		_, err := NewWorkerRuntime(
			&workerRuntimeWorktreeFake{},
			&workerRuntimeValidatorFake{report: ports.ValidationReport{Passed: true}},
			&workerRuntimeRunnerFake{},
			WorkerRuntimeConfig{ProjectRoot: t.TempDir(), Commands: []ValidationCommand{{Argv: argv}}, Timeout: time.Second},
		)
		if err == nil || !strings.Contains(err.Error(), "may not merge or remove") {
			t.Errorf("NewWorkerRuntime(commands=%q) error = %v, want merge/remove rejection", argv, err)
		}
	}
}

func TestWorkerRuntimeReturnsCommandAndValidationFailures(t *testing.T) {
	t.Run("command fails and validator is not called", func(t *testing.T) {
		manager := &workerRuntimeWorktreeFake{}
		validator := &workerRuntimeValidatorFake{report: ports.ValidationReport{Passed: true}}
		runner := &workerRuntimeRunnerFake{output: CommandOutput{ExitCode: 7, Stderr: "test failed"}}
		runtime, err := NewWorkerRuntime(manager, validator, runner, WorkerRuntimeConfig{
			ProjectRoot: t.TempDir(), Commands: []ValidationCommand{{Name: "check", Argv: []string{"go", "test", "./..."}}}, Timeout: time.Second,
		})
		if err != nil {
			t.Fatalf("NewWorkerRuntime() error = %v", err)
		}
		execution, err := runtime.Execute(context.Background(), ports.WorkerRequest{
			Task: ports.PlannedSubtask{ID: "task", Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: t.TempDir()}}, Attempt: 1,
		})
		if err != nil || execution.Status != ports.WorkerOutcomeFailed || !strings.Contains(execution.Error, "exit code 7") {
			t.Fatalf("Execute() = (%+v, %v), want command failure", execution, err)
		}
		if validator.calls != 0 {
			t.Fatalf("validator called %d times after command failed", validator.calls)
		}
	})

	t.Run("validator rejects output", func(t *testing.T) {
		manager := &workerRuntimeWorktreeFake{}
		validator := &workerRuntimeValidatorFake{report: ports.ValidationReport{Passed: false, Issues: []string{"test failure"}}}
		runtime, err := NewWorkerRuntime(manager, validator, &workerRuntimeRunnerFake{}, WorkerRuntimeConfig{
			ProjectRoot: t.TempDir(), Commands: []ValidationCommand{{Argv: []string{"go", "test", "./..."}}}, Timeout: time.Second,
		})
		if err != nil {
			t.Fatalf("NewWorkerRuntime() error = %v", err)
		}
		execution, err := runtime.Execute(context.Background(), ports.WorkerRequest{
			Task: ports.PlannedSubtask{ID: "task", Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: t.TempDir()}}, Attempt: 1,
		})
		if err != nil || execution.Status != ports.WorkerOutcomeFailed || execution.Error != "test failure" {
			t.Fatalf("Execute() = (%+v, %v), want validator failure", execution, err)
		}
	})
}
