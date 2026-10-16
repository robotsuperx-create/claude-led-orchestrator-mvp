package claudeorchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.Validator = (*Validator)(nil)

func TestNewValidatorRejectsShellStringsAndDeniedExecutables(t *testing.T) {
	runner := CommandRunnerFunc(func(context.Context, []string) (CommandOutput, error) {
		t.Fatal("runner must not be called during configuration")
		return CommandOutput{}, nil
	})
	for _, argv := range [][]string{
		{"go test ./..."},
		{"sh", "-c", "go test ./..."},
		{"/usr/bin/sudo", "go", "test"},
		{"python3.11", "-c", "..."},
	} {
		if _, err := NewValidator([]ValidationCommand{{Argv: argv}}, time.Second, runner); err == nil {
			t.Errorf("NewValidator(%q) expected rejection", argv)
		}
	}
}

func TestValidatorRunsOnlyFixedArgvAndReturnsTypedResults(t *testing.T) {
	configured := []ValidationCommand{{Name: "unit tests", Argv: []string{"go", "test", "./backend/...", "-run", "TestSafe"}}}
	var gotArgv []string
	runner := CommandRunnerFunc(func(ctx context.Context, argv []string) (CommandOutput, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("runner context has no timeout deadline")
		}
		gotArgv = append([]string(nil), argv...)
		return CommandOutput{ExitCode: 0, Stdout: "ok\n"}, nil
	})
	validator, err := NewValidator(configured, time.Second, runner)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	// Prove caller-owned slices cannot mutate the validator's command allowlist.
	configured[0].Argv[1] = "malicious-shell-input"

	result, err := validator.Check(context.Background(), ports.ValidationRequest{Task: "run shell: rm -rf /"})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	wantArgv := []string{"go", "test", "./backend/...", "-run", "TestSafe"}
	if strings.Join(gotArgv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Fatalf("runner argv = %#v, want fixed argv %#v", gotArgv, wantArgv)
	}
	if !result.Passed || len(result.Commands) != 1 || result.Commands[0].ExitCode != 0 || result.Commands[0].Stdout != "ok\n" {
		t.Fatalf("Check() result = %+v", result)
	}

	report, err := validator.Validate(context.Background(), ports.ValidationRequest{})
	if err != nil || !report.Passed || len(report.Issues) != 0 {
		t.Fatalf("Validate() = %+v, %v; want passed", report, err)
	}
}

func TestValidatorReportsFailureWithBoundedOutput(t *testing.T) {
	tooMuch := strings.Repeat("x", MaxValidationOutputBytes+100)
	runner := CommandRunnerFunc(func(context.Context, []string) (CommandOutput, error) {
		return CommandOutput{ExitCode: 7, Stdout: tooMuch, Stderr: "password=secret"}, nil
	})
	validator, err := NewValidator([]ValidationCommand{{Name: "check", Argv: []string{"go", "test"}}}, time.Second, runner)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}

	result, err := validator.Check(context.Background(), ports.ValidationRequest{})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Passed || len(result.Commands) != 1 || len(result.Issues) != 1 {
		t.Fatalf("Check() result = %+v, want failed command", result)
	}
	command := result.Commands[0]
	if len(command.Stdout) > MaxValidationOutputBytes || !command.StdoutTruncated {
		t.Errorf("stdout length/truncation = %d/%v", len(command.Stdout), command.StdoutTruncated)
	}
	if len(command.Stderr) > MaxValidationOutputBytes || strings.Contains(command.Stderr, "secret") {
		t.Errorf("stderr was not bounded and redacted: %q", command.Stderr)
	}
	if !strings.Contains(result.Issues[0], "exit code 7") || len(result.Issues[0]) > 2*MaxValidationOutputBytes+1024 {
		t.Errorf("failure issue is missing status or unbounded: length=%d", len(result.Issues[0]))
	}
}

func TestValidatorAppliesTimeoutToInjectedRunner(t *testing.T) {
	runner := CommandRunnerFunc(func(ctx context.Context, _ []string) (CommandOutput, error) {
		<-ctx.Done()
		return CommandOutput{}, ctx.Err()
	})
	validator, err := NewValidator([]ValidationCommand{{Name: "slow", Argv: []string{"go", "test"}}}, 20*time.Millisecond, runner)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	started := time.Now()
	result, err := validator.Check(context.Background(), ports.ValidationRequest{})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.Passed || time.Since(started) > time.Second {
		t.Fatalf("Check() did not enforce timeout promptly: elapsed=%s result=%+v", time.Since(started), result)
	}
	if len(result.Commands) != 1 || result.Commands[0].Error == "" {
		t.Fatalf("timeout result = %+v, want a typed command error", result)
	}
}

