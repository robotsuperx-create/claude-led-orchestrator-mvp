package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// CommandRunner runs an executable with an explicit working directory and
// argument vector. Implementations must not interpret Args as shell text.
type CommandRunner interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}

// Manager manages linked worktrees for one project at a time.
type Manager struct {
	runner      CommandRunner
	managedRoot string
}

// NewManager constructs a manager. A nil runner selects the direct Git
// executable runner; it never invokes a shell. Worktree paths must be strictly
// inside the project root.
func NewManager(runner CommandRunner) *Manager {
	return NewManagerWithManagedRoot(runner, "")
}

// NewManagerWithManagedRoot additionally accepts worktrees strictly inside
// managedRoot, an AO-owned directory such as ~/.ao/worktrees/claude-orchestrator.
// Status and Remove still require Git to list the path as a worktree of the
// project, so the managed root never widens which repository is touched.
func NewManagerWithManagedRoot(runner CommandRunner, managedRoot string) *Manager {
	if runner == nil {
		runner = execRunner{}
	}
	managed := strings.TrimSpace(managedRoot)
	if managed != "" {
		if abs, err := filepath.Abs(managed); err == nil {
			managed = filepath.Clean(abs)
		}
	}
	return &Manager{runner: runner, managedRoot: managed}
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

var safeBranchCharacters = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// Create adds a new branch and worktree. The destination must be an absent
// directory strictly beneath the supplied repository root.
func (m *Manager) Create(ctx context.Context, request ports.WorktreeCreateRequest) (ports.WorktreeCreateResult, error) {
	root, err := canonicalProjectRoot(request.ProjectRoot)
	if err != nil {
		return ports.WorktreeCreateResult{}, err
	}
	if err := validateBranch(request.Branch); err != nil {
		return ports.WorktreeCreateResult{}, err
	}
	if err := m.requireRepositoryRoot(ctx, root); err != nil {
		return ports.WorktreeCreateResult{}, err
	}
	path, err := m.worktreePath(root, request.Path, false)
	if err != nil {
		return ports.WorktreeCreateResult{}, err
	}
	if _, err := os.Lstat(path); err == nil {
		return ports.WorktreeCreateResult{}, fmt.Errorf("worktree: destination already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return ports.WorktreeCreateResult{}, fmt.Errorf("worktree: inspect destination: %w", err)
	}

	// Arguments remain distinct all the way to exec.CommandContext; no shell
	// string is constructed or interpolated here.
	if output, err := m.runner.Run(ctx, root, "worktree", "add", "-b", request.Branch, path); err != nil {
		return ports.WorktreeCreateResult{}, commandError("create worktree", output, err)
	}
	return ports.WorktreeCreateResult{ProjectRoot: root, Path: path, Branch: request.Branch}, nil
}

// Remove removes a worktree only when explicitly called. It is deliberately
// not used as cleanup by Create, Status, or any error path.
func (m *Manager) Remove(ctx context.Context, request ports.WorktreeRemoveRequest) (ports.WorktreeRemoveResult, error) {
	root, err := canonicalProjectRoot(request.ProjectRoot)
	if err != nil {
		return ports.WorktreeRemoveResult{}, err
	}
	if err := m.requireRepositoryRoot(ctx, root); err != nil {
		return ports.WorktreeRemoveResult{}, err
	}
	path, err := m.worktreePath(root, request.Path, true)
	if err != nil {
		return ports.WorktreeRemoveResult{}, err
	}
	if _, err := m.registeredWorktree(ctx, root, path); err != nil {
		return ports.WorktreeRemoveResult{}, err
	}
	args := []string{"worktree", "remove"}
	if request.Force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if output, err := m.runner.Run(ctx, root, args...); err != nil {
		return ports.WorktreeRemoveResult{}, commandError("remove worktree", output, err)
	}
	return ports.WorktreeRemoveResult{Path: path, Removed: true}, nil
}

// Status reports porcelain status for a registered worktree under ProjectRoot.
func (m *Manager) Status(ctx context.Context, request ports.WorktreeStatusRequest) (ports.WorktreeStatusResult, error) {
	root, err := canonicalProjectRoot(request.ProjectRoot)
	if err != nil {
		return ports.WorktreeStatusResult{}, err
	}
	if err := m.requireRepositoryRoot(ctx, root); err != nil {
		return ports.WorktreeStatusResult{}, err
	}
	path, err := m.worktreePath(root, request.Path, true)
	if err != nil {
		return ports.WorktreeStatusResult{}, err
	}
	registered, err := m.registeredWorktree(ctx, root, path)
	if err != nil {
		return ports.WorktreeStatusResult{}, err
	}
	output, err := m.runner.Run(ctx, path, "status", "--porcelain=v1", "--branch")
	if err != nil {
		return ports.WorktreeStatusResult{}, commandError("read worktree status", output, err)
	}
	statusText := strings.TrimRight(string(output), "\r\n")
	clean := true
	for _, line := range strings.Split(statusText, "\n") {
		if line != "" && !strings.HasPrefix(line, "## ") {
			clean = false
			break
		}
	}
	return ports.WorktreeStatusResult{Path: path, Branch: registered.branch, Clean: clean, Output: statusText}, nil
}

func canonicalProjectRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("worktree: project root is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("worktree: resolve project root: %w", err)
	}
	root, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("worktree: resolve project root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("worktree: inspect project root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("worktree: project root must be a directory")
	}
	return filepath.Clean(root), nil
}

// worktreePath checks lexical containment first, then canonical containment so
// an existing symlink cannot escape the allowed roots. For creation, the target
// itself must not exist and its parent must already exist.
func (m *Manager) worktreePath(root, requested string, mustExist bool) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", errors.New("worktree: path is required")
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if err := m.ensureAllowed(root, candidate); err != nil {
		return "", err
	}

	if mustExist {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("worktree: resolve worktree path: %w", err)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", fmt.Errorf("worktree: inspect worktree path: %w", err)
		}
		if !info.IsDir() {
			return "", errors.New("worktree: path must be a directory")
		}
		if err := m.ensureAllowed(root, resolved); err != nil {
			return "", err
		}
		return filepath.Clean(resolved), nil
	}

	parent, err := filepath.EvalSymlinks(filepath.Dir(candidate))
	if err != nil {
		return "", fmt.Errorf("worktree: resolve destination parent (it must exist): %w", err)
	}
	resolved := filepath.Join(parent, filepath.Base(candidate))
	if err := m.ensureAllowed(root, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// ensureAllowed accepts a path strictly inside the project root (outside its
// .git directory) or strictly inside the configured managed root.
func (m *Manager) ensureAllowed(root, path string) error {
	projectErr := ensureWithinRoot(root, path)
	if projectErr == nil {
		if hasGitDirComponent(root, path) {
			return errors.New("worktree: path cannot be inside the project Git directory")
		}
		return nil
	}
	if m.managedRoot == "" {
		return projectErr
	}
	for _, managed := range m.managedRootForms() {
		if ensureWithinRoot(managed, path) == nil {
			return nil
		}
	}
	return fmt.Errorf("worktree: path must be strictly inside project root %s or managed root %s", root, m.managedRoot)
}

// managedRootForms returns the configured managed root and, when it exists,
// its symlink-resolved form, so lexical and canonical checks both match.
func (m *Manager) managedRootForms() []string {
	forms := []string{m.managedRoot}
	if resolved, err := filepath.EvalSymlinks(m.managedRoot); err == nil && filepath.Clean(resolved) != m.managedRoot {
		forms = append(forms, filepath.Clean(resolved))
	}
	return forms
}

func ensureWithinRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("worktree: compare project path: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("worktree: path must be strictly inside project root %s", root)
	}
	return nil
}

