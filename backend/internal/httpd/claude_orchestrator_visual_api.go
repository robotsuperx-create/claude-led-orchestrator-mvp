package httpd

import (
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Display bounds for a run's visual review. The summary, fixes and findings
// are the critic's and the browser's words; they are secret-redacted and
// bounded like a plan.
const (
	claudeOrchestratorMaxVisualRounds   = 10
	claudeOrchestratorMaxVisualShots    = 12
	claudeOrchestratorMaxVisualFixes    = 8
	claudeOrchestratorMaxVisualFindings = 20
	claudeOrchestratorMaxVisualScores   = 12
	claudeOrchestratorMaxVisualText     = 1200
	claudeOrchestratorMaxVisualLine     = 400
	// claudeOrchestratorMaxShotBytes bounds one served screenshot.
	claudeOrchestratorMaxShotBytes = 24 << 20
)

// visualShotName is how the camera names a kept picture: the round, the page
// and the screen size.
var visualShotName = regexp.MustCompile(`^r\d{1,2}-[a-z0-9-]{1,64}-[a-z0-9-]{1,64}\.jpg$`)

// ClaudeOrchestratorVisual is a run's visual review as shown to a person.
type ClaudeOrchestratorVisual struct {
	RunID  string                          `json:"runId"`
	Rounds []ClaudeOrchestratorVisualRound `json:"rounds"`
}

// ClaudeOrchestratorVisualRound is one look the critic took.
type ClaudeOrchestratorVisualRound struct {
	Round    int                            `json:"round"`
	Score    float64                        `json:"score"`
	Scores   map[string]float64             `json:"scores,omitempty"`
	Summary  string                         `json:"summary,omitempty"`
	Fixes    []string                       `json:"fixes,omitempty"`
	Shots    []ClaudeOrchestratorVisualShot `json:"shots,omitempty"`
	Polish   string                         `json:"polish,omitempty"`
	Failed   bool                           `json:"failed,omitempty"`
	Findings int                            `json:"findings,omitempty"`
}

// ClaudeOrchestratorVisualShot is one kept picture and what was measured.
type ClaudeOrchestratorVisualShot struct {
	Page     string   `json:"page"`
	Viewport string   `json:"viewport"`
	Name     string   `json:"name,omitempty"`
	Findings []string `json:"findings,omitempty"`
}

func (api *ClaudeOrchestratorAPI) visualReporter() (ports.OrchestrationVisualReporter, bool) {
	if api.Service == nil {
		return nil, false
	}
	reporter, ok := api.Service.(ports.OrchestrationVisualReporter)
	return reporter, ok
}

func (api *ClaudeOrchestratorAPI) visual(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	runID := chi.URLParam(r, "runId")
	reporter, ok := api.visualReporter()
	if _, known := api.getRunState(runID); !known || !ok {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "RUN_NOT_FOUND", "Run was not found")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, displayVisual(runID, reporter.VisualFor(runID)))
}

func displayVisual(runID string, rounds []ports.VisualRound) ClaudeOrchestratorVisual {
	if len(rounds) > claudeOrchestratorMaxVisualRounds {
		rounds = rounds[len(rounds)-claudeOrchestratorMaxVisualRounds:]
	}
	shown := ClaudeOrchestratorVisual{RunID: runID, Rounds: make([]ClaudeOrchestratorVisualRound, 0, len(rounds))}
	for _, round := range rounds {
		view := ClaudeOrchestratorVisualRound{
			Round:    round.Round,
			Score:    round.Score,
			Summary:  boundedPlanText(round.Summary, claudeOrchestratorMaxVisualText),
			Polish:   boundedPlanText(round.Polish, claudeOrchestratorMaxPlanIdentRunes),
			Failed:   round.Error != "",
			Findings: round.Findings,
		}
		for name, score := range round.Scores {
			if len(view.Scores) == claudeOrchestratorMaxVisualScores {
				break
			}
			if key := boundedPlanText(name, claudeOrchestratorMaxPlanIdentRunes); key != "" {
				if view.Scores == nil {
					view.Scores = map[string]float64{}
				}
				view.Scores[key] = score
			}
		}
		view.Fixes = boundedLines(round.Fixes, claudeOrchestratorMaxVisualFixes)
		for i, shot := range round.Shots {
			if i == claudeOrchestratorMaxVisualShots {
				break
			}
			item := ClaudeOrchestratorVisualShot{
				Page:     boundedPlanText(shot.Page, claudeOrchestratorMaxPlanTitleRunes),
				Viewport: boundedPlanText(shot.Viewport, claudeOrchestratorMaxPlanIdentRunes),
				Findings: boundedLines(shot.Findings, claudeOrchestratorMaxVisualFindings),
			}
			if visualShotName.MatchString(shot.Name) {
				item.Name = shot.Name
			}
			view.Shots = append(view.Shots, item)
		}
		shown.Rounds = append(shown.Rounds, view)
	}
	return shown
}

func boundedLines(lines []string, limit int) []string {
	var out []string
	for _, line := range lines {
		if len(out) == limit {
			break
		}
		if text := boundedPlanText(line, claudeOrchestratorMaxVisualLine); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// visualShot serves one picture the run's visual review kept. Only names the
// review recorded for that run are found, so no other file can be read.
func (api *ClaudeOrchestratorAPI) visualShot(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	runID, name := chi.URLParam(r, "runId"), chi.URLParam(r, "name")
	reporter, ok := api.visualReporter()
	if _, known := api.getRunState(runID); !known || !ok || !visualShotName.MatchString(name) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "SHOT_NOT_FOUND", "Screenshot was not found")
		return
	}
	path, found := reporter.VisualShotFile(runID, name)
	if !found {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "SHOT_NOT_FOUND", "Screenshot was not found")
		return
	}
	file, err := os.Open(path) //nolint:gosec // a path the service recorded for this run, never one from the request
	if err != nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "SHOT_NOT_FOUND", "Screenshot was not found")
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > claudeOrchestratorMaxShotBytes {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "SHOT_NOT_FOUND", "Screenshot was not found")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.LimitReader(file, claudeOrchestratorMaxShotBytes))
}
