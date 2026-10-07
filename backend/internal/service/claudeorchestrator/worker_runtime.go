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

	// ErrWorkerRuntimeUnavailable reports a nil or unconfigured worker runtime.
	ErrWorkerRuntimeUnavailable = errors.New("claude orchestrator worker runtime is unavailable")
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
	// Author, when supplied, asks a model to write the code for each subtask
	// before the fixed commands run. Its proposals are validated and applied
	// only inside the verified worktree. Timeout must leave room for it.
	Author ports.CodeAuthor
	// Agents, when supplied, hands each subtask to a real AO agent session
	// instead of Author. The agent's committed work is fast-forwarded into the
	// verified worktree before the fixed commands and validator run.
	Agents ports.AgentImplementer
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
	author        ports.CodeAuthor
	agents        ports.AgentImplementer
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
		author:        config.Author,
		agents:        config.Agents,
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
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	worktreePath, err := verifyWorktree(runCtx, r.worktrees, r.projectRoot,
		request.Task.Metadata[ports.SubtaskMetadataKeyWorktreePath], fmt.Sprintf("subtask %q", request.Task.ID))
	if err != nil {
		return ports.WorkerExecution{}, err
	}
	if err := runCtx.Err(); err != nil {
		return ports.WorkerExecution{}, err
	}

	var output strings.Builder
	authored := ""
	if r.agents != nil {
		summary, err := r.agents.Implement(runCtx, ports.AgentImplementRequest{
			WorktreePath:    worktreePath,
			Task:            request.Task,
			Attempt:         request.Attempt,
			PreviousFailure: request.PreviousFailure,
		})
		if errors.Is(err, ports.ErrAgentAttemptFailed) {
			return ports.WorkerExecution{
				Status:  ports.WorkerOutcomeFailed,
				Summary: "the agent did not produce an applicable change",
				Error:   boundedRedacted(err.Error()),
			}, nil
		}
		if err != nil {
			return ports.WorkerExecution{}, err
		}
		authored = summary
		output.WriteString("[agent]\n")
		output.WriteString(summary)
	} else if r.author != nil {
		summary, err := r.authorCode(runCtx, worktreePath, request)
		var attemptErr *authorAttemptError
		if errors.As(err, &attemptErr) {
			// A model or proposal failure is a failed attempt, not an
			// infrastructure error, so the service retries with this reason.
			return ports.WorkerExecution{
				Status:  ports.WorkerOutcomeFailed,
				Summary: "code author did not produce an applicable change",
				Error:   boundedRedacted(attemptErr.Error()),
			}, nil
		}
		if err != nil {
			return ports.WorkerExecution{}, err
		}
		authored = summary
		output.WriteString("[author]\n")
		output.WriteString(summary)
	}
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

	summary := fmt.Sprintf("completed %d allowlisted worker command(s)", len(r.commands))
	if authored != "" {
		summary = authored + "; " + summary
	}
	execution := ports.WorkerExecution{
		Status:  ports.WorkerOutcomeCompleted,
		Summary: boundedRedacted(summary),
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
	command = strings.TrimSuffix(command, ".exe")
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

// authorAttemptError marks a failure the coding model can act on in a retry,
// as opposed to an infrastructure fault.
type authorAttemptError struct {
	stage string
	err   error
}

func (e *authorAttemptError) Error() string { return e.stage + ": " + e.err.Error() }
func (e *authorAttemptError) Unwrap() error { return e.err }

// authorCode runs the two-step authoring exchange for one attempt: the model
// picks files to read, then proposes whole-file edits, which are applied in
// the verified worktree. Failures the model can fix are *authorAttemptError.
func (r *WorkerRuntime) authorCode(ctx context.Context, worktreePath string, request ports.WorkerRequest) (string, error) {
	files, err := r.repositoryFiles(ctx, worktreePath)
	if err != nil {
		return "", err
	}
	authorRequest := ports.CodeAuthorRequest{
		Provider:        request.Task.Provider,
		Title:           request.Task.Title,
		Instructions:    request.Task.Instructions,
		Attempt:         request.Attempt,
		PreviousFailure: request.PreviousFailure,
		RepositoryFiles: files,
	}
	selection, err := r.author.SelectFiles(ctx, authorRequest)
	if err != nil {
		return "", &authorAttemptError{stage: "select files to read", err: err}
	}
	authorRequest.Files, err = readSnapshots(worktreePath, selection.Paths, files)
	if err != nil {
		return "", err
	}
	proposal, err := r.author.ProposeEdits(ctx, authorRequest)
	if err != nil {
		return "", &authorAttemptError{stage: "propose edits", err: err}
	}
	applied, err := applyEdits(worktreePath, proposal.Edits)
	if err != nil {
		return "", &authorAttemptError{stage: "the proposed edits were rejected", err: err}
	}
	var parts []string
	if text := strings.TrimSpace(proposal.Summary); text != "" {
		parts = append(parts, text)
	}
	switch {
	case len(applied.Written) == 0 && len(applied.Deleted) == 0:
		parts = append(parts, "no files changed")
	default:
		if len(applied.Written) > 0 {
			parts = append(parts, "wrote "+strings.Join(applied.Written, ", "))
		}
		if len(applied.Deleted) > 0 {
			parts = append(parts, "deleted "+strings.Join(applied.Deleted, ", "))
		}
	}
	return strings.Join(parts, "; "), nil
}

// repositoryFiles lists tracked and untracked, non-ignored files with a fixed
// Git argv on the host. Protected paths are omitted, and the list is bounded.
func (r *WorkerRuntime) repositoryFiles(ctx context.Context, worktreePath string) ([]string, error) {
	output, err := r.runner.RunInDirectory(ctx, worktreePath, []string{"git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"})
	if err != nil {
		return nil, fmt.Errorf("list repository files: %s", boundedRedacted(err.Error()))
	}
	if output.ExitCode != 0 {
		return nil, fmt.Errorf("list repository files: git exited with code %d", output.ExitCode)
	}
	entries := strings.Split(output.Stdout, "\x00")
	if output.StdoutTruncated && len(entries) > 0 {
		entries = entries[:len(entries)-1] // the last entry may be cut short
	}
	files := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if len(files) >= maxRepositoryFilesListed {
			break
		}
		clean, err := cleanRepoPath(entry)
		if err != nil {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		files = append(files, clean)
	}
	return files, nil
}
