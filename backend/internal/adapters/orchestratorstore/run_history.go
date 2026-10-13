package orchestratorstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// runHistoryVersion guards the file format so a future layout change can
// migrate instead of misreading.
const runHistoryVersion = 1

// maxRunHistoryBytes bounds what Load will read; 256 bounded records fit far
// below it, so a larger file is not ours to trust.
const maxRunHistoryBytes = 4 << 20

type runHistoryFile struct {
	Version int                                 `json:"version"`
	Runs    []ports.ClaudeOrchestratorRunRecord `json:"runs"`
}

// FileRunHistory keeps Claude orchestrator run records in one JSON file under
// the AO data directory. Writes are atomic (temp file, fsync, rename) and
// owner-only, so a crash leaves either the old or the new history, never a
// torn file. An unreadable file is moved aside rather than discarded.
type FileRunHistory struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
}

var _ ports.ClaudeOrchestratorRunHistory = (*FileRunHistory)(nil)

// NewFileRunHistory stores history at path, e.g. ~/.ao/claude-orchestrator/runs.json.
func NewFileRunHistory(path string) (*FileRunHistory, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, errors.New("run history path must be absolute")
	}
	return &FileRunHistory{path: filepath.Clean(path), now: time.Now}, nil
}

// Load returns the stored records, oldest first. A missing file is an empty
// history. A corrupt or foreign file is renamed with a timestamp suffix and
// an empty history is returned together with an error describing the move.
func (h *FileRunHistory) Load() ([]ports.ClaudeOrchestratorRunRecord, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	info, err := os.Stat(h.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat run history: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxRunHistoryBytes {
		return nil, h.quarantine("not a regular file within the size limit")
	}
	data, err := os.ReadFile(h.path)
	if err != nil {
		return nil, fmt.Errorf("read run history: %w", err)
	}
	var file runHistoryFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, h.quarantine("invalid JSON")
	}
	if file.Version != runHistoryVersion {
		return nil, h.quarantine(fmt.Sprintf("unsupported version %d", file.Version))
	}
	return file.Runs, nil
}

// Save replaces the stored history atomically.
func (h *FileRunHistory) Save(records []ports.ClaudeOrchestratorRunRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if records == nil {
		records = []ports.ClaudeOrchestratorRunRecord{}
	}
	data, err := json.MarshalIndent(runHistoryFile{Version: runHistoryVersion, Runs: records}, "", "\t")
	if err != nil {
		return fmt.Errorf("encode run history: %w", err)
	}
	dir := filepath.Dir(h.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create run history directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".runs-*.json")
	if err != nil {
		return fmt.Errorf("create run history temp file: %w", err)
	}
	tempPath := temp.Name()
	cleanup := func() { _ = os.Remove(tempPath) }
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		cleanup()
		return fmt.Errorf("protect run history temp file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		cleanup()
		return fmt.Errorf("write run history: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		cleanup()
		return fmt.Errorf("sync run history: %w", err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close run history: %w", err)
	}
	if err := os.Rename(tempPath, h.path); err != nil {
		cleanup()
		return fmt.Errorf("replace run history: %w", err)
	}
	return nil
}

func (h *FileRunHistory) quarantine(reason string) error {
	aside := fmt.Sprintf("%s.corrupt-%s", h.path, h.now().UTC().Format("20060102T150405Z"))
	if err := os.Rename(h.path, aside); err != nil {
		return fmt.Errorf("run history is unusable (%s) and could not be moved aside: %w", reason, err)
	}
	return fmt.Errorf("run history was unusable (%s); moved it to %s and started fresh", reason, filepath.Base(aside))
}
