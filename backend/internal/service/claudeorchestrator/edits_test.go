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

func replacements(pairs ...string) []ports.TextReplacement {
	var out []ports.TextReplacement
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, ports.TextReplacement{Old: pairs[i], New: pairs[i+1]})
	}
	return out
}

// A writer changes a large file by naming only the passages it changes; the
// rest of the file is kept byte for byte.
func TestApplyEditsReplacesPassagesAndKeepsTheRestOfTheFile(t *testing.T) {
	worktree := t.TempDir()
	body := strings.Repeat("// unchanged line\n", 5000)
	mustWrite(t, filepath.Join(worktree, "config.go"), "package config\n\ntype Config struct {\n\tClaudeOnly bool\n}\n"+body)
	mustWrite(t, filepath.Join(worktree, "keep.txt"), "before")

	applied, err := applyEdits(worktree, []ports.FileEdit{
		{Path: "config.go", Replace: replacements(
			"\tClaudeOnly bool\n", "\t// ClaudeOnly lets one Claude key do everything.\n\tClaudeOnly bool\n",
			// Later replacements see the earlier ones.
			"everything.", "everything: plan, write and review.",
		)},
		{Path: "keep.txt", Content: "after"},
	})
	if err != nil {
		t.Fatalf("applyEdits() error = %v", err)
	}
	want := "package config\n\ntype Config struct {\n\t// ClaudeOnly lets one Claude key do everything: plan, write and review.\n\tClaudeOnly bool\n}\n" + body
	if got := mustRead(t, filepath.Join(worktree, "config.go")); got != want {
		t.Fatalf("config.go head = %q", got[:120])
	}
	if got := mustRead(t, filepath.Join(worktree, "keep.txt")); got != "after" {
		t.Fatalf("keep.txt = %q", got)
	}
	if strings.Join(applied.Written, ",") != "config.go,keep.txt" {
		t.Fatalf("applied = %+v", applied)
	}
}

// Git for Windows checks files out with CRLF line endings; a model quoting
// them with plain newlines still matches, and its new text gets CRLF too.
func TestApplyEditsReplacementsMatchCRLFCheckouts(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "a.go"), "one\r\ntwo\r\nthree\r\n")
	if _, err := applyEdits(worktree, []ports.FileEdit{{Path: "a.go", Replace: replacements("one\ntwo\n", "1\n2\n")}}); err != nil {
		t.Fatalf("applyEdits() error = %v", err)
	}
	if got := mustRead(t, filepath.Join(worktree, "a.go")); got != "1\r\n2\r\nthree\r\n" {
		t.Fatalf("a.go = %q", got)
	}
	// Quoting the CRLF exactly works as well.
	if _, err := applyEdits(worktree, []ports.FileEdit{{Path: "a.go", Replace: replacements("2\r\nthree", "2\r\n3")}}); err != nil {
		t.Fatalf("applyEdits() error = %v", err)
	}
	if got := mustRead(t, filepath.Join(worktree, "a.go")); got != "1\r\n2\r\n3\r\n" {
		t.Fatalf("a.go = %q", got)
	}
}

// A replacement that does not match exactly once rejects the whole proposal
// with a reason the model can act on in its retry.
func TestApplyEditsRejectsReplacementsThatDoNotMatchOnce(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "a.txt"), "original")
	mustWrite(t, filepath.Join(worktree, "b.txt"), "x = 1\nx = 1\n")
	tooMany := make([]ports.TextReplacement, maxReplacementsPerEdit+1)
	for i := range tooMany {
		tooMany[i] = ports.TextReplacement{Old: "x", New: "y"}
	}
	for want, bad := range map[string]ports.FileEdit{
		"not in the file":   {Path: "b.txt", Replace: replacements("y = 2", "y = 3")},
		"occurs 2 times":    {Path: "b.txt", Replace: replacements("x = 1", "x = 2")},
		"has no old text":   {Path: "b.txt", Replace: replacements("", "x")},
		"does not exist":    {Path: "missing.txt", Replace: replacements("a", "b")},
		"one of them":       {Path: "b.txt", Content: "x", Replace: replacements("x", "y")},
		"at most 100":       {Path: "b.txt", Replace: tooMany},
		"replacement 2 for": {Path: "b.txt", Replace: replacements("x = 1\nx", "z = 1\nx", "x = 1\nx", "w")},
	} {
		_, err := applyEdits(worktree, []ports.FileEdit{{Path: "a.txt", Content: "changed"}, bad})
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), bad.Path) {
			t.Errorf("applyEdits(%s) = %v, want an error naming the file and %q", bad.Path, err, want)
		}
		if got := mustRead(t, filepath.Join(worktree, "a.txt")); got != "original" {
			t.Fatalf("a.txt = %q after a rejected proposal; nothing should be written", got)
		}
		if got := mustRead(t, filepath.Join(worktree, "b.txt")); got != "x = 1\nx = 1\n" {
			t.Fatalf("b.txt = %q after a rejected proposal", got)
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
	if _, err := applyEdits(worktree, []ports.FileEdit{{Path: "linkfile.txt", Replace: replacements("outside", "pwned")}}); err == nil {
		t.Error("a replacement went through a symlink")
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

// What one reply is sent is bounded; the files left out are named, so the
// writer knows they exist, and only files that are really there are named.
func TestFilesPastTheReadLimitAreNamedAsNotShown(t *testing.T) {
	worktree := t.TempDir()
	half := strings.Repeat("a", maxReadTotalBytes/2+1)
	mustWrite(t, filepath.Join(worktree, "css/one.css"), half)
	mustWrite(t, filepath.Join(worktree, "css/two.css"), half)
	mustWrite(t, filepath.Join(worktree, "js/small.js"), "x")
	mustWrite(t, filepath.Join(worktree, "huge.txt"), strings.Repeat("a", maxReadFileBytes+1))
	known := []string{"css/one.css", "css/two.css", "js/small.js", "huge.txt", "missing.go"}

	snapshots, left, err := snapshotsFor(worktree, []string{"css/one.css", "css/two.css", "js/small.js", "huge.txt", "missing.go"}, known)
	if err != nil {
		t.Fatal(err)
	}
	var sent []string
	for _, snapshot := range snapshots {
		sent = append(sent, snapshot.Path)
	}
	if strings.Join(sent, ",") != "css/one.css,js/small.js" {
		t.Fatalf("sent %v, want the files that fit, in the order asked", sent)
	}
	if strings.Join(left, ",") != "css/two.css,huge.txt" {
		t.Fatalf("not shown = %v, want the existing files that did not fit", left)
	}
}
