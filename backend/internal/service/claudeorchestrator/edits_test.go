package claudeorchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCleanRepoPathAcceptsOrdinaryRelativePaths(t *testing.T) {
	for input, want := range map[string]string{
		"main.go":            "main.go",
		"cmd/app/main.go":    "cmd/app/main.go",
		"./docs/README.md":   "docs/README.md",
		"a//b.txt":           "a/b.txt",
		".github/ci.yml":     ".github/ci.yml",
		"envoy/config.yaml":  "envoy/config.yaml",
		"src/.environment.x": "src/.environment.x",
	} {
		got, err := cleanRepoPath(input)
		if err != nil || got != want {
			t.Errorf("cleanRepoPath(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
}

func TestCleanRepoPathRejectsEscapesAndProtectedPaths(t *testing.T) {
	for _, input := range []string{
		"", ".", "/etc/passwd", "../outside", "a/../../outside", "a/..", `a\b`, "C:/x", "a\x00b",
		".git/config", "sub/.git/hooks/pre-commit", ".GIT/config",
		".env", "app/.env.production", ".envrc", "home/.ssh/id_rsa", "id_ed25519.pub",
		"certs/server.key", "tls.pem", "infra/terraform.tfstate", ".aws/credentials", ".npmrc",
	} {
		if got, err := cleanRepoPath(input); err == nil {
			t.Errorf("cleanRepoPath(%q) = %q, want rejection", input, got)
		}
	}
}

func TestApplyEditsWritesCreatesAndDeletesInsideWorktree(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "old.txt"), "remove me")
	mustWrite(t, filepath.Join(worktree, "keep.txt"), "before")

	applied, err := applyEdits(worktree, []ports.FileEdit{
		{Path: "keep.txt", Content: "after"},
		{Path: "pkg/new/file.go", Content: "package new\n"},
		{Path: "old.txt", Delete: true},
	})
	if err != nil {
		t.Fatalf("applyEdits() error = %v", err)
	}
	if got := mustRead(t, filepath.Join(worktree, "keep.txt")); got != "after" {
		t.Fatalf("keep.txt = %q, want replaced content", got)
	}
	if got := mustRead(t, filepath.Join(worktree, "pkg", "new", "file.go")); got != "package new\n" {
		t.Fatalf("new file = %q", got)
	}
	if _, err := os.Stat(filepath.Join(worktree, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old.txt still exists: %v", err)
	}
	if strings.Join(applied.Written, ",") != "keep.txt,pkg/new/file.go" || strings.Join(applied.Deleted, ",") != "old.txt" {
		t.Fatalf("applied = %+v", applied)
	}
}

// A proposal is validated as a whole before anything is written, so one bad
// edit leaves the worktree untouched.
func TestApplyEditsRejectsWholeProposalWhenAnyEditIsUnsafe(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "a.txt"), "original")
	for _, bad := range []ports.FileEdit{
		{Path: "../escape.txt", Content: "x"},
		{Path: "/tmp/abs.txt", Content: "x"},
		{Path: ".git/config", Content: "x"},
		{Path: ".env", Content: "SECRET=1"},
		{Path: "a.txt", Content: "duplicate"},
		{Path: "big.txt", Content: strings.Repeat("x", maxEditFileBytes+1)},
		{Path: "bin.dat", Content: string([]byte{0xff, 0xfe})},
	} {
		_, err := applyEdits(worktree, []ports.FileEdit{{Path: "a.txt", Content: "changed"}, bad})
		if err == nil {
			t.Errorf("applyEdits with %q succeeded, want rejection", bad.Path)
			continue
		}
		if got := mustRead(t, filepath.Join(worktree, "a.txt")); got != "original" {
			t.Fatalf("a.txt = %q after rejected proposal with %q; nothing should be written", got, bad.Path)
		}
	}
}

func TestApplyEditsRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	worktree := t.TempDir()
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "target.txt"), "outside")
	if err := os.Symlink(outside, filepath.Join(worktree, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "target.txt"), filepath.Join(worktree, "linkfile.txt")); err != nil {
		t.Fatal(err)
	}
	// An in-tree link must not redirect a write into a protected location.
	if err := os.MkdirAll(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git", filepath.Join(worktree, "innocent")); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"linkdir/target.txt", "linkdir/new.txt", "linkfile.txt", "innocent/config"} {
		if _, err := applyEdits(worktree, []ports.FileEdit{{Path: path, Content: "pwned"}}); err == nil {
			t.Errorf("applyEdits(%q) succeeded through a symlink", path)
		}
	}
	if got := mustRead(t, filepath.Join(outside, "target.txt")); got != "outside" {
		t.Fatalf("file outside the worktree was modified: %q", got)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file was created outside the worktree")
	}
}

func TestApplyEditsAllowsEmptyProposalAndBoundsEditCount(t *testing.T) {
	worktree := t.TempDir()
	if applied, err := applyEdits(worktree, nil); err != nil || len(applied.Written)+len(applied.Deleted) != 0 {
		t.Fatalf("empty proposal = %+v, %v; want no-op", applied, err)
	}
	edits := make([]ports.FileEdit, maxEditsPerAttempt+1)
	for i := range edits {
		edits[i] = ports.FileEdit{Path: filepath.ToSlash(filepath.Join("f", strings.Repeat("x", i+1))), Content: "x"}
	}
	if _, err := applyEdits(worktree, edits); err == nil {
		t.Fatal("applyEdits accepted more edits than allowed")
	}
}

func TestReadSnapshotsReturnsOnlyKnownSafeTextFiles(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "main.go"), "package main\n")
	mustWrite(t, filepath.Join(worktree, "secret.pem"), "-----BEGIN-----")
	mustWrite(t, filepath.Join(worktree, "binary.bin"), string([]byte{0xff, 0x00, 0xfe}))
	mustWrite(t, filepath.Join(worktree, "huge.txt"), strings.Repeat("a", maxReadFileBytes+1))
	mustWrite(t, filepath.Join(worktree, "unlisted.go"), "package hidden\n")
	known := []string{"main.go", "secret.pem", "binary.bin", "huge.txt", "missing.go"}

	snapshots, err := readSnapshots(worktree,
		[]string{"main.go", "main.go", "secret.pem", "binary.bin", "huge.txt", "unlisted.go", "missing.go", "../etc/passwd"}, known)
	if err != nil {
		t.Fatalf("readSnapshots() error = %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Path != "main.go" || snapshots[0].Content != "package main\n" {
		t.Fatalf("snapshots = %+v, want only main.go", snapshots)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
