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
	maxRepositoryFilesListed = 5000
	maxFilesRead             = 20
	maxReadFileBytes         = 128 * 1024
	maxReadTotalBytes        = 512 * 1024
	maxEditsPerAttempt       = 50
	maxReplacementsPerEdit   = 100
	maxEditFileBytes         = 512 * 1024
	maxEditTotalBytes        = 4 * 1024 * 1024
)

var errProtectedPath = errors.New("path is protected")

// unlistedExtensions are file types a coding model can neither read (they are
// not UTF-8 text) nor usefully write, so listing them only costs tokens.
var unlistedExtensions = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".webp": {}, ".avif": {}, ".ico": {}, ".bmp": {}, ".icns": {}, ".tif": {}, ".tiff": {},
	".woff": {}, ".woff2": {}, ".ttf": {}, ".otf": {}, ".eot": {},
	".mp3": {}, ".mp4": {}, ".wav": {}, ".ogg": {}, ".webm": {}, ".mov": {}, ".m4a": {},
	".zip": {}, ".gz": {}, ".tgz": {}, ".tar": {}, ".7z": {}, ".rar": {}, ".jar": {},
	".exe": {}, ".dll": {}, ".so": {}, ".dylib": {}, ".bin": {}, ".wasm": {}, ".pdf": {},
}

// listedForModel reports whether a repository file is worth naming to a
// coding model.
func listedForModel(clean string) bool {
	_, unlisted := unlistedExtensions[strings.ToLower(path.Ext(clean))]
	return !unlisted
}

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
	contents := make([]string, len(edits))
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
		if err := ensureNoSymlinkComponents(root, clean); err != nil {
			return appliedEdits{}, err
		}
		info, statErr := root.Lstat(clean)
		exists := statErr == nil
		if exists && !info.Mode().IsRegular() {
			return appliedEdits{}, fmt.Errorf("%s exists and is not a regular file", clean)
		} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return appliedEdits{}, fmt.Errorf("inspect %s: %w", clean, statErr)
		}
		content := edit.Content
		switch {
		case len(edit.Replace) > 0 && (edit.Delete || edit.Content != ""):
			return appliedEdits{}, fmt.Errorf("edit to %s sets replace together with content or delete; use one of them", clean)
		case len(edit.Replace) > 0:
			if !exists {
				return appliedEdits{}, fmt.Errorf("edit to %s replaces text in a file that does not exist; use content to create it", clean)
			}
			if content, err = replaceInFile(root, clean, info.Size(), edit.Replace); err != nil {
				return appliedEdits{}, err
			}
		}
		if !edit.Delete {
			if len(content) > maxEditFileBytes {
				return appliedEdits{}, fmt.Errorf("edit to %s exceeds %d bytes", clean, maxEditFileBytes)
			}
			if !utf8.ValidString(content) {
				return appliedEdits{}, fmt.Errorf("edit to %s is not valid UTF-8 text", clean)
			}
			total += len(content)
		}
		cleaned[i] = clean
		contents[i] = content
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
		if err := root.WriteFile(clean, []byte(contents[i]), mode); err != nil {
			return applied, fmt.Errorf("write %s: %w", clean, err)
		}
		applied.Written = append(applied.Written, clean)
	}
	return applied, nil
}

// replaceInFile applies replacements, in order, to the current content of an
// existing file and returns the result; nothing is written here. Each old
// passage must occur exactly once, so a model that misremembers the file gets
// an error it can act on instead of a silent wrong change. A file checked out
// with CRLF line endings (core.autocrlf on Windows) also matches a passage
// written with plain newlines.
func replaceInFile(root *os.Root, clean string, size int64, replacements []ports.TextReplacement) (string, error) {
	if len(replacements) > maxReplacementsPerEdit {
		return "", fmt.Errorf("edit to %s has %d replacements; at most %d are allowed", clean, len(replacements), maxReplacementsPerEdit)
	}
	if size > maxEditFileBytes {
		return "", fmt.Errorf("%s is larger than %d bytes and cannot be edited", clean, maxEditFileBytes)
	}
	data, err := root.ReadFile(clean)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", clean, err)
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("%s is not UTF-8 text and cannot be edited", clean)
	}
	text := string(data)
	crlf := strings.Contains(text, "\r\n")
	for i, replacement := range replacements {
		oldText, newText := replacement.Old, replacement.New
		if oldText == "" {
			return "", fmt.Errorf("replacement %d for %s has no old text", i+1, clean)
		}
		count := strings.Count(text, oldText)
		if count == 0 && crlf && !strings.Contains(oldText, "\r") {
			oldText = strings.ReplaceAll(oldText, "\n", "\r\n")
			if !strings.Contains(newText, "\r") {
				newText = strings.ReplaceAll(newText, "\n", "\r\n")
			}
			count = strings.Count(text, oldText)
		}
		switch count {
		case 1:
			text = strings.Replace(text, oldText, newText, 1)
		case 0:
			return "", fmt.Errorf("replacement %d for %s: the old text is not in the file; copy it exactly from the file's current content", i+1, clean)
		default:
			return "", fmt.Errorf("replacement %d for %s: the old text occurs %d times; include neighbouring lines so it occurs once", i+1, clean, count)
		}
		if len(text) > maxEditFileBytes {
			return "", fmt.Errorf("edit to %s exceeds %d bytes", clean, maxEditFileBytes)
		}
	}
	return text, nil
}

// requireReadBeforeRewrite rejects a proposal that rewrites or deletes an
// existing file the model was not given. The model sees the repository listing
// only while it chooses files, so a "new" file it names may already exist;
// writing it blind would silently replace someone's work. A replacement needs
// no such check: it only applies to text that is in the file.
func requireReadBeforeRewrite(worktree string, edits []ports.FileEdit, read []ports.FileSnapshot) error {
	given := make(map[string]struct{}, len(read))
	for _, snapshot := range read {
		given[snapshot.Path] = struct{}{}
	}
	root, err := os.OpenRoot(worktree)
	if err != nil {
		return fmt.Errorf("open worktree: %w", err)
	}
	defer func() { _ = root.Close() }()
	for _, edit := range edits {
		if len(edit.Replace) > 0 {
			continue
		}
		clean, err := cleanRepoPath(edit.Path)
		if err != nil {
			continue // applyEdits rejects it with the reason
		}
		if _, ok := given[clean]; ok {
			continue
		}
		if _, err := root.Lstat(clean); err == nil {
			verb := "rewrites"
			if edit.Delete {
				verb = "deletes"
			}
			return fmt.Errorf("the edit to %s %s an existing file you were not given; choose it in the files to read first, or use a new path", clean, verb)
		}
	}
	return nil
}
