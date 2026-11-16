package visualcheck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Camera photographs a run's pages for mission control and keeps each picture
// under Dir/<run>/ so people can see what the critic saw.
type Camera struct {
	Browser   string
	Pages     []string
	Dir       string
	Viewports []Viewport
}

var _ ports.VisualCapturer = Camera{}

var unsafeName = regexp.MustCompile(`[^a-z0-9-]+`)

// Capture renders the worktree's pages at every viewport. A worktree without
// web pages yields nothing, so non-web projects are never held up.
func (c Camera) Capture(ctx context.Context, runID string, round int, worktree string) ([]ports.VisualShot, error) {
	pages := FindPages(worktree, c.Pages)
	if len(pages) == 0 {
		return nil, nil
	}
	shots, err := Capture(ctx, Options{Browser: c.Browser, ProfileDir: filepath.Join(c.Dir, ".profiles"), Viewports: c.Viewports}, pages)
	if err != nil {
		return nil, err
	}
	folder := ""
	if c.Dir != "" {
		folder = filepath.Join(c.Dir, safeName(runID))
		if err := os.MkdirAll(folder, 0o700); err != nil {
			folder = ""
		}
	}
	out := make([]ports.VisualShot, 0, len(shots))
	for _, shot := range shots {
		kept := ports.VisualShot{
			Page:     shot.Page,
			Viewport: shot.Viewport.Name,
			Width:    shot.Viewport.Width,
			Height:   shot.Height,
			JPEG:     shot.JPEG,
			Findings: shot.Findings,
		}
		if folder != "" {
			name := fmt.Sprintf("r%d-%s-%s.jpg", round, safeName(strings.TrimSuffix(shot.Page, filepath.Ext(shot.Page))), safeName(shot.Viewport.Name))
			path := filepath.Join(folder, name)
			if os.WriteFile(path, shot.JPEG, 0o600) == nil {
				kept.File = path
			}
		}
		out = append(out, kept)
	}
	return out, nil
}

// safeName keeps a name to lowercase letters, digits and dashes.
func safeName(name string) string {
	clean := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if clean == "" {
		return "page"
	}
	if len(clean) > 64 {
		clean = clean[:64]
	}
	return clean
}
