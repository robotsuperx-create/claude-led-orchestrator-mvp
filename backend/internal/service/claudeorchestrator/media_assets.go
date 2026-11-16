package claudeorchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// MediaMaker makes a picture on the person's GPU server for a run (the media
// gateway). It is optional: without one, the crew is not told about it.
type MediaMaker interface {
	Enabled() bool
	MakePicture(ctx context.Context, prompt string, width, height int) ([]byte, error)
}

// The crew asks for pictures in this file inside the run's worktree; the
// gateway makes them after the subtasks and the file is removed, so it never
// reaches the commit.
const (
	mediaManifest       = ".xrobot/media.json"
	maxMediaRequests    = 8
	maxMediaManifest    = 64 << 10
	maxMediaPromptRunes = 2000
)

// mediaNote tells a writer it may ask for pictures instead of fetching them.
const mediaNote = `

Pictures: the owner has a GPU server that makes images. If this task needs pictures (hero images, product shots, backgrounds, illustrations), do not download them or use placeholder services. Write the file ` + mediaManifest + ` in the worktree as a JSON list, for example [{"path": "public/images/hero.png", "prompt": "a detailed English description of the picture", "width": 1344, "height": 768}], with at most 8 entries, and reference those paths in the code. Use .png, .jpg or .webp paths, sides between 256 and 2048. The pictures are made and placed at those paths after the subtasks finish.`

type mediaRequest struct {
	Path   string `json:"path"`
	Prompt string `json:"prompt"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// withMediaNote adds the note to a writer's task when the gateway can make pictures.
func (s *Service) withMediaNote(task ports.PlannedSubtask) ports.PlannedSubtask {
	if s.media == nil || !s.media.Enabled() || strings.Contains(task.Instructions, mediaManifest) {
		return task
	}
	task.Instructions += mediaNote
	return task
}

// fulfilMedia makes the pictures the crew asked for and puts them in place,
// inside the worktree only. A picture that cannot be made is skipped: the
// run goes on, and the review sees what is there.
func (s *Service) fulfilMedia(ctx context.Context, worktree string) {
	if s.media == nil || !s.media.Enabled() || worktree == "" {
		return
	}
	root, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return
	}
	manifest := filepath.Join(root, filepath.FromSlash(mediaManifest))
	info, err := os.Lstat(manifest)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxMediaManifest {
		return
	}
	data, err := os.ReadFile(manifest) //nolint:gosec // the manifest inside the run's own worktree
	_ = os.Remove(manifest)
	_ = os.Remove(filepath.Dir(manifest))
	if err != nil {
		return
	}
	var requests []mediaRequest
	if json.Unmarshal(data, &requests) != nil {
		return
	}
	if len(requests) > maxMediaRequests {
		requests = requests[:maxMediaRequests]
	}
	for _, request := range requests {
		target, ok := mediaTarget(root, request.Path)
		prompt := strings.TrimSpace(request.Prompt)
		if !ok || prompt == "" || utf8.RuneCountInString(prompt) > maxMediaPromptRunes {
			continue
		}
		picture, err := s.media.MakePicture(ctx, prompt, side(request.Width), side(request.Height))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil { //nolint:gosec // a folder the crew's own code refers to
			continue
		}
		// The folder may have been a link the crew made: check again where it lands.
		parent, err := filepath.EvalSymlinks(filepath.Dir(target))
		if err != nil || !inside(root, parent) {
			continue
		}
		_ = os.WriteFile(filepath.Join(parent, filepath.Base(target)), picture, 0o644) //nolint:gosec // a picture the crew's code shows
	}
}

// mediaTarget is where a picture goes: a relative path to a picture file,
// inside the worktree, never in .git.
func mediaTarget(root, raw string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(raw)))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	first := strings.Split(filepath.ToSlash(clean), "/")[0]
	if first == ".git" || first == ".xrobot" {
		return "", false
	}
	switch strings.ToLower(filepath.Ext(clean)) {
	case ".png", ".jpg", ".jpeg", ".webp":
	default:
		return "", false
	}
	target := filepath.Join(root, clean)
	return target, inside(root, target)
}

func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func side(value int) int {
	if value < 256 || value > 2048 {
		return 1024
	}
	return value
}
