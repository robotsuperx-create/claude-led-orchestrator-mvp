package orchestratorstore

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func newHistory(t *testing.T) (*FileRunHistory, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-orchestrator", "runs.json")
	history, err := NewFileRunHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	return history, path
}

func TestFileRunHistoryRoundTripsAtomicallyWithOwnerOnlyAccess(t *testing.T) {
	history, path := newHistory(t)
	if records, err := history.Load(); err != nil || len(records) != 0 {
		t.Fatalf("missing file Load = %v, %v; want empty", records, err)
	}
	now := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	want := []ports.ClaudeOrchestratorRunRecord{
		{RunID: "claude-run-a", State: ports.RunStateCompleted, Title: "Add tests", Branch: "ao/x", Commit: "abc", Recommendation: ports.MergeOutcomeMerge, CreatedAt: now, UpdatedAt: now},
		{RunID: "claude-run-b", State: ports.RunStateFailed, Title: "Refactor", Interrupted: true, CreatedAt: now, UpdatedAt: now},
	}
	if err := history.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := history.Load()
	if err != nil || len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Load = %+v, %v; want %+v", got, err, want)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("history mode = %v, %v; want 0600", info.Mode().Perm(), err)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only runs.json (no temp leftovers)", len(entries))
	}
}

func TestFileRunHistoryMovesAnUnreadableFileAside(t *testing.T) {
	for name, content := range map[string]string{
		"invalid JSON":  "{not json",
		"wrong version": `{"version":99,"runs":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			history, path := newHistory(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			records, err := history.Load()
			if err == nil || len(records) != 0 || !strings.Contains(err.Error(), "moved it") {
				t.Fatalf("Load = %v, %v; want an empty history and a moved-aside error", records, err)
			}
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Fatal("the unreadable file is still in place")
			}
			matches, _ := filepath.Glob(path + ".corrupt-*")
			if len(matches) != 1 {
				t.Fatalf("quarantined copies = %v, want one", matches)
			}
			// The next save starts a fresh, valid history.
			if err := history.Save(nil); err != nil {
				t.Fatal(err)
			}
			if records, err := history.Load(); err != nil || len(records) != 0 {
				t.Fatalf("Load after fresh save = %v, %v", records, err)
			}
		})
	}
}

func TestNewFileRunHistoryRequiresAnAbsolutePath(t *testing.T) {
	if _, err := NewFileRunHistory("runs.json"); err == nil {
		t.Fatal("a relative path was accepted")
	}
}
