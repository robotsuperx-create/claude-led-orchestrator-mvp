package claudeorchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	worktreesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/worktree"
)

// newGitProject creates a repository with one commit and returns its
// canonical root (temp dirs can sit behind symlinks, e.g. on macOS).
func newGitProject(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "README.md"), "# demo\n")
	runGit(t, root, "init", "--quiet", "--initial-branch=main")
	runGit(t, root, "add", "--all")
	runGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "initial")
	return root
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func newTestRunWorkspaces(t *testing.T, project string) (*RunWorkspaces, string) {
	t.Helper()
	managed, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	managed = filepath.Join(managed, "worktrees", "claude-orchestrator")
	workspaces, err := NewRunWorkspaces(worktreesvc.NewManagerWithManagedRoot(nil, managed), ExecCommandRunner{}, RunWorkspacesConfig{
		ProjectRoot: project, ManagedRoot: managed,
	})
	if err != nil {
		t.Fatalf("NewRunWorkspaces: %v", err)
	}
	return workspaces, managed
}

func TestRunWorkspacesCreateIsolatedWorktreeAndCommitChanges(t *testing.T) {
	project := newGitProject(t)
	workspaces, managed := newTestRunWorkspaces(t, project)
	ctx := context.Background()

	workspace, err := workspaces.Prepare(ctx, "run-123")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !strings.HasPrefix(workspace.Path, managed+string(filepath.Separator)) {
		t.Fatalf("worktree %q is not under the managed root %q", workspace.Path, managed)
	}
	if workspace.Branch != "ao/claude-orchestrator/run-123" {
		t.Fatalf("branch = %q", workspace.Branch)
	}
	if got := runGit(t, workspace.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != workspace.Branch {
		t.Fatalf("worktree HEAD = %q, want %q", got, workspace.Branch)
	}
	// The worker's worktree verification accepts it.
	if _, err := verifyWorktree(ctx, worktreesvc.NewManagerWithManagedRoot(nil, managed), project, workspace.Path, "run"); err != nil {
		t.Fatalf("verifyWorktree rejected the provisioned worktree: %v", err)
	}

	mustWrite(t, filepath.Join(workspace.Path, "greet.go"), "package main\n")
	finalized, err := workspaces.Finalize(ctx, workspace, ports.OrchestrationResult{
		RunID: "run-123", Task: "add a greeting",
		Plan:          ports.ExecutionPlan{Summary: "إضافة تحية للمستخدم"},
		MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeMerge},
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if finalized.Commit == "" || runGit(t, project, "rev-parse", workspace.Branch) != finalized.Commit {
		t.Fatalf("commit %q is not the tip of %s", finalized.Commit, workspace.Branch)
	}
	if files := runGit(t, project, "show", "--name-only", "--format=", finalized.Commit); files != "greet.go" {
		t.Fatalf("committed files = %q, want greet.go", files)
	}
	if subject := runGit(t, project, "log", "-1", "--format=%s", finalized.Commit); subject != "إضافة تحية للمستخدم" {
		t.Fatalf("commit subject = %q", subject)
	}
	if author := runGit(t, project, "log", "-1", "--format=%an", finalized.Commit); author != "AO Claude Orchestrator" {
		t.Fatalf("commit author = %q", author)
	}
	// The project's own checkout is untouched.
	if _, err := os.Stat(filepath.Join(project, "greet.go")); !os.IsNotExist(err) {
		t.Fatal("the run changed the main checkout")
	}
	if branch := runGit(t, project, "rev-parse", "--abbrev-ref", "HEAD"); branch != "main" {
		t.Fatalf("main checkout moved to %q", branch)
	}
}

func TestRunWorkspacesFinalizeWithoutChangesMakesNoCommit(t *testing.T) {
	project := newGitProject(t)
	workspaces, _ := newTestRunWorkspaces(t, project)
	workspace, err := workspaces.Prepare(context.Background(), "run-clean")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	before := runGit(t, project, "rev-parse", workspace.Branch)
	finalized, err := workspaces.Finalize(context.Background(), workspace, ports.OrchestrationResult{RunID: "run-clean"})
	if err != nil || finalized.Commit != "" {
		t.Fatalf("Finalize = %+v, %v; want no commit", finalized, err)
	}
	if after := runGit(t, project, "rev-parse", workspace.Branch); after != before {
		t.Fatal("branch moved although nothing changed")
	}
}

func TestRunWorkspacesRejectUnsafeRunIDs(t *testing.T) {
	project := newGitProject(t)
	workspaces, _ := newTestRunWorkspaces(t, project)
	for _, id := range []string{"", "../escape", "a/b", "-flag", strings.Repeat("x", 65), "a b"} {
		if _, err := workspaces.Prepare(context.Background(), id); err == nil {
			t.Errorf("Prepare(%q) succeeded, want rejection", id)
		}
	}
}

func TestRunWorkspacesFinalizeReportsCommitsMadeDuringTheRun(t *testing.T) {
	project := newGitProject(t)
	workspaces, _ := newTestRunWorkspaces(t, project)
	workspace, err := workspaces.Prepare(context.Background(), "run-agent")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if workspace.BaseCommit != runGit(t, project, "rev-parse", "HEAD") {
		t.Fatalf("BaseCommit = %q, want the project HEAD", workspace.BaseCommit)
	}
	// An agent's work arrives already committed.
	mustWrite(t, filepath.Join(workspace.Path, "agent.txt"), "done\n")
	runGit(t, workspace.Path, "add", "--all")
	runGit(t, workspace.Path, "-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "--quiet", "-m", "agent")
	head := runGit(t, workspace.Path, "rev-parse", "HEAD")
	finalized, err := workspaces.Finalize(context.Background(), workspace, ports.OrchestrationResult{RunID: "run-agent"})
	if err != nil || finalized.Commit != head {
		t.Fatalf("Finalize = %+v, %v; want commit %s", finalized, err, head)
	}
}