func hasGitDirComponent(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		if component == ".git" {
			return true
		}
	}
	return false
}

func validateBranch(branch string) error {
	if !safeBranchCharacters.MatchString(branch) || branch == "@" || strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.Contains(branch, "@{") || strings.HasSuffix(branch, "/") {
		return fmt.Errorf("worktree: unsafe branch name %q", branch)
	}
	for _, component := range strings.Split(branch, "/") {
		if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".") || strings.HasSuffix(component, ".lock") {
			return fmt.Errorf("worktree: unsafe branch name %q", branch)
		}
	}
	return nil
}

func (m *Manager) requireRepositoryRoot(ctx context.Context, root string) error {
	output, err := m.runner.Run(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return commandError("verify project root", output, err)
	}
	reported := strings.TrimSpace(string(output))
	if reported == "" {
		return errors.New("worktree: Git returned an empty repository root")
	}
	canonical, err := filepath.EvalSymlinks(reported)
	if err != nil {
		return fmt.Errorf("worktree: resolve Git repository root: %w", err)
	}
	if filepath.Clean(canonical) != root {
		return fmt.Errorf("worktree: project root %s is not the Git repository root %s", root, canonical)
	}
	return nil
}

type worktreeRecord struct {
	path   string
	branch string
}

func (m *Manager) registeredWorktree(ctx context.Context, root, target string) (worktreeRecord, error) {
	output, err := m.runner.Run(ctx, root, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return worktreeRecord{}, commandError("list worktrees", output, err)
	}
	var record worktreeRecord
	for _, field := range strings.Split(string(output), "\x00") {
		if strings.HasPrefix(field, "worktree ") {
			if record.path != "" {
				if filepath.Clean(record.path) == target {
					return record, nil
				}
				record = worktreeRecord{}
			}
			record.path = strings.TrimPrefix(field, "worktree ")
		} else if strings.HasPrefix(field, "branch ") {
			record.branch = strings.TrimPrefix(field, "branch refs/heads/")
		} else if field == "" && record.path != "" {
			if filepath.Clean(record.path) == target {
				return record, nil
			}
			record = worktreeRecord{}
		}
	}
	if record.path != "" && filepath.Clean(record.path) == target {
		return record, nil
	}
	return worktreeRecord{}, fmt.Errorf("worktree: path is not a registered worktree of project root: %s", target)
}

func commandError(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("worktree: %s: %w", action, err)
	}
	return fmt.Errorf("worktree: %s: %w: %s", action, err, message)
}

var _ ports.WorktreeManager = (*Manager)(nil)
