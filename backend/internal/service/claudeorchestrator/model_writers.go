package claudeorchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// modelWriterCleanupTimeout bounds removing an attempt's own worktree, which
// runs even after the attempt itself was canceled.
const modelWriterCleanupTimeout = 30 * time.Second

// ModelWriters lets a model write several of a run's independent subtasks at
// the same time, all through the same API key. Each attempt is written in a
// short-lived worktree of its own, on its own branch cut from the run's latest
// commit; the model's edits are checked and applied there exactly as they are
// in the shared run worktree, then committed. Integrate merges that branch
// into the run worktree the way agent work is merged, so the fixed commands
// and the validator still run one subtask at a time on the combined result.
//
// It never pushes, never touches a branch other than its own and the run's,
// and never force-removes a worktree: an attempt's worktree is removed only
// once it is clean.
type ModelWriters struct {
	git         WorktreeCommandRunner
	author      ports.CodeAuthor
	authorName  string
	authorEmail string
	// setup serializes adding and removing worktrees and branches, which all
	// change the same repository's worktree list.
	setup sync.Mutex
}

var _ ports.AgentImplementer = (*ModelWriters)(nil)

// NewModelWriters wires a code author to per-attempt worktrees. git runs only
// fixed argv vectors, never anything taken from a plan or a model.
func NewModelWriters(git WorktreeCommandRunner, author ports.CodeAuthor) (*ModelWriters, error) {
	if git == nil {
		return nil, errors.New("model writers require a git command runner")
	}
	if author == nil {
		return nil, errors.New("model writers require a code author")
	}
	return &ModelWriters{
		git:         git,
		author:      author,
		authorName:  defaultCommitAuthorName,
		authorEmail: defaultCommitAuthorEmail,
	}, nil
}

// Implement writes one attempt and returns once its change is committed on
// its own branch. Independent subtasks may call it concurrently. Work with
// no Branch changed nothing and has nothing to merge.
func (m *ModelWriters) Implement(ctx context.Context, request ports.AgentImplementRequest) (ports.AgentWork, error) {
	if m == nil {
		return ports.AgentWork{}, errors.New("model writers are not configured")
	}
	if request.WorktreePath == "" || strings.TrimSpace(request.Task.ID) == "" {
		return ports.AgentWork{}, errors.New("a model writer needs a run worktree and a subtask id")
	}
	scratch, branch, base, err := m.prepare(ctx, request.WorktreePath, request.Task.ID)
	if err != nil {
		return ports.AgentWork{}, err
	}
	committed := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), modelWriterCleanupTimeout)
		defer cancel()
		m.release(cleanupCtx, request.WorktreePath, scratch, branch, committed)
	}()

	summary, _, err := authorInWorktree(ctx, m.git, m.author, scratch, ports.WorkerRequest{
		Task:            request.Task,
		Attempt:         request.Attempt,
		PreviousFailure: request.PreviousFailure,
	})
	var attemptErr *authorAttemptError
	if errors.As(err, &attemptErr) {
		// The model can act on this in a retry, so it is a failed attempt.
		return ports.AgentWork{}, agentAttemptFailure("%s", boundedRedacted(attemptErr.Error()))
	}
	if err != nil {
		return ports.AgentWork{}, err
	}
	work := ports.AgentWork{Summary: boundedRedacted(summary), Base: base}
	status, err := m.gitOutput(ctx, scratch, "status", "--porcelain")
	if err != nil {
		return ports.AgentWork{}, err
	}
	if status == "" {
		// Nothing changed; the fixed commands and the validator still decide
		// whether the subtask is done, as they do in the shared worktree.
		return work, nil
	}
	if _, err := m.gitOutput(ctx, scratch, "add", "--all"); err != nil {
		return ports.AgentWork{}, err
	}
	// Hooks are skipped so repository scripts never run on the host.
	if _, err := m.gitOutput(ctx, scratch,
		"-c", "user.name="+m.authorName, "-c", "user.email="+m.authorEmail,
		"commit", "--no-verify", "--quiet", "-m", writerCommitMessage(request.Task)); err != nil {
		return ports.AgentWork{}, err
	}
	committed = true
	work.Branch = branch
	return work, nil
}

