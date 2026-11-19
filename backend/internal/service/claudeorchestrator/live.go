package claudeorchestrator

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The live board: for each run, how each step of its plan is going and the
// worktree it writes in, so the deck can show the crew at work and the code
// as it lands. It holds no model output beyond the plan's step titles, which
// PlanFor already serves.

var (
	_ ports.OrchestrationLiveReporter = (*Service)(nil)
	_ ports.OrchestrationSiteReporter = (*Service)(nil)
)

type liveRun struct {
	steps     []ports.LiveStep
	index     map[string]int
	workspace ports.RunWorkspace
}

// liveStart puts a plan's steps on the board, waiting. A revised plan keeps
// the steps already there and adds its new ones after them.
func (s *Service) liveStart(runID string, plan ports.ExecutionPlan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	board := s.liveRunLocked(runID)
	for _, task := range plan.Subtasks {
		if i, known := board.index[task.ID]; known {
			board.steps[i].Title = task.Title
			continue
		}
		board.index[task.ID] = len(board.steps)
		board.steps = append(board.steps, ports.LiveStep{
			Index: len(board.steps),
			ID:    task.ID,
			Title: task.Title,
			State: ports.LiveStepWaiting,
		})
	}
}

// liveWorkspace records the worktree a run writes in.
func (s *Service) liveWorkspace(runID string, workspace ports.RunWorkspace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.liveRunLocked(runID).workspace = workspace
}

// liveStep moves one step on the board.
func (s *Service) liveStep(runID, taskID string, state ports.LiveStepState, attempt int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	board, ok := s.live[runID]
	if !ok {
		return
	}
	i, known := board.index[taskID]
	if !known {
		return
	}
	board.steps[i].State = state
	if attempt > 0 {
		board.steps[i].Attempt = attempt
	}
	if state == ports.LiveStepDone {
		board.steps[i].Failure = ""
	}
}

// maxLiveFailureRunes bounds the reason a step shows for its last failed attempt.
const maxLiveFailureRunes = 280

// liveFailure records why a step's attempt failed.
func (s *Service) liveFailure(runID, taskID, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	board, ok := s.live[runID]
	if !ok {
		return
	}
	if i, known := board.index[taskID]; known {
		board.steps[i].Failure = boundedText(RedactSecrets(reason), maxLiveFailureRunes)
	}
}

func (s *Service) liveRunLocked(runID string) *liveRun {
	board, ok := s.live[runID]
	if !ok {
		board = &liveRun{index: map[string]int{}}
		s.live[runID] = board
	}
	return board
}

// LiveFor returns a run's steps and, when its worktree can be read, the code
// it has written so far. A run that has not reached its plan has no board.
func (s *Service) LiveFor(ctx context.Context, runID string) (ports.LiveWork, bool) {
	if s == nil {
		return ports.LiveWork{}, false
	}
	s.mu.RLock()
	board, ok := s.live[runID]
	var work ports.LiveWork
	var workspace ports.RunWorkspace
	if ok {
		work.Steps = append([]ports.LiveStep(nil), board.steps...)
		workspace = board.workspace
	}
	s.mu.RUnlock()
	if !ok {
		return ports.LiveWork{}, false
	}
	if reader, can := s.workspaces.(ports.RunWorkspaceChanges); can && workspace.Path != "" {
		if files, err := reader.Changes(ctx, workspace); err == nil {
			work.Files = files
		}
	}
	return work, true
}

// SiteFor returns where a run's pages are, once it has a worktree.
func (s *Service) SiteFor(runID string) (ports.RunSite, bool) {
	if s == nil {
		return ports.RunSite{}, false
	}
	s.mu.RLock()
	board, ok := s.live[runID]
	root := ""
	if ok {
		root = board.workspace.Path
	}
	s.mu.RUnlock()
	if root == "" {
		return ports.RunSite{}, false
	}
	site := ports.RunSite{Root: root}
	if s.sitePages != nil {
		site.Pages = s.sitePages(root)
	}
	return site, true
}
