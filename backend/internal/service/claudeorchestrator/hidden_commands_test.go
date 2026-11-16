package claudeorchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The desktop daemon runs without a console. On Windows a child started with
// a plain exec.Command gets a console of its own, and every git or check
// command of a run then opens a terminal window on the user's screen. Mission
// control starts its commands through internal/process, which hides them.
func TestMissionControlStartsEveryCommandHidden(t *testing.T) {
	raw := regexp.MustCompile(`\bexec\.Command(Context)?\(`)
	for _, dir := range []string{".", filepath.Join("..", "worktree")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			for number, line := range strings.Split(string(data), "\n") {
				if code := strings.TrimSpace(line); !strings.HasPrefix(code, "//") && raw.MatchString(code) {
					t.Errorf("%s:%d starts a command with os/exec directly; use process.CommandContext so no window opens on Windows",
						filepath.Join(dir, name), number+1)
				}
			}
		}
	}
}
