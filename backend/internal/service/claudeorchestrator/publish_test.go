package claudeorchestrator

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPublishPushesOnlyTheRunBranchWithoutForce(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	project := filepath.Join(root, "project")
	gitT(t, root, "init", "--bare", "-b", "main", remote)
	gitT(t, root, "init", "-b", "main", project)
	gitT(t, project, "commit", "--allow-empty", "-m", "base")
	gitT(t, project, "remote", "add", "origin", remote)
	gitT(t, project, "push", "origin", "main")
	gitT(t, project, "branch", "ao/claude-orchestrator/run-1")
	gitT(t, project, "branch", "other-work")

	publisher, err := NewRunPublisher(ExecCommandRunner{}, project, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := publisher.Publish(context.Background(), PublishRequest{RunID: "run-1", Branch: "ao/claude-orchestrator/run-1", Title: "t"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.Remote != "origin" || result.Base != "main" {
		t.Fatalf("result = %+v", result)
	}
	branches := gitT(t, root, "--git-dir", remote, "branch", "--list")
	if !strings.Contains(branches, "ao/claude-orchestrator/run-1") || strings.Contains(branches, "other-work") {
		t.Fatalf("remote branches = %q, want only the run branch pushed", branches)
	}
	if result.CompareURL != "" {
		t.Fatalf("CompareURL = %q for a non-GitHub remote", result.CompareURL)
	}

	// A branch that diverged on the remote is never overwritten.
	gitT(t, project, "checkout", "-q", "ao/claude-orchestrator/run-1")
	gitT(t, project, "commit", "--allow-empty", "-m", "local")
	gitT(t, project, "push", "origin", "ao/claude-orchestrator/run-1")
	gitT(t, project, "reset", "-q", "--hard", "HEAD~1")
	gitT(t, project, "commit", "--allow-empty", "-m", "diverged")
	if _, err := publisher.Publish(context.Background(), PublishRequest{RunID: "run-1", Branch: "ao/claude-orchestrator/run-1"}); err == nil {
		t.Fatal("Publish force-pushed over a diverged remote branch")
	}
}

func TestPublishRefusesOtherBranchesAndMissingRemotes(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	project := t.TempDir()
	gitT(t, project, "init", "-b", "main")
	publisher, err := NewRunPublisher(ExecCommandRunner{}, project, "origin")
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []PublishRequest{
		{RunID: "run-1", Branch: "main"},
		{RunID: "run-1", Branch: "ao/claude-orchestrator/run-2"},
		{RunID: "../x", Branch: "ao/claude-orchestrator/../x"},
	} {
		if _, err := publisher.Publish(context.Background(), request); !errors.Is(err, ErrPublishBranchInvalid) {
			t.Fatalf("Publish(%+v) = %v, want ErrPublishBranchInvalid", request, err)
		}
	}
	if _, err := publisher.Publish(context.Background(), PublishRequest{RunID: "run-1", Branch: "ao/claude-orchestrator/run-1"}); !errors.Is(err, ErrPublishNoRemote) {
		t.Fatalf("Publish without a remote = %v, want ErrPublishNoRemote", err)
	}
	if _, err := NewRunPublisher(ExecCommandRunner{}, project, "--upload-pack=evil"); err == nil {
		t.Fatal("an option-shaped remote name was accepted")
	}
}

func TestGitHubCompareURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/app.git":                  "https://github.com/acme/app/compare/main...ao/claude-orchestrator/r?expand=1",
		"https://github.com/acme/app.git":              "https://github.com/acme/app/compare/main...ao/claude-orchestrator/r?expand=1",
		"https://user:hunter2@github.com/acme/app.git": "https://github.com/acme/app/compare/main...ao/claude-orchestrator/r?expand=1",
		"https://gitlab.com/acme/app.git":              "",
		"/tmp/remote.git":                              "",
	}
	for remote, want := range cases {
		if got := githubCompareURL(remote, "main", "ao/claude-orchestrator/r"); got != want {
			t.Errorf("githubCompareURL(%q) = %q, want %q", remote, got, want)
		}
	}
}
