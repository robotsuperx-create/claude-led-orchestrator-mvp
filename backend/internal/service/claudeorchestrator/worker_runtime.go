package claudeorchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/writerskills"
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
	// runMu serializes everything that touches the shared run worktree:
	// applying model edits, integrating agent work, and running checks.
	// Agent coding itself happens in each agent's own worktree, outside it.
	runMu sync.Mutex
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
	worktreeRef := request.Task.Metadata[ports.SubtaskMetadataKeyWorktreePath]
	subject := fmt.Sprintf("subtask %q", request.Task.ID)

	var output strings.Builder
	authored := ""
	var agentWork ports.AgentWork
	if r.agents != nil {
		// The agent codes in its own worktree, so independent subtasks may do
		// this part at the same time, each within its own timeout.
		implementCtx, cancelImplement := context.WithTimeout(ctx, r.timeout)
		worktreePath, err := verifyWorktree(implementCtx, r.worktrees, r.projectRoot, worktreeRef, subject)
		if err != nil {
			cancelImplement()
			return ports.WorkerExecution{}, err
		}
		agentWork, err = r.agents.Implement(implementCtx, ports.AgentImplementRequest{
			WorktreePath:    worktreePath,
			Task:            request.Task,
			Attempt:         request.Attempt,
			PreviousFailure: request.PreviousFailure,
		})
		cancelImplement()
		if failed, ok := agentAttemptExecution(err); ok {
			return failed, nil
		}
		if err != nil {
			return ports.WorkerExecution{}, err
		}
	}

	// From here on the shared run worktree changes, one subtask at a time.
	r.runMu.Lock()
	defer r.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	worktreePath, err := verifyWorktree(runCtx, r.worktrees, r.projectRoot, worktreeRef, subject)
	if err != nil {
		return ports.WorkerExecution{}, err
	}
	if err := runCtx.Err(); err != nil {
		return ports.WorkerExecution{}, err
	}
	if r.agents != nil {
		err := r.agents.Integrate(runCtx, worktreePath, agentWork)
		if failed, ok := agentAttemptExecution(err); ok {
			return failed, nil
		}
		if err != nil {
			return ports.WorkerExecution{}, err
		}
		authored = agentWork.Summary
		output.WriteString("[agent]\n")
		output.WriteString(agentWork.Summary)
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
// picks files to read, then proposes edits, which are applied in the verified
// worktree. Failures the model can fix are *authorAttemptError.
func (r *WorkerRuntime) authorCode(ctx context.Context, worktreePath string, request ports.WorkerRequest) (string, error) {
	return authorInWorktree(ctx, r.runner, r.author, worktreePath, request)
}

// authorInWorktree is authorCode for any trusted worktree: the shared run
// worktree, or a model writer's own worktree.
func authorInWorktree(ctx context.Context, runner WorktreeCommandRunner, author ports.CodeAuthor, worktreePath string, request ports.WorkerRequest) (string, error) {
	files, err := listRepositoryFiles(ctx, runner, worktreePath)
	if err != nil {
		return "", err
	}
	authorRequest := ports.CodeAuthorRequest{
		Provider:        request.Task.Provider,
		Tier:            request.Task.Tier,
		Title:           request.Task.Title,
		Brief:           request.Task.Brief,
		Instructions:    request.Task.Instructions,
		Attempt:         request.Attempt,
		PreviousFailure: request.PreviousFailure,
		RepositoryFiles: files,
		Skills:          writerskills.Guidance(request.Task.Skills),
	}
	selection, err := author.SelectFiles(ctx, authorRequest)
	if err != nil {
		return "", &authorAttemptError{stage: "select files to read", err: err}
	}
	authorRequest.Files, err = readSnapshots(worktreePath, selection.Paths, files)
	if err != nil {
		return "", err
	}
	// The writer works in short replies, each applied before the next, so a
	// large subtask never has to fit in one reply's output limit.
	var (
		proposal ports.EditProposal
		applied  appliedEdits
		progress []string
	)
	for turn := 1; turn <= maxAuthorTurns; turn++ {
		authorRequest.Turn = turn
		authorRequest.Progress = progress
		reply, replyErr := author.ProposeEdits(ctx, authorRequest)
		if errors.Is(replyErr, ports.ErrReplyCutOff) && turn < maxAuthorTurns {
			// The reply was lost; ask again for a smaller one.
			authorRequest.PreviousFailure = "Your last reply was cut off at the output limit and was discarded. Write less in each reply: at most two files, and set more to true."
			continue
		}
		if replyErr != nil {
			if len(applied.Written)+len(applied.Deleted) > 0 && turn > 1 {
				// Earlier replies already wrote work; keep it and let the
				// validator decide whether the subtask is done.
				break
			}
			return "", &authorAttemptError{stage: "propose edits", err: replyErr}
		}
		if len(reply.Edits) == 0 && len(reply.Read) > 0 && turn < maxAuthorTurns {
			// The writer asked to see more before writing: show it, then ask again.
			selection.Paths = appendNew(selection.Paths, reply.Read...)
			authorRequest.Files, err = readSnapshots(worktreePath, appendNew(append([]string(nil), selection.Paths...), applied.Written...), currentFiles(ctx, runner, worktreePath, files))
			if err != nil {
				return "", err
			}
			continue
		}
		if err := requireReadBeforeRewrite(worktreePath, reply.Edits, authorRequest.Files); err != nil {
			return "", &authorAttemptError{stage: "the proposed edits were rejected", err: err}
		}
		done, err := applyEdits(worktreePath, reply.Edits)
		if err != nil {
			return "", &authorAttemptError{stage: "the proposed edits were rejected", err: err}
		}
		applied.Written = appendNew(applied.Written, done.Written...)
		applied.Deleted = appendNew(applied.Deleted, done.Deleted...)
		proposal.Summary = joinSummary(proposal.Summary, reply.Summary)
		if !reply.More || len(reply.Edits) == 0 {
			break
		}
		progress = append(progress, boundedText(reply.Summary, 300))
		authorRequest.PreviousFailure = request.PreviousFailure
		// The next reply sees the files as they are now, its own new ones included.
		authorRequest.Files, err = readSnapshots(worktreePath, appendNew(append([]string(nil), selection.Paths...), done.Written...), currentFiles(ctx, runner, worktreePath, files))
		if err != nil {
			return "", err
		}
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

// maxAuthorTurns bounds the replies one attempt may take.
const maxAuthorTurns = 8

// currentFiles lists the repository again after a reply wrote new files,
// keeping the earlier list if Git cannot answer.
func currentFiles(ctx context.Context, runner WorktreeCommandRunner, worktreePath string, earlier []string) []string {
	files, err := listRepositoryFiles(ctx, runner, worktreePath)
	if err != nil {
		return earlier
	}
	return files
}

// appendNew appends the values not already in list.
func appendNew(list []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(list, value) {
			list = append(list, value)
		}
	}
	return list
}

// joinSummary joins the summaries of an attempt's replies.
func joinSummary(earlier, next string) string {
	next = strings.TrimSpace(next)
	switch {
	case next == "":
		return earlier
	case earlier == "":
		return next
	default:
		return earlier + " " + next
	}
}

// boundedText keeps at most limit runes of text.
func boundedText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}

// listRepositoryFiles lists tracked and untracked, non-ignored files with a
// fixed Git argv on the host. Protected paths and binary assets (images,
// fonts, archives, ...) are omitted, and the list is bounded.
func listRepositoryFiles(ctx context.Context, runner WorktreeCommandRunner, worktreePath string) ([]string, error) {
	output, err := runner.RunInDirectory(ctx, worktreePath, []string{"git", "ls-files", "-z", "--cached", "--others", "--exclude-standard"})
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
		if _, dup := seen[clean]; dup || !listedForModel(clean) {
			continue
		}
		seen[clean] = struct{}{}
		files = append(files, clean)
	}
	return files, nil
}

// agentAttemptExecution turns a retryable agent failure into a failed attempt.
func agentAttemptExecution(err error) (ports.WorkerExecution, bool) {
	if !errors.Is(err, ports.ErrAgentAttemptFailed) {
		return ports.WorkerExecution{}, false
	}
	return ports.WorkerExecution{
		Status:  ports.WorkerOutcomeFailed,
		Summary: "the agent did not produce an applicable change",
		Error:   boundedRedacted(err.Error()),
	}, true
}
