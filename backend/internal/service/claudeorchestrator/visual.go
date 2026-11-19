package claudeorchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/writerskills"
)

// VisualReview photographs a run's web pages once its subtasks are done and
// lets a critic that sees them ask for polish: look, polish, look again, until
// the pages reach Target or Rounds polish rounds have run. A run whose
// worktree has no web pages is not affected.
type VisualReview struct {
	Capturer ports.VisualCapturer
	Critic   ports.VisualCritic
	// Rounds is how many polish rounds may follow the first look. Zero looks
	// once and only reports.
	Rounds int
	// Target is the score out of ten that ends the loop; zero means 9.
	Target float64
}

const (
	defaultVisualTarget = 9
	maxVisualRounds     = 5
	maxVisualBriefRunes = 6000
)

// polishSkills is the guidance every polish round's writer gets.
var polishSkills = []string{"design-taste", "responsive-accessible", "motion", "code-illustration"}

var _ ports.OrchestrationVisualReporter = (*Service)(nil)

// VisualReviewStatus reports whether runs get a visual review, and why not
// when one was asked for but cannot run.
func (s *Service) VisualReviewStatus() (bool, string) {
	if s == nil {
		return false, ""
	}
	if s.visual != nil && s.visual.Capturer != nil && s.visual.Critic != nil {
		return true, ""
	}
	return false, s.visualUnavailable
}

// VisualFor returns the visual rounds a run has had so far.
func (s *Service) VisualFor(runID string) []ports.VisualRound {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]ports.VisualRound(nil), s.visuals[runID]...)
}

// VisualShotFile returns where a run's kept picture is, by the name its
// round recorded. Only pictures the capturer reported can be found.
func (s *Service) VisualShotFile(runID, name string) (string, bool) {
	if s == nil || name == "" {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, round := range s.visuals[runID] {
		for _, shot := range round.Shots {
			if shot.Name == name && shot.File != "" {
				return shot.File, true
			}
		}
	}
	return "", false
}

func (s *Service) addVisual(runID string, result *ports.OrchestrationResult, round ports.VisualRound) {
	result.Visual = append(result.Visual, round)
	s.mu.Lock()
	s.visuals[runID] = append(s.visuals[runID], round)
	s.mu.Unlock()
}

// reviewVisually runs the visual loop and returns the plan with any polish
// subtasks added. Failures to photograph or judge are recorded, not fatal:
// the run goes on to validation and review with what it has.
func (s *Service) reviewVisually(ctx context.Context, request ports.OrchestrationRequest, plan ports.ExecutionPlan, result *ports.OrchestrationResult) ports.ExecutionPlan {
	review := s.visual
	if review == nil || review.Capturer == nil || review.Critic == nil {
		return plan
	}
	worktree := result.Workspace.Path
	if worktree == "" {
		worktree = s.defaultWorktreePath
	}
	if worktree == "" {
		return plan
	}
	rounds := min(max(review.Rounds, 0), maxVisualRounds)
	target := review.Target
	if target <= 0 {
		target = defaultVisualTarget
	}
	brief := visualBrief(plan)
	var previous []string
	for look := 1; look <= rounds+1; look++ {
		if ctx.Err() != nil {
			return plan
		}
		shots, err := review.Capturer.Capture(ctx, request.RunID, look, worktree)
		if err != nil {
			s.addVisual(request.RunID, result, ports.VisualRound{Round: look, Error: boundedRedacted("photograph the pages: " + err.Error())})
			return plan
		}
		if len(shots) == 0 {
			return plan
		}
		round := ports.VisualRound{Round: look, Shots: shotRecords(shots)}
		for _, shot := range shots {
			round.Findings += len(shot.Findings)
		}
		critique, err := review.Critic.Critique(ctx, ports.VisualCritiqueRequest{
			Provider:      s.planner,
			Task:          request.Task,
			Brief:         brief,
			Shots:         shots,
			Round:         look,
			PreviousFixes: previous,
		})
		if err != nil {
			round.Error = boundedRedacted("judge the pages: " + err.Error())
			s.addVisual(request.RunID, result, round)
			return plan
		}
		round.Score, round.Scores, round.Summary, round.Fixes = critique.Score, critique.Scores, critique.Summary, critique.Fixes
		if critique.Score >= target || len(critique.Fixes) == 0 || look > rounds {
			s.addVisual(request.RunID, result, round)
			return plan
		}
		polish := ports.PlannedSubtask{
			ID:           fmt.Sprintf("visual-%d", look),
			Title:        fmt.Sprintf("Visual polish, round %d (scored %.1f/10)", look, critique.Score),
			Instructions: polishInstructions(critique, shots),
			WorkerID:     "visual-critic",
			Provider:     s.writer,
			// Polish is judged by eye: the design model writes it.
			Tier:   ports.SubtaskTierDesign,
			Skills: writerskills.Known(polishSkills),
		}
		round.Polish = polish.ID
		s.addVisual(request.RunID, result, round)
		polishPlan := bindTrustedWorkspace(ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{polish}}, result.Workspace.Path)
		result.Results = append(result.Results, s.executePlan(ctx, request, polishPlan)...)
		plan.Subtasks = append(plan.Subtasks, polishPlan.Subtasks...)
		previous = critique.Fixes
	}
	return plan
}

