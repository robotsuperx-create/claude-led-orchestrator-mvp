package claudeorchestrator

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Limits on what a coding model may read and write in one attempt. They bound
// prompt size, provider cost, and the blast radius of a bad proposal.
const (
	maxRepositoryFilesListed = 2000
	maxFilesRead             = 20
	maxReadFileBytes         = 128 * 1024
	maxReadTotalBytes        = 512 * 1024
	maxEditsPerAttempt       = 50
	maxEditFileBytes         = 512 * 1024
	maxEditTotalBytes        = 4 * 1024 * 1024
)

var errProtectedPath = errors.New("path is protected")

// cleanRepoPath validates a model-supplied, slash-separated path relative to
// the worktree root and returns its clean form. It rejects absolute paths,
// traversal, Windows drive and separator forms, NUL bytes, and protected
// locations such as .git and secret files.
func cleanRepoPath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) || len(name) > 1024 {
		return "", fmt.Errorf("invalid path %q", name)
	}
	if strings.HasPrefix(name, "/") || (len(name) >= 2 && name[1] == ':') {
		return "", fmt.Errorf("path %q must be relative to the repository root", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("path %q must not contain '..'", name)
		}
	}
	clean := path.Clean(name)
	if clean == "." || !fs.ValidPath(clean) {
		return "", fmt.Errorf("invalid path %q", name)
	}
	if protectedRepoPath(clean) {
		return "", fmt.Errorf("%w: %s", errProtectedPath, clean)
	}
	return clean, nil
}

// protectedRepoPath reports paths a coding model may neither read nor write:
// Git internals and common credential or secret files.
func protectedRepoPath(clean string) bool {
	parts := strings.Split(clean, "/")
	for _, part := range parts {
		switch strings.ToLower(part) {
		case ".git", ".ssh", ".aws", ".gnupg", ".kube", ".docker":
			return true
		}
	}
	base := strings.ToLower(parts[len(parts)-1])
	switch base {
	case ".envrc", ".netrc", ".npmrc", ".pypirc", "credentials", "credentials.json", ".git-credentials":
		return true
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasPrefix(base, "id_rsa") ||
		strings.HasPrefix(base, "id_ed25519") || strings.HasPrefix(base, "id_ecdsa") {
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".tfstate", ".tfvars"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// ensureNoSymlinkComponents rejects a path whose existing components include
// a symlink. os.Root already prevents escaping the worktree; this also stops
// an in-tree link from redirecting a write into a protected location.
func ensureNoSymlinkComponents(root *os.Root, clean string) error {
	parts := strings.Split(clean, "/")
	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", prefix, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("path %s traverses a symbolic link", prefix)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("path %s is not a directory", prefix)
		}
	}
	return nil
}

// readSnapshots returns the content of the requested files. Paths outside
// the repository file list, protected paths, symlinks, non-regular files,
// binary files, and anything beyond the read budget are skipped.
func readSnapshots(worktree string, requested, repositoryFiles []string) ([]ports.FileSnapshot, error) {
	root, err := os.OpenRoot(worktree)
	if err != nil {
		return nil, fmt.Errorf("open worktree: %w", err)
	}
	defer func() { _ = root.Close() }()

	known := make(map[string]struct{}, len(repositoryFiles))
	for _, file := range repositoryFiles {
		known[file] = struct{}{}
	}
	seen := make(map[string]struct{})
	var snapshots []ports.FileSnapshot
	total := 0
	for _, name := range requested {
		if len(snapshots) >= maxFilesRead {
			break
		}
		clean, err := cleanRepoPath(name)
		if err != nil {
			continue
		}
		if _, ok := known[clean]; !ok {
			continue
		}
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		if ensureNoSymlinkComponents(root, clean) != nil {
			continue
		}
		info, err := root.Lstat(clean)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxReadFileBytes || total+int(info.Size()) > maxReadTotalBytes {
			continue
		}
		data, err := root.ReadFile(clean)
		if err != nil || !utf8.Valid(data) {
			continue
		}
		total += len(data)
		snapshots = append(snapshots, ports.FileSnapshot{Path: clean, Content: string(data)})
	}
	return snapshots, nil
}

// appliedEdits records what applyEdits changed, for the worker summary.
type appliedEdits struct {
	Written []string
	Deleted []string
}

// applyEdits validates the whole proposal first and only then writes, so an
// invalid proposal changes nothing. Every write is confined to worktree by
// os.Root and refuses symlinked or protected paths.
func applyEdits(worktree string, edits []ports.FileEdit) (appliedEdits, error) {
	if len(edits) == 0 {
		// A model may conclude nothing needs changing; the fixed commands and
		// the validator still decide whether the subtask is done.
		return appliedEdits{}, nil
	}
	if len(edits) > maxEditsPerAttempt {
		return appliedEdits{}, fmt.Errorf("the proposal has %d edits; at most %d are allowed", len(edits), maxEditsPerAttempt)
	}
	root, err := os.OpenRoot(worktree)
	if err != nil {
		return appliedEdits{}, fmt.Errorf("open worktree: %w", err)
	}
	defer func() { _ = root.Close() }()

	cleaned := make([]string, len(edits))
	seen := make(map[string]struct{}, len(edits))
	total := 0
	for i, edit := range edits {
		clean, err := cleanRepoPath(edit.Path)
		if err != nil {
			return appliedEdits{}, err
		}
		if _, dup := seen[clean]; dup {
			return appliedEdits{}, fmt.Errorf("the proposal edits %s more than once", clean)
		}
		seen[clean] = struct{}{}
		if !edit.Delete {
			if len(edit.Content) > maxEditFileBytes {
				return appliedEdits{}, fmt.Errorf("edit to %s exceeds %d bytes", clean, maxEditFileBytes)
			}
			if !utf8.ValidString(edit.Content) {
				return appliedEdits{}, fmt.Errorf("edit to %s is not valid UTF-8 text", clean)
			}
			total += len(edit.Content)
		}
		if err := ensureNoSymlinkComponents(root, clean); err != nil {
			return appliedEdits{}, err
		}
		if info, err := root.Lstat(clean); err == nil && !info.Mode().IsRegular() {
			return appliedEdits{}, fmt.Errorf("%s exists and is not a regular file", clean)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return appliedEdits{}, fmt.Errorf("inspect %s: %w", clean, err)
		}
		cleaned[i] = clean
	}
	if total > maxEditTotalBytes {
		return appliedEdits{}, fmt.Errorf("the proposal writes %d bytes; at most %d are allowed", total, maxEditTotalBytes)
	}

	var applied appliedEdits
	for i, edit := range edits {
		clean := cleaned[i]
		if edit.Delete {
			if err := root.Remove(clean); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return applied, fmt.Errorf("delete %s: %w", clean, err)
			}
			applied.Deleted = append(applied.Deleted, clean)
			continue
		}
		if dir := path.Dir(clean); dir != "." {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return applied, fmt.Errorf("create directory for %s: %w", clean, err)
			}
		}
		mode := fs.FileMode(0o644)
		if info, err := root.Lstat(clean); err == nil {
			mode = info.Mode().Perm()
		}
		if err := root.WriteFile(clean, []byte(edit.Content), mode); err != nil {
			return applied, fmt.Errorf("write %s: %w", clean, err)
		}
		applied.Written = append(applied.Written, clean)
	}
	return applied, nil
}
