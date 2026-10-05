package claudeorchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.WorkerRuntime = (*WorkerRuntime)(nil)

	ErrWorkerRuntimeUnavailable = errors.New("Claude orchestrator worker runtime is unavailable")
)

// WorkerRuntimeConfig contains trusted, operator-supplied commands. These argv
// vectors are copied at construction and are never sourced from a model plan.
type WorkerRuntimeConfig struct {
	ProjectRoot string
	Commands    []ValidationCommand
	Timeout     time.Duration
	// Sandbox, when supplied, becomes the execution boundary for worker
	// commands. Image and limits are trusted daemon configuration.
	Sandbox       ports.SandboxRunner
	SandboxImage  string
	SandboxLimits ports.SandboxResourceLimits
}

// WorkerRuntime runs a fixed command allowlist in an already-existing task
// worktree, then asks the injected validator to inspect the result. It does not
// create or remove worktrees, and it never merges Git branches.
type WorkerRuntime struct {
	worktrees     ports.WorktreeManager
	validator     ports.Validator
	runner        WorktreeCommandRunner
	projectRoot   string
	commands      []ValidationCommand
	timeout       time.Duration
	sandbox       ports.SandboxRunner
	sandboxImage  string
	sandboxLimits ports.SandboxResourceLimits
}

// NewWorkerRuntime constructs a runtime around existing worktree, validation,
// and command-runner ports. The configured argv vectors are the complete worker
// allowlist; PlannedSubtask instructions and metadata are never parsed as argv.
func NewWorkerRuntime(worktrees ports.WorktreeManager, validator ports.Validator, runner CommandRunner, config WorkerRuntimeConfig) (*WorkerRuntime, error) {
	if worktrees == nil {
		return nil, errors.New("worker runtime worktree manager is required")
	}
	if validator == nil {
		return nil, errors.New("worker runtime validator is required")
	}
	if runner == nil {
		return nil, errors.New("worker runtime command runner is required")
	}
	worktreeRunner, ok := runner.(WorktreeCommandRunner)
	if !ok {
		return nil, errors.New("worker runtime command runner must support an explicit working directory")
	}
	if strings.TrimSpace(config.ProjectRoot) == "" {
		return nil, errors.New("worker runtime project root is required")
	}
	if config.Timeout <= 0 {
		return nil, errors.New("worker runtime timeout must be positive")
	}
	projectRoot, err := filepath.Abs(config.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve worker runtime project root: %w", err)
	}
	commands, err := validateWorkerCommands(config.Commands)
	if err != nil {
		return nil, err
	}
	return &WorkerRuntime{
		worktrees:     worktrees,
		validator:     validator,
		runner:        worktreeRunner,
		projectRoot:   filepath.Clean(projectRoot),
		commands:      commands,
		timeout:       config.Timeout,
		sandbox:       config.Sandbox,
		sandboxImage:  strings.TrimSpace(config.SandboxImage),
		sandboxLimits: config.SandboxLimits,
	}, nil
}

// Execute runs the constructor's fixed command allowlist in the pre-existing
// worktree named by ports.SubtaskMetadataKeyWorktreePath. Commands provided in
// task instructions or metadata are deliberately ignored.
func (r *WorkerRuntime) Execute(ctx context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
	if r == nil || r.worktrees == nil || r.validator == nil || r.runner == nil || len(r.commands) == 0 || r.timeout <= 0 {
		return ports.WorkerExecution{}, ErrWorkerRuntimeUnavailable
	}
	if ctx == nil {
		return ports.WorkerExecution{}, errors.New("worker runtime context is required")
	}
	if request.Attempt < 1 {
		return ports.WorkerExecution{}, errors.New("worker attempt must be positive")
	}
	worktreePath := strings.TrimSpace(request.Task.Metadata[ports.SubtaskMetadataKeyWorktreePath])
	if worktreePath == "" {
		return ports.WorkerExecution{}, fmt.Errorf("subtask %q has no existing worktree path in metadata", request.Task.ID)
	}
	if !filepath.IsAbs(worktreePath) {
		return ports.WorkerExecution{}, fmt.Errorf("subtask %q worktree path must be absolute", request.Task.ID)
	}
	worktreePath = filepath.Clean(worktreePath)

	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	status, err := r.worktrees.Status(runCtx, ports.WorktreeStatusRequest{
		ProjectRoot: r.projectRoot,
		Path:        worktreePath,
	})
	if err != nil {
		return ports.WorkerExecution{}, fmt.Errorf("inspect subtask %q worktree: %w", request.Task.ID, err)
	}
	if returnedPath := strings.TrimSpace(status.Path); returnedPath != "" {
		resolvedStatusPath, resolveErr := filepath.Abs(returnedPath)
		if resolveErr != nil || filepath.Clean(resolvedStatusPath) != worktreePath {
			return ports.WorkerExecution{}, fmt.Errorf("worktree manager returned a different path for subtask %q", request.Task.ID)
		}
	}
	if err := runCtx.Err(); err != nil {
		return ports.WorkerExecution{}, err
	}

	var output strings.Builder
	for _, command := range r.commands {
		if err := runCtx.Err(); err != nil {
			return ports.WorkerExecution{}, err
		}
		// argv is copied from trusted construction-time configuration. No task
		// field is appended or interpolated, and the runner does not invoke a shell.
		commandOutput, runErr := r.runCommand(runCtx, worktreePath, command.Argv)
		if runErr != nil {
			return ports.WorkerExecution{}, fmt.Errorf("run allowlisted worker command %q: %s", command.Name, boundedRedacted(runErr.Error()))
		}
		appendWorkerOutput(&output, command.Name, commandOutput)
		if commandOutput.ExitCode != 0 {
			return ports.WorkerExecution{
				Status:  ports.WorkerOutcomeFailed,
				Summary: fmt.Sprintf("allowlisted command %q failed with exit code %d", command.Name, commandOutput.ExitCode),
				Output:  boundedRedacted(output.String()),
				Error:   fmt.Sprintf("command %q failed: exit code %d", command.Name, commandOutput.ExitCode),
			}, nil
		}
	}

	execution := ports.WorkerExecution{
		Status:  ports.WorkerOutcomeCompleted,
		Summary: fmt.Sprintf("completed %d allowlisted worker command(s)", len(r.commands)),
		Output:  boundedRedacted(output.String()),
	}
	collected := ports.CollectedTaskResult{
		Task: request.Task,
		Attempts: []ports.CollectedAttempt{{
			Attempt:   request.Attempt,
			Execution: execution,
		}},
		FinalExecution: execution,
	}
	report, err := r.validator.Validate(runCtx, ports.ValidationRequest{
		Task:    request.Task.Instructions,
		Plan:    ports.ExecutionPlan{Summary: request.Task.Title, Subtasks: []ports.PlannedSubtask{request.Task}},
		Results: []ports.CollectedTaskResult{collected},
	})
	if err != nil {
		return ports.WorkerExecution{}, fmt.Errorf("validate subtask %q execution: %w", request.Task.ID, err)
	}
	if !report.Passed {
		issues := make([]string, 0, len(report.Issues))
		for _, issue := range report.Issues {
			if strings.TrimSpace(issue) != "" {
				issues = append(issues, boundedRedacted(issue))
			}
		}
		if len(issues) == 0 {
			issues = append(issues, "validator did not pass")
		}
		return ports.WorkerExecution{
			Status:  ports.WorkerOutcomeFailed,
			Summary: "worker output did not pass validation",
			Output:  execution.Output,
			Error:   strings.Join(issues, "; "),
		}, nil
	}
	return execution, nil
}