func TestValidatorRejectsInvalidConfiguration(t *testing.T) {
	validRunner := CommandRunnerFunc(func(context.Context, []string) (CommandOutput, error) { return CommandOutput{}, nil })
	for _, tc := range []struct {
		name     string
		commands []ValidationCommand
		timeout  time.Duration
		runner   CommandRunner
	}{
		{name: "no commands", timeout: time.Second, runner: validRunner},
		{name: "no timeout", commands: []ValidationCommand{{Argv: []string{"go", "test"}}}, runner: validRunner},
		{name: "no runner", commands: []ValidationCommand{{Argv: []string{"go", "test"}}}, timeout: time.Second},
		{name: "duplicate name", commands: []ValidationCommand{{Name: "test", Argv: []string{"go", "test"}}, {Name: "test", Argv: []string{"git", "status"}}}, timeout: time.Second, runner: validRunner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewValidator(tc.commands, tc.timeout, tc.runner); err == nil {
				t.Fatal("NewValidator() expected configuration error")
			}
		})
	}
}

func TestValidatorSurfacesInjectedRunnerErrors(t *testing.T) {
	runner := CommandRunnerFunc(func(context.Context, []string) (CommandOutput, error) {
		return CommandOutput{}, errors.New("runner unavailable")
	})
	validator, err := NewValidator([]ValidationCommand{{Name: "check", Argv: []string{"go", "test"}}}, time.Second, runner)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	result, err := validator.Check(context.Background(), ports.ValidationRequest{})
	if err != nil || result.Passed || len(result.Issues) != 1 || !strings.Contains(result.Issues[0], "runner unavailable") {
		t.Fatalf("Check() = %+v, %v; want failed report", result, err)
	}
}

type validatorSandboxFake struct {
	requests []ports.SandboxRunRequest
	result   ports.SandboxRunResult
}

func (f *validatorSandboxFake) Run(_ context.Context, request ports.SandboxRunRequest) (ports.SandboxRunResult, error) {
	f.requests = append(f.requests, request)
	return f.result, nil
}

func TestValidatorUsesConfiguredSandboxForWorktree(t *testing.T) {
	worktree := t.TempDir()
	sandbox := &validatorSandboxFake{result: ports.SandboxRunResult{Stdout: []byte("sandbox validation\n")}}
	runner := CommandRunnerFunc(func(context.Context, []string) (CommandOutput, error) {
		t.Fatal("host runner must not be called when validator sandbox is configured")
		return CommandOutput{}, nil
	})
	validator, err := NewValidatorWithSandbox(
		[]ValidationCommand{{Name: "checks", Argv: []string{"go", "test", "./..."}}},
		time.Second, runner, sandbox, "ao/worker:v1",
		ports.SandboxResourceLimits{MemoryBytes: 256 << 20, NanoCPUs: 1_000_000_000, PIDs: 128},
		ValidatorWorkspace{Worktrees: &workerRuntimeWorktreeFake{}, ProjectRoot: worktree},
	)
	if err != nil {
		t.Fatalf("NewValidatorWithSandbox() error = %v", err)
	}
	result, err := validator.Check(context.Background(), ports.ValidationRequest{
		Results: []ports.CollectedTaskResult{{Task: ports.PlannedSubtask{
			Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: worktree},
		}}},
	})
	if err != nil || !result.Passed {
		t.Fatalf("Check() = %+v, %v; want passed", result, err)
	}
	if len(sandbox.requests) != 1 {
		t.Fatalf("sandbox calls = %d, want 1", len(sandbox.requests))
	}
	request := sandbox.requests[0]
	if request.RootFS != "ao/worker:v1" || request.ProjectRoot != filepath.Clean(worktree) || request.WorkDir != "/workspace" || !request.NoNetwork {
		t.Fatalf("sandbox request = %+v, want constrained worktree request", request)
	}
	if got := strings.Join(request.Argv, "\x00"); got != "go\x00test\x00./..." {
		t.Fatalf("sandbox argv = %q, want fixed argv", got)
	}
}

func TestValidatorSandboxRequiresWorktreePath(t *testing.T) {
	sandbox := &validatorSandboxFake{}
	validator, err := NewValidatorWithSandbox(
		[]ValidationCommand{{Name: "checks", Argv: []string{"go", "test"}}},
		time.Second, CommandRunnerFunc(func(context.Context, []string) (CommandOutput, error) { return CommandOutput{}, nil }),
		sandbox, "ao/worker:v1", ports.SandboxResourceLimits{MemoryBytes: 1, NanoCPUs: 1, PIDs: 1},
		ValidatorWorkspace{Worktrees: &workerRuntimeWorktreeFake{}, ProjectRoot: t.TempDir()},
	)
	if err != nil {
		t.Fatalf("NewValidatorWithSandbox() error = %v", err)
	}
	if _, err := validator.Check(context.Background(), ports.ValidationRequest{}); err == nil || !strings.Contains(err.Error(), "worktree path") {
		t.Fatalf("Check() error = %v, want missing worktree path", err)
	}
	if len(sandbox.requests) != 0 {
		t.Fatalf("sandbox calls = %d, want 0", len(sandbox.requests))
	}
}