// visualBrief is what the plan asked the pages to be.
func visualBrief(plan ports.ExecutionPlan) string {
	var brief strings.Builder
	for _, task := range plan.Subtasks {
		fmt.Fprintf(&brief, "%s: %s\n", strings.TrimSpace(task.Title), strings.TrimSpace(task.Instructions))
	}
	text := strings.TrimSpace(brief.String())
	if runes := []rune(text); len(runes) > maxVisualBriefRunes {
		text = string(runes[:maxVisualBriefRunes]) + "…"
	}
	return text
}

// polishInstructions turns the critic's verdict into a writer's task.
func polishInstructions(critique ports.VisualCritique, shots []ports.VisualShot) string {
	var text strings.Builder
	fmt.Fprintf(&text, "An art director reviewed screenshots of these pages at desktop, tablet and phone sizes and scored them %.1f out of 10.\n", critique.Score)
	if summary := strings.TrimSpace(critique.Summary); summary != "" {
		fmt.Fprintf(&text, "Their verdict: %s\n", summary)
	}
	text.WriteString("\nMake these changes, most important first. Keep the design's direction, content and everything that already works; change only what the fixes need.\n")
	for i, fix := range critique.Fixes {
		fmt.Fprintf(&text, "%d. %s\n", i+1, fix)
	}
	var measured []string
	seen := map[string]bool{}
	for _, shot := range shots {
		for _, finding := range shot.Findings {
			line := fmt.Sprintf("%s at %s: %s", shot.Page, shot.Viewport, finding)
			if !seen[line] && len(measured) < 12 {
				seen[line] = true
				measured = append(measured, line)
			}
		}
	}
	if len(measured) > 0 {
		text.WriteString("\nThe browser also measured these problems; fix them too:\n")
		for _, line := range measured {
			fmt.Fprintf(&text, "- %s\n", line)
		}
	}
	text.WriteString("\nChoose every file you change in the files to read, and change existing files with targeted replacements.")
	return text.String()
}

func shotRecords(shots []ports.VisualShot) []ports.VisualShotRecord {
	records := make([]ports.VisualShotRecord, 0, len(shots))
	for _, shot := range shots {
		record := ports.VisualShotRecord{Page: shot.Page, Viewport: shot.Viewport, Findings: shot.Findings}
		if shot.File != "" {
			record.Name = filepath.Base(shot.File)
			record.File = shot.File
		}
		records = append(records, record)
	}
	return records
}