// runCommand keeps command policy independent from the execution backend. With
// a sandbox configured, only the selected worktree is exposed and argv is sent
// literally to the sandbox; otherwise local worktree execution is retained.
func (r *WorkerRuntime) runCommand(ctx context.Context, worktreePath string, argv []string) (CommandOutput, error) {
	if r.sandbox == nil {
		return r.runner.RunInDirectory(ctx, worktreePath, append([]string(nil), argv...))
	}
	if r.sandboxImage == "" {
		return CommandOutput{}, errors.New("worker sandbox image is required")
	}
	result, err := r.sandbox.Run(ctx, ports.SandboxRunRequest{
		RootFS:         r.sandboxImage,
		ProjectRoot:    worktreePath,
		WorkDir:        "/workspace",
		NoNetwork:      true,
		ResourceLimits: r.sandboxLimits,
		Timeout:        r.timeout,
		Argv:           append([]string(nil), argv...),
	})
	if err != nil {
		return CommandOutput{}, err
	}
	return CommandOutput{ExitCode: result.ExitCode, Stdout: string(result.Stdout), Stderr: string(result.Stderr)}, nil
}

func validateWorkerCommands(commands []ValidationCommand) ([]ValidationCommand, error) {
	if len(commands) == 0 {
		return nil, errors.New("worker runtime requires at least one allowlisted command")
	}
	fixed := make([]ValidationCommand, len(commands))
	names := make(map[string]struct{}, len(commands))
	for i, command := range commands {
		if len(command.Argv) == 0 || strings.TrimSpace(command.Argv[0]) == "" {
			return nil, fmt.Errorf("worker command %d has no executable", i+1)
		}
		if err := ValidateCommand(command.Argv); err != nil {
			return nil, fmt.Errorf("invalid worker command %d: %w", i+1, err)
		}
		if isGitMergeOrWorktreeRemove(command.Argv) {
			return nil, fmt.Errorf("worker command %d may not merge or remove a Git worktree", i+1)
		}
		name := strings.TrimSpace(command.Name)
		if name == "" {
			name = command.Argv[0]
		}
		if strings.ContainsAny(name, "\x00\r\n") {
			return nil, fmt.Errorf("worker command %d has an invalid name", i+1)
		}
		if _, exists := names[name]; exists {
			return nil, fmt.Errorf("worker command name %q is duplicated", name)
		}
		names[name] = struct{}{}
		fixed[i] = ValidationCommand{Name: name, Argv: append([]string(nil), command.Argv...)}
	}
	return fixed, nil
}

func isGitMergeOrWorktreeRemove(argv []string) bool {
	command := strings.ToLower(filepath.Base(strings.ReplaceAll(argv[0], `\`, "/")))
	if strings.HasSuffix(command, ".exe") {
		command = strings.TrimSuffix(command, ".exe")
	}
	if command != "git" {
		return false
	}
	for index, arg := range argv[1:] {
		if arg == "merge" {
			return true
		}
		if arg == "worktree" && index+2 < len(argv) && argv[index+2] == "remove" {
			return true
		}
	}
	return false
}

func appendWorkerOutput(builder *strings.Builder, name string, output CommandOutput) {
	if builder.Len() > 0 {
		builder.WriteByte('\n')
	}
	builder.WriteString("[")
	builder.WriteString(name)
	builder.WriteString("]")
	if text := strings.TrimSpace(output.Stdout); text != "" {
		builder.WriteByte('\n')
		builder.WriteString(text)
	}
	if text := strings.TrimSpace(output.Stderr); text != "" {
		builder.WriteString("\nstderr: ")
		builder.WriteString(text)
	}
	if output.StdoutTruncated || output.StderrTruncated {
		builder.WriteString("\noutput truncated")
	}
}

// The standard process runner satisfies the worktree-scoped execution contract.
var _ WorktreeCommandRunner = ExecCommandRunner{}
