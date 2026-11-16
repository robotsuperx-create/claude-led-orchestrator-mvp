package httpd

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/claudeorchestrator"
)

// Display bounds for a run's live work. Step titles are the plan's (shown by
// /plan already); code lines are the run's own writing in its own worktree,
// secret-redacted and bounded.
const (
	claudeOrchestratorMaxLiveSteps     = 40
	claudeOrchestratorMaxLiveFiles     = 12
	claudeOrchestratorMaxLiveLines     = 24
	claudeOrchestratorMaxLiveLineRunes = 240
	claudeOrchestratorMaxLivePath      = 200
	claudeOrchestratorMaxSitePages     = 8
)

// ClaudeOrchestratorLive is what a run is doing right now, for the deck.
type ClaudeOrchestratorLive struct {
	RunID string                       `json:"runId"`
	Steps []ClaudeOrchestratorLiveStep `json:"steps"`
	Files []ClaudeOrchestratorLiveFile `json:"files"`
}

// ClaudeOrchestratorLiveStep is one plan step and how it is going.
type ClaudeOrchestratorLiveStep struct {
	Index   int    `json:"index"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Attempt int    `json:"attempt,omitempty"`
}

// ClaudeOrchestratorLiveFile is one file the run has written to.
type ClaudeOrchestratorLiveFile struct {
	Path    string   `json:"path"`
	Added   int      `json:"added"`
	Removed int      `json:"removed"`
	New     bool     `json:"new,omitempty"`
	Lines   []string `json:"lines,omitempty"`
}

// ClaudeOrchestratorSite is where a run's pages are. Root is a local path: it
// is served to the desktop's main process over loopback, which keeps it and
// hands the renderer only the page names.
type ClaudeOrchestratorSite struct {
	RunID string   `json:"runId"`
	Root  string   `json:"root"`
	Pages []string `json:"pages"`
}

var liveStepStates = map[ports.LiveStepState]bool{
	ports.LiveStepWaiting: true,
	ports.LiveStepRunning: true,
	ports.LiveStepDone:    true,
	ports.LiveStepFailed:  true,
	ports.LiveStepBlocked: true,
}

func (api *ClaudeOrchestratorAPI) liveReporter() (ports.OrchestrationLiveReporter, bool) {
	if api.Service == nil {
		return nil, false
	}
	reporter, ok := api.Service.(ports.OrchestrationLiveReporter)
	return reporter, ok
}

func (api *ClaudeOrchestratorAPI) siteReporter() (ports.OrchestrationSiteReporter, bool) {
	if api.Service == nil {
		return nil, false
	}
	reporter, ok := api.Service.(ports.OrchestrationSiteReporter)
	return reporter, ok
}

// live serves a run's steps and the code it has written so far. A run that
// has not reached its plan yet has no steps and no files.
func (api *ClaudeOrchestratorAPI) live(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	runID := chi.URLParam(r, "runId")
	reporter, ok := api.liveReporter()
	if _, known := api.getRunState(runID); !known || !ok {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "RUN_NOT_FOUND", "Run was not found")
		return
	}
	work, _ := reporter.LiveFor(r.Context(), runID)
	envelope.WriteJSON(w, http.StatusOK, displayLive(runID, work))
}

func displayLive(runID string, work ports.LiveWork) ClaudeOrchestratorLive {
	shown := ClaudeOrchestratorLive{RunID: runID, Steps: []ClaudeOrchestratorLiveStep{}, Files: []ClaudeOrchestratorLiveFile{}}
	for _, step := range work.Steps {
		if len(shown.Steps) == claudeOrchestratorMaxLiveSteps {
			break
		}
		if !liveStepStates[step.State] {
			continue
		}
		shown.Steps = append(shown.Steps, ClaudeOrchestratorLiveStep{
			Index:   step.Index,
			ID:      boundedPlanText(step.ID, claudeOrchestratorMaxPlanIdentRunes),
			Title:   boundedPlanText(step.Title, claudeOrchestratorMaxPlanTitleRunes),
			State:   string(step.State),
			Attempt: max(step.Attempt, 0),
		})
	}
	for _, file := range work.Files {
		if len(shown.Files) == claudeOrchestratorMaxLiveFiles {
			break
		}
		path := livePath(file.Path)
		if path == "" {
			continue
		}
		item := ClaudeOrchestratorLiveFile{Path: path, Added: max(file.Added, 0), Removed: max(file.Removed, 0), New: file.New}
		lines := file.Lines
		if len(lines) > claudeOrchestratorMaxLiveLines {
			lines = lines[len(lines)-claudeOrchestratorMaxLiveLines:]
		}
		for _, line := range lines {
			item.Lines = append(item.Lines, liveCodeLine(line))
		}
		shown.Files = append(shown.Files, item)
	}
	return shown
}

// livePath keeps a repository-relative path: never absolute, never climbing out.
func livePath(path string) string {
	path = strings.ToValidUTF8(strings.TrimSpace(filepath.ToSlash(path)), "")
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, ":") || strings.ContainsAny(path, "\x00\r\n") {
		return ""
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return ""
		}
	}
	if runes := []rune(path); len(runes) > claudeOrchestratorMaxLivePath {
		return ""
	}
	return path
}

// liveCodeLine keeps a line's indentation, removes anything that looks like a
// secret, and bounds it.
func liveCodeLine(line string) string {
	line = claudeorchestrator.RedactSecrets(strings.ToValidUTF8(strings.TrimRight(line, "\r\n"), ""))
	if runes := []rune(line); len(runes) > claudeOrchestratorMaxLiveLineRunes {
		line = string(runes[:claudeOrchestratorMaxLiveLineRunes])
	}
	return line
}

// site serves where a run's pages are, to the desktop's main process only:
// the response carries a local path, so it must never reach a browser.
func (api *ClaudeOrchestratorAPI) site(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	runID := chi.URLParam(r, "runId")
	reporter, ok := api.siteReporter()
	if _, known := api.getRunState(runID); !known || !ok {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "RUN_NOT_FOUND", "Run was not found")
		return
	}
	site, found := reporter.SiteFor(runID)
	if !found || !filepath.IsAbs(site.Root) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "SITE_NOT_FOUND", "The run has no pages yet")
		return
	}
	shown := ClaudeOrchestratorSite{RunID: runID, Root: filepath.Clean(site.Root), Pages: []string{}}
	for _, page := range site.Pages {
		if len(shown.Pages) == claudeOrchestratorMaxSitePages {
			break
		}
		if path := livePath(page); path != "" {
			shown.Pages = append(shown.Pages, path)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	envelope.WriteJSON(w, http.StatusOK, shown)
}
