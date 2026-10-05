package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type runnerCall struct {
	dir  string
	args []string
}

type fakeRunner struct {
	root       string
	worktree   string
	status     string
	calls      []runnerCall
	failOn     string
	failErr    error
	listOutput string
}

func (f *fakeRunner) Run(_ context.Context, dir string, args ...string) ([]byte, error) {
	copied := append([]string(nil), args...)
	f.calls = append(f.calls, runnerCall{dir: dir, args: copied})
	if len(args) > 0 && args[0] == f.failOn {
		return []byte("runner failure"), f.failErr
	}
	switch {
	case reflect.DeepEqual(args, []string{"rev-parse", "--show-toplevel"}):
		return []byte(f.root + "\n"), nil
	case reflect.DeepEqual(args, []string{"worktree", "list", "--porcelain", "-z"}):
		if f.listOutput != "" {
			return []byte(f.listOutput), nil
		}
		return []byte("worktree " + f.worktree + "\x00HEAD deadbeef\x00branch refs/heads/topic\x00\x00"), nil
	case len(args) > 0 && args[0] == "status":
		return []byte(f.status), nil
	default:
		return nil, nil
	}
}

func newTestManager(t *testing.T) (string, string, *fakeRunner, *Manager) {
	t.Helper()
	root := t.TempDir()
	parent := filepath.Join(root, "worktrees")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "topic")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{root: root, worktree: path, status: "## topic\n"}
	return root, path, runner, NewManager(runner)
}

func TestCreateUsesGitArgvAndDoesNotRemoveAutomatically(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "worktrees", "topic")
	runner := &fakeRunner{root: root}
	manager := NewManager(runner)

	result, err := manager.Create(context.Background(), ports.WorktreeCreateRequest{
		ProjectRoot: root,
		Path:        path,
		Branch:      "feature/topic-1",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if result.Path != path || result.Branch != "feature/topic-1" {
		t.Fatalf("Create() result = %#v", result)
	}
	if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[1].args, []string{"worktree", "add", "-b", "feature/topic-1", path}) {
		t.Fatalf("unexpected command calls: %#v", runner.calls)
	}
	for _, call := range runner.calls {
		if len(call.args) > 0 && call.args[0] == "worktree" && len(call.args) > 1 && call.args[1] == "remove" {
			t.Fatal("Create() unexpectedly removed a worktree")
		}
	}
}

func TestCreateKeepsPathAsOneArgumentWithoutShell(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "worktrees", "literal ; $(not-a-command)")
	runner := &fakeRunner{root: root}
	manager := NewManager(runner)
	if _, err := manager.Create(context.Background(), ports.WorktreeCreateRequest{ProjectRoot: root, Path: path, Branch: "safe-branch"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got := runner.calls[1].args[len(runner.calls[1].args)-1]; got != path {
		t.Fatalf("destination argument = %q, want exact path %q", got, path)
	}
	if len(runner.calls[1].args) != 5 {
		t.Fatalf("unexpected argv split: %#v", runner.calls[1].args)
	}
}

func TestCreateRejectsUnsafeBranchAndEscapingPath(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{root: root}
	manager := NewManager(runner)
	for _, tc := range []struct {
		name   string
		path   string
		branch string
	}{
		{name: "shell punctuation branch", path: filepath.Join(root, "worktrees", "topic"), branch: "topic;touch-pwned"},
		{name: "option branch", path: filepath.Join(root, "worktrees", "topic"), branch: "--force"},
		{name: "outside root", path: filepath.Join(filepath.Dir(root), "escaped"), branch: "safe"},
		{name: "git metadata", path: filepath.Join(root, ".git", "escaped"), branch: "safe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(runner.calls)
			_, err := manager.Create(context.Background(), ports.WorktreeCreateRequest{ProjectRoot: root, Path: tc.path, Branch: tc.branch})
			if err == nil {
				t.Fatal("Create() error = nil, want validation error")
			}
			for _, call := range runner.calls[before:] {
				if len(call.args) > 0 && call.args[0] == "worktree" {
					t.Fatalf("invalid request reached Git worktree command: %#v", call.args)
				}
			}
		})
	}
}

func TestStatusReportsRegisteredWorktreeAndDoesNotRemove(t *testing.T) {
	root, path, runner, manager := newTestManager(t)
	runner.status = "## topic\n M file.txt\n"
	result, err := manager.Status(context.Background(), ports.WorktreeStatusRequest{ProjectRoot: root, Path: path})
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if result.Path != path || result.Branch != "topic" || result.Clean || !strings.Contains(result.Output, " M file.txt") {
		t.Fatalf("Status() result = %#v", result)
	}
	last := runner.calls[len(runner.calls)-1]
	if last.dir != path || !reflect.DeepEqual(last.args, []string{"status", "--porcelain=v1", "--branch"}) {
		t.Fatalf("unexpected status invocation: %#v", last)
	}
	for _, call := range runner.calls {
		if len(call.args) > 1 && call.args[0] == "worktree" && call.args[1] == "remove" {
			t.Fatal("Status() unexpectedly removed a worktree")
		}
	}
}

func TestStatusRejectsUnregisteredWorktree(t *testing.T) {
	root, path, runner, manager := newTestManager(t)
	runner.listOutput = "worktree " + filepath.Join(root, "other") + "\x00HEAD abc\x00branch refs/heads/other\x00\x00"
	_, err := manager.Status(context.Background(), ports.WorktreeStatusRequest{ProjectRoot: root, Path: path})
	if err == nil || !strings.Contains(err.Error(), "not a registered worktree") {
		t.Fatalf("Status() error = %v, want unregistered worktree error", err)
	}
}

func TestRemoveIsExplicitAndReturnsTypedResult(t *testing.T) {
	root, path, runner, manager := newTestManager(t)
	result, err := manager.Remove(context.Background(), ports.WorktreeRemoveRequest{ProjectRoot: root, Path: path, Force: true})
	if err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if !result.Removed || result.Path != path {
		t.Fatalf("Remove() result = %#v", result)
	}
	last := runner.calls[len(runner.calls)-1]
	if !reflect.DeepEqual(last.args, []string{"worktree", "remove", "--force", path}) {
		t.Fatalf("unexpected remove invocation: %#v", last.args)
	}
}

func TestRemovePropagatesRunnerError(t *testing.T) {
	root, path, runner, manager := newTestManager(t)
	wantErr := errors.New("git failed")
	runner.failOn = "worktree"
	runner.failErr = wantErr
	_, err := manager.Remove(context.Background(), ports.WorktreeRemoveRequest{ProjectRoot: root, Path: path})
	if err == nil || !strings.Contains(err.Error(), "runner failure") {
		t.Fatalf("Remove() error = %v, want command failure", err)
	}
}
