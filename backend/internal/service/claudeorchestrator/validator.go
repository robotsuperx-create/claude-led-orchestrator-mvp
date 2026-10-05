package claudeorchestrator

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const MaxValidationOutputBytes = 64 * 1024

var (
	_ ports.Validator = (*Validator)(nil)

	ErrValidatorUnavailable = errors.New("Claude orchestrator validator is unavailable")
)

// ValidationCommand is a fixed command definition. Argv is passed directly to
// the runner; it is never joined, parsed, or interpreted as a shell command.
type ValidationCommand struct {
	Name string
	Argv []string
}

// CommandOutput is the typed result returned by a CommandRunner. Output is
// bounded again by Validator, even when a custom runner does not enforce limits.
type CommandOutput struct {
	ExitCode        int
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
}

// CommandRunner executes one argv vector without a shell.
type CommandRunner interface {
	Run(ctx context.Context, argv []string) (CommandOutput, error)
}

// WorktreeCommandRunner runs argv directly with the supplied working directory.
// It extends CommandRunner so the same implementation can serve validators and
// worktree-scoped workers without passing a directory through model-controlled argv.
type WorktreeCommandRunner interface {
	CommandRunner
	RunInDirectory(ctx context.Context, directory string, argv []string) (CommandOutput, error)
}

// CommandRunnerFunc adapts a function into a CommandRunner.
type CommandRunnerFunc func(context.Context, []string) (CommandOutput, error)

// Run executes argv directly and returns its typed result.
func (f CommandRunnerFunc) Run(ctx context.Context, argv []string) (CommandOutput, error) {
	return f(ctx, argv)
}

// CommandResult records a bounded and redacted result for one configured check.
type CommandResult struct {
	Name            string
	Argv            []string
	ExitCode        int
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
	Error           string
}

// ValidationResult is the typed aggregate returned by Check.
type ValidationResult struct {
	Passed   bool
	Commands []CommandResult
	Issues   []string
}

// Validator runs a fixed set of allowlisted validation commands. The timeout
// bounds the whole validation pass, not each command independently.
type Validator struct {
	commands []ValidationCommand
	timeout  time.Duration
	runner   CommandRunner
}

// NewValidator constructs a validator from a fixed argv allowlist and an
// injected runner. Invalid entries (including shell/interpreter commands) are
// rejected before any validation request can cause execution.
func NewValidator(commands []ValidationCommand, timeout time.Duration, runner CommandRunner) (*Validator, error) {
	if len(commands) == 0 {
		return nil, errors.New("validator requires at least one allowlisted command")
	}
	if timeout <= 0 {
		return nil, errors.New("validator timeout must be positive")
	}
	if runner == nil {
		return nil, errors.New("validator command runner is required")
	}

	fixed := make([]ValidationCommand, len(commands))
	names := make(map[string]struct{}, len(commands))
	for i, command := range commands {
		if len(command.Argv) == 0 || strings.TrimSpace(command.Argv[0]) == "" {
			return nil, fmt.Errorf("validator command %d has no executable", i+1)
		}
		// A command supplied as one shell string (for example "go test ./...")
		// is not argv. Requiring an executable token prevents accidental shell
		// string configuration while still permitting spaces in later arguments.
		if strings.IndexFunc(command.Argv[0], func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }) >= 0 {
			return nil, fmt.Errorf("validator command %d executable must be a single argv token", i+1)
		}
		if err := ValidateCommand(command.Argv); err != nil {
			return nil, fmt.Errorf("invalid validator command %d: %w", i+1, err)
		}
		name := strings.TrimSpace(command.Name)
		if name == "" {
			name = command.Argv[0]
		}
		if strings.ContainsAny(name, "\x00\r\n") {
			return nil, fmt.Errorf("validator command %d has an invalid name", i+1)
		}
		if _, exists := names[name]; exists {
			return nil, fmt.Errorf("validator command name %q is duplicated", name)
		}
		names[name] = struct{}{}
		fixed[i] = ValidationCommand{Name: name, Argv: append([]string(nil), command.Argv...)}
	}
	return &Validator{commands: fixed, timeout: timeout, runner: runner}, nil
}

// Validate implements ports.Validator. Validation failures are reported as a
// negative report; infrastructure and cancellation details are preserved as
// bounded issues rather than being interpreted as successful validation.
func (v *Validator) Validate(ctx context.Context, request ports.ValidationRequest) (ports.ValidationReport, error) {
	result, err := v.Check(ctx, request)
	if err != nil {
		return ports.ValidationReport{}, err
	}
	return ports.ValidationReport{Passed: result.Passed, Issues: result.Issues}, nil
}

