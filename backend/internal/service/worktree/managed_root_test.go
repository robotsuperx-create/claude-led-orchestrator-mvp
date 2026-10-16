package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func gitProject(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "--all"},
		{"-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "--quiet", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	return root
}

func TestManagedRootAcceptsRegisteredWorktreesOnly(t *testing.T) {
	project := gitProject(t)
	managed, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManagerWithManagedRoot(nil, managed)
	ctx := context.Background()

	created, err := manager.Create(ctx, ports.WorktreeCreateRequest{
		ProjectRoot: project, Path: filepath.Join(managed, "run-1"), Branch: "ao/run-1",
	})
	if err != nil {
		t.Fatalf("Create under managed root: %v", err)
	}
	if status, err := manager.Status(ctx, ports.WorktreeStatusRequest{ProjectRoot: project, Path: created.Path}); err != nil || status.Branch != "ao/run-1" {
		t.Fatalf("Status = %+v, %v; want the registered run worktree", status, err)
	}

	// A plain directory under the managed root is not a worktree of the project.
	stray := filepath.Join(managed, "stray")
	if err := os.Mkdir(stray, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Status(ctx, ports.WorktreeStatusRequest{ProjectRoot: project, Path: stray}); err == nil ||
		!strings.Contains(err.Error(), "not a registered worktree") {
		t.Fatalf("Status(stray) error = %v, want unregistered rejection", err)
	}

	// Paths outside both the project and the managed root are refused.
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create(ctx, ports.WorktreeCreateRequest{ProjectRoot: project, Path: filepath.Join(outside, "run-2"), Branch: "ao/run-2"}); err == nil {
		t.Fatal("Create outside the allowed roots succeeded")
	}
	if _, err := manager.Status(ctx, ports.WorktreeStatusRequest{ProjectRoot: project, Path: outside}); err == nil {
		t.Fatal("Status outside the allowed roots succeeded")
	}

	// Without a managed root the same path is refused, as before.
	if _, err := NewManager(nil).Status(ctx, ports.WorktreeStatusRequest{ProjectRoot: project, Path: created.Path}); err == nil {
		t.Fatal("a manager without a managed root accepted a path outside the project")
	}
}
