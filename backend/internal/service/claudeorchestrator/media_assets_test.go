package claudeorchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeMediaMaker struct {
	enabled bool
	asked   []string
}

func (f *fakeMediaMaker) Enabled() bool { return f.enabled }

func (f *fakeMediaMaker) MakePicture(_ context.Context, prompt string, width, height int) ([]byte, error) {
	f.asked = append(f.asked, prompt)
	return []byte("\x89PNG\r\n\x1a\npicture"), nil
}

func TestTheCrewsPicturesAreMadeAndPlacedInsideTheWorktreeOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".xrobot"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `[
		{"path": "public/images/hero.png", "prompt": "a car at dusk, the glass turning dark", "width": 1344, "height": 768},
		{"path": "../outside.png", "prompt": "escape"},
		{"path": ".git/hooks/x.png", "prompt": "hook"},
		{"path": "run.sh", "prompt": "a script"},
		{"path": "public/empty.png", "prompt": "  "}
	]`
	if err := os.WriteFile(filepath.Join(root, ".xrobot", "media.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	maker := &fakeMediaMaker{enabled: true}
	service := &Service{media: maker}
	service.fulfilMedia(context.Background(), root)
	data, err := os.ReadFile(filepath.Join(root, "public", "images", "hero.png"))
	if err != nil || !strings.HasPrefix(string(data), "\x89PNG") {
		t.Fatalf("hero.png = %q, %v", data, err)
	}
	if len(maker.asked) != 1 || maker.asked[0] != "a car at dusk, the glass turning dark" {
		t.Fatalf("asked = %q", maker.asked)
	}
	for _, path := range []string{filepath.Join(filepath.Dir(root), "outside.png"), filepath.Join(root, ".git", "hooks", "x.png"), filepath.Join(root, "run.sh"), filepath.Join(root, ".xrobot", "media.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists: %v", path, err)
		}
	}
}

func TestWritersHearOfTheGPUServerOnlyWhenItIsSet(t *testing.T) {
	task := ports.PlannedSubtask{ID: "s1", Instructions: "Build the landing page."}
	if got := (&Service{}).withMediaNote(task); got.Instructions != task.Instructions {
		t.Fatalf("no gateway, but the note was added: %q", got.Instructions)
	}
	if got := (&Service{media: &fakeMediaMaker{}}).withMediaNote(task); got.Instructions != task.Instructions {
		t.Fatalf("gateway off, but the note was added")
	}
	got := (&Service{media: &fakeMediaMaker{enabled: true}}).withMediaNote(task)
	if !strings.Contains(got.Instructions, mediaManifest) || !strings.HasPrefix(got.Instructions, "Build the landing page.") {
		t.Fatalf("instructions = %q", got.Instructions)
	}
	if twice := (&Service{media: &fakeMediaMaker{enabled: true}}).withMediaNote(got); twice.Instructions != got.Instructions {
		t.Fatal("the note was added twice")
	}
}