// Integrate merges an attempt's branch into the run worktree, then deletes
// the branch, merged or not: a failed attempt is retried from the run's latest
// commit on a new branch.
func (m *ModelWriters) Integrate(ctx context.Context, worktree string, work ports.AgentWork) error {
	if m == nil {
		return errors.New("model writers are not configured")
	}
	if work.Branch == "" {
		return nil
	}
	err := m.checkpoint(ctx, worktree)
	if err == nil {
		err = integrateWorkBranch(ctx, m.gitOutput, worktree, work, m.authorName, m.authorEmail)
	}
	m.setup.Lock()
	defer m.setup.Unlock()
	deleteFlag := "-d"
	if err != nil {
		deleteFlag = "-D"
	}
	_, _ = m.gitOutput(context.WithoutCancel(ctx), worktree, "branch", deleteFlag, work.Branch)
	return err
}

// prepare cuts the attempt's branch from the run's HEAD and checks it out in
// a new, empty directory beside the run worktree, which is under ~/.ao.
func (m *ModelWriters) prepare(ctx context.Context, runWorktree, subtaskID string) (scratch, branch, base string, err error) {
	base, err = m.gitOutput(ctx, runWorktree, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	runBranch, err := m.gitOutput(ctx, runWorktree, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	if runBranch == "" || runBranch == "HEAD" {
		return "", "", "", errors.New("the run worktree is not on a branch")
	}
	m.setup.Lock()
	defer m.setup.Unlock()
	branch, err = createSubtaskBranch(ctx, m.gitOutput, runWorktree, runBranch, subtaskID, base)
	if err != nil {
		return "", "", "", err
	}
	scratch, err = os.MkdirTemp(filepath.Dir(runWorktree), filepath.Base(runWorktree)+".writer-")
	if err != nil {
		_, _ = m.gitOutput(context.WithoutCancel(ctx), runWorktree, "branch", "-D", branch)
		return "", "", "", fmt.Errorf("create a worktree directory for subtask %q: %w", subtaskID, err)
	}
	if _, err := m.gitOutput(ctx, runWorktree, "worktree", "add", "--quiet", scratch, branch); err != nil {
		_ = os.Remove(scratch)
		_, _ = m.gitOutput(context.WithoutCancel(ctx), runWorktree, "branch", "-D", branch)
		return "", "", "", err
	}
	return scratch, branch, base, nil
}

// release removes the attempt's worktree once it is clean, and its branch
// unless it carries committed work for Integrate. A worktree left dirty by a
// failed write is kept rather than force-removed.
func (m *ModelWriters) release(ctx context.Context, runWorktree, scratch, branch string, keepBranch bool) {
	m.setup.Lock()
	defer m.setup.Unlock()
	if _, err := m.gitOutput(ctx, runWorktree, "worktree", "remove", scratch); err != nil {
		return
	}
	if !keepBranch {
		_, _ = m.gitOutput(ctx, runWorktree, "branch", "-D", branch)
	}
}

// checkpoint commits whatever the run's checks left in the run worktree, so
// merging the next subtask is never blocked by it. The run's final commit
// would include the same files.
func (m *ModelWriters) checkpoint(ctx context.Context, worktree string) error {
	status, err := m.gitOutput(ctx, worktree, "status", "--porcelain")
	if err != nil || status == "" {
		return err
	}
	if _, err := m.gitOutput(ctx, worktree, "add", "--all"); err != nil {
		return err
	}
	_, err = m.gitOutput(ctx, worktree,
		"-c", "user.name="+m.authorName, "-c", "user.email="+m.authorEmail,
		"commit", "--no-verify", "--quiet", "-m", "Keep the run's work so far")
	return err
}

func (m *ModelWriters) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := m.git.RunInDirectory(ctx, dir, append([]string{"git"}, args...))
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
	return strings.TrimSpace(output.Stdout), nil
}

// writerCommitMessage names the subtask on one line.
func writerCommitMessage(task ports.PlannedSubtask) string {
	title := strings.Join(strings.Fields(task.Title), " ")
	if title == "" {
		title = task.ID
	}
	if runes := []rune(title); len(runes) > 72 {
		title = string(runes[:72]) + "…"
	}
	return boundedRedacted(title)
}
