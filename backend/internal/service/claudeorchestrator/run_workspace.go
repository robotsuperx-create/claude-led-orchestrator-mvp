package claudeorchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DefaultRunBranchPrefix namespaces the branches created for runs.
const DefaultRunBranchPrefix = "ao/claude-orchestrator/"

var safeRunID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// RunWorkspacesConfig configures per-run worktree provisioning.
type RunWorkspacesConfig struct {
	// ProjectRoot is the repository whose branches and worktrees are created.
	ProjectRoot string
	// ManagedRoot is the AO-owned directory that holds run worktrees, for
	// example ~/.ao/worktrees/claude-orchestrator. The WorktreeManager must
	// accept paths beneath it.
	ManagedRoot string
	// BranchPrefix defaults to DefaultRunBranchPrefix.
	BranchPrefix string
	// CommitAuthorName and CommitAuthorEmail identify run commits.
	CommitAuthorName  string
	CommitAuthorEmail string
}

// RunWorkspaces creates one linked worktree and branch per run and commits
// the run's changes to that branch when it finishes. It never merges,
// pushes, or removes worktrees.
type RunWorkspaces struct {
	worktrees   ports.WorktreeManager
	git         WorktreeCommandRunner
	projectRoot string
	runRoot     string
	prefix      string
	authorName  string
	authorEmail string
}

var _ ports.RunWorkspaceProvisioner = (*RunWorkspaces)(nil)

// NewRunWorkspaces validates configuration. git runs only fixed argv vectors.
func NewRunWorkspaces(worktrees ports.WorktreeManager, git WorktreeCommandRunner, config RunWorkspacesConfig) (*RunWorkspaces, error) {
	if worktrees == nil {
		return nil, errors.New("run workspaces require a worktree manager")
	}
	if git == nil {
		return nil, errors.New("run workspaces require a command runner")
	}
	if strings.TrimSpace(config.ProjectRoot) == "" || strings.TrimSpace(config.ManagedRoot) == "" {
		return nil, errors.New("run workspaces require a project root and a managed root")
	}
	projectRoot, err := filepath.Abs(config.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve project root: %w", err)
	}
	managedRoot, err := filepath.Abs(config.ManagedRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve managed root: %w", err)
	}
	prefix := config.BranchPrefix
	if prefix == "" {
		prefix = DefaultRunBranchPrefix
	}
	name := strings.TrimSpace(config.CommitAuthorName)
	if name == "" {
		name = "AO Claude Orchestrator"
	}
	email := strings.TrimSpace(config.CommitAuthorEmail)
	if email == "" {
		email = "claude-orchestrator@ao.invalid"
	}
	if strings.ContainsAny(name+email, "\x00\r\n") {
		return nil, errors.New("commit author must be a single line")
	}
	projectRoot = filepath.Clean(projectRoot)
	return &RunWorkspaces{
		worktrees:   worktrees,
		git:         git,
		projectRoot: projectRoot,
		// One directory per project keeps runs from different repositories apart.
		runRoot:     filepath.Join(filepath.Clean(managedRoot), projectKey(projectRoot)),
		prefix:      prefix,
		authorName:  name,
		authorEmail: email,
	}, nil
}

// projectKey is a readable, collision-resistant directory name for a project.
func projectKey(projectRoot string) string {
	sum := sha256.Sum256([]byte(projectRoot))
	base := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, filepath.Base(projectRoot))
	if len(base) > 40 {
		base = base[:40]
	}
	return base + "-" + hex.EncodeToString(sum[:])[:12]
}

// Prepare creates the run's worktree on a new branch from the project's
// current HEAD.
func (w *RunWorkspaces) Prepare(ctx context.Context, runID string) (ports.RunWorkspace, error) {
	if w == nil {
		return ports.RunWorkspace{}, errors.New("run workspaces are unavailable")
	}
	if !safeRunID.MatchString(runID) {
		return ports.RunWorkspace{}, fmt.Errorf("run ID %q cannot name a worktree", runID)
	}
	if err := os.MkdirAll(w.runRoot, 0o700); err != nil {
		return ports.RunWorkspace{}, fmt.Errorf("create run worktree directory: %w", err)
	}
	created, err := w.worktrees.Create(ctx, ports.WorktreeCreateRequest{
		ProjectRoot: w.projectRoot,
		Path:        filepath.Join(w.runRoot, runID),
		Branch:      w.prefix + runID,
	})
	if err != nil {
		return ports.RunWorkspace{}, fmt.Errorf("create run worktree: %w", err)
	}
	return ports.RunWorkspace{Path: created.Path, Branch: created.Branch}, nil
}

// Finalize commits every change in the run worktree to the run branch. Hooks
// are skipped so repository scripts never run on the host as a side effect.
func (w *RunWorkspaces) Finalize(ctx context.Context, workspace ports.RunWorkspace, result ports.OrchestrationResult) (ports.RunWorkspace, error) {
	if w == nil || workspace.Path == "" {
		return workspace, nil
	}
	status, err := w.gitIn(ctx, workspace.Path, "status", "--porcelain")
	if err != nil {
		return workspace, err
	}
	if strings.TrimSpace(status) == "" {
		return workspace, nil
	}
	if _, err := w.gitIn(ctx, workspace.Path, "add", "--all"); err != nil {
		return workspace, err
	}
	message := commitMessage(result)
	if _, err := w.gitIn(ctx, workspace.Path,
		"-c", "user.name="+w.authorName, "-c", "user.email="+w.authorEmail,
		"commit", "--no-verify", "--quiet", "-m", message); err != nil {
		return workspace, err
	}
	head, err := w.gitIn(ctx, workspace.Path, "rev-parse", "HEAD")
	if err != nil {
		return workspace, err
	}
	workspace.Commit = strings.TrimSpace(head)
	return workspace, nil
}

func (w *RunWorkspaces) gitIn(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := w.git.RunInDirectory(ctx, dir, append([]string{"git"}, args...))
	if err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], boundedRedacted(err.Error()))
	}
	if output.ExitCode != 0 {
		detail := strings.TrimSpace(output.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(output.Stdout)
		}
		return "", fmt.Errorf("git %s exited with code %d: %s", args[0], output.ExitCode, boundedRedacted(detail))
	}
	return output.Stdout, nil
}

func commitMessage(result ports.OrchestrationResult) string {
	subject := strings.TrimSpace(result.Plan.Summary)
	if subject == "" {
		subject = strings.TrimSpace(result.Task)
	}
	subject = strings.Join(strings.Fields(subject), " ")
	if runes := []rune(subject); len(runes) > 72 {
		subject = strings.TrimSpace(string(runes[:72]))
	}
	if subject == "" {
		subject = "Claude orchestrator run"
	}
	var body strings.Builder
	fmt.Fprintf(&body, "%s\n\nRun: %s\nRecommendation: %s\n", subject, result.RunID, result.MergeDecision.Decision)
	for _, reason := range result.MergeDecision.Reasons {
		fmt.Fprintf(&body, "- %s\n", strings.Join(strings.Fields(reason), " "))
	}
	return RedactSecrets(body.String())
}