// A worktree path that the worktree manager does not recognize as a registered
// worktree of the project must never be bind-mounted into the sandbox, even
// though it is absolute and present in subtask metadata.
func TestValidatorSandboxRejectsUnregisteredWorktree(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir() // e.g. a home or credentials directory
	sandbox := &validatorSandboxFake{}
	worktrees := &workerRuntimeWorktreeFake{statusErr: errors.New("worktree: path is not a registered worktree of project root")}
	validator, err := NewValidatorWithSandbox(
		[]ValidationCommand{{Name: "checks", Argv: []string{"go", "test"}}},
		time.Second, CommandRunnerFunc(func(context.Context, []string) (CommandOutput, error) { return CommandOutput{}, nil }),
		sandbox, "ao/worker:v1", ports.SandboxResourceLimits{MemoryBytes: 1, NanoCPUs: 1, PIDs: 1},
		ValidatorWorkspace{Worktrees: worktrees, ProjectRoot: projectRoot},
	)
	if err != nil {
		t.Fatalf("NewValidatorWithSandbox() error = %v", err)
	}
	_, err = validator.Check(context.Background(), ports.ValidationRequest{
		Results: []ports.CollectedTaskResult{{Task: ports.PlannedSubtask{
			Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: outside},
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "not a registered worktree") {
		t.Fatalf("Check() error = %v, want unregistered worktree rejection", err)
	}
	if len(sandbox.requests) != 0 {
		t.Fatalf("sandbox calls = %d, want 0: an unverified path must never be mounted", len(sandbox.requests))
	}
	if worktrees.statusRequest.ProjectRoot != filepath.Clean(projectRoot) || worktrees.statusRequest.Path != filepath.Clean(outside) {
		t.Fatalf("Status request = %+v, want the configured root and the candidate path", worktrees.statusRequest)
	}
}

func TestValidatorSandboxRejectsWorktreeManagerPathMismatch(t *testing.T) {
	requested := t.TempDir()
	sandbox := &validatorSandboxFake{}
	worktrees := &workerRuntimeWorktreeFake{statusResult: ports.WorktreeStatusResult{Path: t.TempDir()}}
	validator, err := NewValidatorWithSandbox(
		[]ValidationCommand{{Name: "checks", Argv: []string{"go", "test"}}},
		time.Second, CommandRunnerFunc(func(context.Context, []string) (CommandOutput, error) { return CommandOutput{}, nil }),
		sandbox, "ao/worker:v1", ports.SandboxResourceLimits{MemoryBytes: 1, NanoCPUs: 1, PIDs: 1},
		ValidatorWorkspace{Worktrees: worktrees, ProjectRoot: requested},
	)
	if err != nil {
		t.Fatalf("NewValidatorWithSandbox() error = %v", err)
	}
	_, err = validator.Check(context.Background(), ports.ValidationRequest{
		Results: []ports.CollectedTaskResult{{Task: ports.PlannedSubtask{
			Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: requested},
		}}},
	})
	if err == nil || !strings.Contains(err.Error(), "different path") {
		t.Fatalf("Check() error = %v, want path mismatch rejection", err)
	}
	if len(sandbox.requests) != 0 {
		t.Fatalf("sandbox calls = %d, want 0", len(sandbox.requests))
	}
}

func TestNewValidatorWithSandboxRequiresWorktreeBoundary(t *testing.T) {
	_, err := NewValidatorWithSandbox(
		[]ValidationCommand{{Name: "checks", Argv: []string{"go", "test"}}},
		time.Second, CommandRunnerFunc(func(context.Context, []string) (CommandOutput, error) { return CommandOutput{}, nil }),
		&validatorSandboxFake{}, "ao/worker:v1", ports.SandboxResourceLimits{MemoryBytes: 1, NanoCPUs: 1, PIDs: 1},
		ValidatorWorkspace{},
	)
	if err == nil {
		t.Fatal("NewValidatorWithSandbox() without a worktree manager must fail closed")
	}
}

// Host-executed validation must run in the verified task worktree, not in the
// daemon's own working directory.
func TestValidatorInWorkspaceRunsCommandsInVerifiedWorktree(t *testing.T) {
	worktree := t.TempDir()
	runner := &workerRuntimeRunnerFake{}
	worktrees := &workerRuntimeWorktreeFake{}
	validator, err := NewValidatorInWorkspace(
		[]ValidationCommand{{Name: "checks", Argv: []string{"go", "test", "./..."}}},
		time.Second, runner, ValidatorWorkspace{Worktrees: worktrees, ProjectRoot: worktree},
	)
	if err != nil {
		t.Fatalf("NewValidatorInWorkspace() error = %v", err)
	}
	result, err := validator.Check(context.Background(), ports.ValidationRequest{
		Results: []ports.CollectedTaskResult{{Task: ports.PlannedSubtask{
			Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: worktree},
		}}},
	})
	if err != nil || !result.Passed {
		t.Fatalf("Check() = %+v, %v; want passed", result, err)
	}
	if worktrees.statusCalls != 1 {
		t.Fatalf("Status calls = %d, want 1", worktrees.statusCalls)
	}
	if len(runner.calls) != 1 || runner.calls[0].directory != filepath.Clean(worktree) {
		t.Fatalf("runner calls = %+v, want one call in %q", runner.calls, worktree)
	}
}