// Check runs the configured commands and returns their structured results.
// Values from request are intentionally not interpolated into command argv.
func (v *Validator) Check(ctx context.Context, _ ports.ValidationRequest) (ValidationResult, error) {
	if v == nil || v.runner == nil || len(v.commands) == 0 || v.timeout <= 0 {
		return ValidationResult{}, ErrValidatorUnavailable
	}
	if ctx == nil {
		return ValidationResult{}, errors.New("validator context is required")
	}

	checkCtx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	result := ValidationResult{Passed: true, Commands: make([]CommandResult, 0, len(v.commands))}
	for index, command := range v.commands {
		if err := checkCtx.Err(); err != nil {
			for _, pending := range v.commands[index:] {
				message := fmt.Sprintf("command %q was not run: %s", pending.Name, err)
				result.Commands = append(result.Commands, CommandResult{Name: pending.Name, Argv: append([]string(nil), pending.Argv...), Error: message})
				result.Issues = append(result.Issues, message)
			}
			result.Passed = false
			break
		}

		output, runErr := v.runner.Run(checkCtx, append([]string(nil), command.Argv...))
		if runErr == nil && checkCtx.Err() != nil {
			runErr = checkCtx.Err()
		}
		commandResult := CommandResult{
			Name:            command.Name,
			Argv:            append([]string(nil), command.Argv...),
			ExitCode:        output.ExitCode,
			Stdout:          boundedRedacted(output.Stdout),
			Stderr:          boundedRedacted(output.Stderr),
			StdoutTruncated: output.StdoutTruncated || len(output.Stdout) > MaxValidationOutputBytes,
			StderrTruncated: output.StderrTruncated || len(output.Stderr) > MaxValidationOutputBytes,
		}
		failed := false
		if runErr != nil {
			commandResult.Error = boundedRedacted(runErr.Error())
			failed = true
		}
		if output.ExitCode != 0 {
			failed = true
		}
		result.Commands = append(result.Commands, commandResult)
		if failed {
			result.Passed = false
			result.Issues = append(result.Issues, commandIssue(commandResult))
		}
		if checkCtx.Err() != nil {
			// The current command's timeout/error is already represented above.
			// The next loop iteration records any remaining checks as not run.
			continue
		}
	}
	return result, nil
}

func commandIssue(result CommandResult) string {
	var parts []string
	if result.Error != "" {
		parts = append(parts, result.Error)
	}
	if result.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit code %d", result.ExitCode))
	}
	if result.Stdout != "" {
		parts = append(parts, "stdout: "+result.Stdout)
	}
	if result.Stderr != "" {
		parts = append(parts, "stderr: "+result.Stderr)
	}
	if result.StdoutTruncated {
		parts = append(parts, "stdout truncated")
	}
	if result.StderrTruncated {
		parts = append(parts, "stderr truncated")
	}
	return fmt.Sprintf("validation command %q failed: %s", result.Name, strings.Join(parts, "; "))
}

func boundedRedacted(value string) string {
	value = RedactSecrets(value)
	if len(value) <= MaxValidationOutputBytes {
		return value
	}
	limit := MaxValidationOutputBytes
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit]
}

// ExecCommandRunner is the production runner. It always passes argv directly
// to exec.CommandContext and bounds output while the child process is running.
type ExecCommandRunner struct{}

// Run executes the supplied argv vector directly, never through a shell.
func (ExecCommandRunner) Run(ctx context.Context, argv []string) (CommandOutput, error) {
	return ExecCommandRunner{}.RunInDirectory(ctx, "", argv)
}

// RunInDirectory executes argv directly from directory, never through a shell.
func (ExecCommandRunner) RunInDirectory(ctx context.Context, directory string, argv []string) (CommandOutput, error) {
	if len(argv) == 0 {
		return CommandOutput{}, errors.New("command argv is empty")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = directory
	stdout := &boundedBuffer{limit: MaxValidationOutputBytes}
	stderr := &boundedBuffer{limit: MaxValidationOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	output := CommandOutput{}
	err := cmd.Run()
	output.Stdout, output.StdoutTruncated = stdout.String(), stdout.truncated
	output.Stderr, output.StderrTruncated = stderr.String(), stderr.truncated
	if err == nil {
		return output, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		output.ExitCode = exitErr.ExitCode()
		return output, nil
	}
	return output, fmt.Errorf("execute %q: %w", argv[0], err)
}

type boundedBuffer struct {
	builder   strings.Builder
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := b.limit - b.builder.Len()
	if remaining <= 0 {
		if originalLength > 0 {
			b.truncated = true
		}
		return originalLength, nil
	}
	if len(data) > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.builder.Write(data)
	return originalLength, nil
}

func (b *boundedBuffer) String() string { return b.builder.String() }
