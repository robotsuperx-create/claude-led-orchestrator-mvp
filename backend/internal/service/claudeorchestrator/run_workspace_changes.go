package claudeorchestrator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Bounds for what the deck's code stream shows of a run's writing.
const (
	liveMaxFiles     = 12
	liveMaxLines     = 24
	liveMaxLineRunes = 240
	liveMaxFileBytes = 64 << 10
)

var _ ports.RunWorkspaceChanges = (*RunWorkspaces)(nil)

// Changes reads what the run has written so far: the difference between its
// worktree and the commit it started from, plus the files it created. It uses
// read-only Git commands that take no index lock, so the run's writers are
// never disturbed. Files come newest first; lines are bounded and redacted.
func (w *RunWorkspaces) Changes(ctx context.Context, workspace ports.RunWorkspace) ([]ports.LiveFileChange, error) {
	if w == nil || workspace.Path == "" {
		return nil, nil
	}
	base := workspace.BaseCommit
	if base == "" {
		base = "HEAD"
	}
	diff, err := w.gitIn(ctx, workspace.Path, "--no-optional-locks", "diff", "--no-color", "--no-ext-diff", "--no-renames", "--unified=0", base, "--")
	if err != nil {
		return nil, err
	}
	files := parseLiveDiff(diff)
	untracked, err := w.gitIn(ctx, workspace.Path, "--no-optional-locks", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	for _, name := range strings.Split(untracked, "\x00") {
		if name = strings.TrimSpace(name); name != "" {
			if change, ok := readNewFile(workspace.Path, name); ok {
				files = append(files, change)
			}
		}
	}
	return newestFirst(workspace.Path, files), nil
}

var _ RunWorkspaceDiff = (*RunWorkspaces)(nil)

// FullDiff is the whole of a run's change for the security guard: the diff
// against the commit it started from, then each new file as added lines.
// Read-only, bounded, and it takes no index lock.
func (w *RunWorkspaces) FullDiff(ctx context.Context, workspace ports.RunWorkspace) (string, error) {
	if w == nil || workspace.Path == "" {
		return "", nil
	}
	base := workspace.BaseCommit
	if base == "" {
		base = "HEAD"
	}
	diff, err := w.gitIn(ctx, workspace.Path, "--no-optional-locks", "diff", "--no-color", "--no-ext-diff", "--no-renames", "--unified=0", base, "--")
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString(diff)
	untracked, err := w.gitIn(ctx, workspace.Path, "--no-optional-locks", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", err
	}
	for _, name := range strings.Split(untracked, "\x00") {
		if name = strings.TrimSpace(name); name == "" || out.Len() > maxGuardDiffBytes {
			continue
		}
		clean := filepath.Clean(filepath.FromSlash(name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			continue
		}
		path := filepath.Join(workspace.Path, clean)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > liveMaxFileBytes*4 {
			continue
		}
		content, err := os.ReadFile(path) //nolint:gosec // a file Git lists inside the run's own worktree
		if err != nil || bytes.IndexByte(content, 0) >= 0 {
			continue
		}
		fmt.Fprintf(&out, "\n+++ b/%s\n@@ -0,0 +1 @@\n", filepath.ToSlash(clean))
		for _, line := range strings.Split(strings.TrimRight(string(content), "\n"), "\n") {
			out.WriteString("+" + line + "\n")
		}
	}
	text := out.String()
	if len(text) > maxGuardDiffBytes {
		text = text[:maxGuardDiffBytes]
	}
	return text, nil
}

// parseLiveDiff turns `git diff --unified=0` into per-file counts and the last
// lines each file gained.
func parseLiveDiff(diff string) []ports.LiveFileChange {
	var files []ports.LiveFileChange
	current := -1
	inHunk := false
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			// The header names the file even when no +++ line follows (binary files).
			files = append(files, ports.LiveFileChange{Path: diffHeaderPath(line)})
			current, inHunk = len(files)-1, false
			continue
		}
		if current < 0 {
			continue
		}
		if !inHunk {
			switch {
			case strings.HasPrefix(line, "@@"):
				inHunk = true
			case strings.HasPrefix(line, "new file mode"):
				files[current].New = true
			case strings.HasPrefix(line, "+++ "):
				if path := strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/"); path != "/dev/null" {
					files[current].Path = path
				}
			}
			continue
		}
		if strings.HasPrefix(line, "@@") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			files[current].Added++
			files[current].Lines = keepLast(files[current].Lines, liveLine(strings.TrimPrefix(line, "+")))
		case strings.HasPrefix(line, "-"):
			files[current].Removed++
		}
	}
	kept := files[:0]
	for _, file := range files {
		if file.Path != "" {
			kept = append(kept, file)
		}
	}
	return kept
}

// diffHeaderPath reads the new name from `diff --git a/<old> b/<new>`.
func diffHeaderPath(line string) string {
	header := strings.TrimPrefix(line, "diff --git ")
	if i := strings.LastIndex(header, " b/"); i >= 0 {
		return header[i+len(" b/"):]
	}
	return ""
}

// readNewFile reads a file the run created, inside its worktree only. Binary
// and oversized files are counted but show no lines.
func readNewFile(root, name string) (ports.LiveFileChange, bool) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ports.LiveFileChange{}, false
	}
	path := filepath.Join(root, clean)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ports.LiveFileChange{}, false
	}
	change := ports.LiveFileChange{Path: filepath.ToSlash(clean), New: true}
	if info.Size() > liveMaxFileBytes {
		return change, true
	}
	content, err := os.ReadFile(path) //nolint:gosec // a file Git lists inside the run's own worktree
	if err != nil || bytes.IndexByte(content, 0) >= 0 {
		return change, true
	}
	text := strings.TrimRight(string(content), "\n")
	if text == "" {
		return change, true
	}
	for _, line := range strings.Split(text, "\n") {
		change.Added++
		change.Lines = keepLast(change.Lines, liveLine(line))
	}
	return change, true
}

// newestFirst orders files by when they last changed and keeps the newest.
func newestFirst(root string, files []ports.LiveFileChange) []ports.LiveFileChange {
	changed := make([]time.Time, len(files))
	order := make([]int, len(files))
	for i, file := range files {
		order[i] = i
		if info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(file.Path))); err == nil {
			changed[i] = info.ModTime()
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return changed[order[a]].After(changed[order[b]]) })
	out := make([]ports.LiveFileChange, 0, min(len(files), liveMaxFiles))
	for _, i := range order {
		if len(out) == liveMaxFiles {
			break
		}
		out = append(out, files[i])
	}
	return out
}

func keepLast(lines []string, line string) []string {
	lines = append(lines, line)
	if len(lines) > liveMaxLines {
		lines = lines[len(lines)-liveMaxLines:]
	}
	return lines
}

// liveLine bounds one line of code and removes anything that looks like a secret.
func liveLine(line string) string {
	line = strings.TrimRight(line, "\r")
	if runes := []rune(line); len(runes) > liveMaxLineRunes {
		line = string(runes[:liveMaxLineRunes])
	}
	return RedactSecrets(line)
}
