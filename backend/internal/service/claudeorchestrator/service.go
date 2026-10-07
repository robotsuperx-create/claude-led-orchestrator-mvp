package claudeorchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Errors returned by Service.Run for invalid or conflicting requests.
var (
	ErrRunIDRequired       = errors.New("orchestration run ID is required")
	ErrTaskRequired        = errors.New("orchestration task is required")
	ErrRunAlreadyInProcess = errors.New("orchestration run is already in progress")
)

// Dependencies are the application boundaries used by Service. The service
// coordinates these ports but performs no provider, workspace, Git, or storage
// operations itself.
type Dependencies struct {
	Model     ports.ModelGateway
	Worker    ports.WorkerRuntime
	Validator ports.Validator
	Memory    ports.ProjectMemory
	// DefaultWorktreePath is the trusted operator-configured workspace used
	// when a request does not select one. Worker and validator still verify
	// it against the project root before running anything.
	DefaultWorktreePath string
	// Workspaces, when set, gives each run that has no explicit or default
	// worktree its own new worktree and branch, and commits the result there.
	Workspaces ports.RunWorkspaceProvisioner
}

// Service coordinates a single executive planning and delegation run. Run
// states are retained in memory for the lifetime of the service; durable
// outcome recording, when desired, is delegated to ProjectMemory.
type Service struct {
	model     ports.ModelGateway
	worker    ports.WorkerRuntime
	validator ports.Validator
	memory    ports.ProjectMemory

	defaultWorktreePath string
	workspaces          ports.RunWorkspaceProvisioner

	mu      sync.RWMutex
	states  map[string]ports.RunState
	running map[string]bool
	cancels map[string]context.CancelFunc
}

// New constructs the orchestration application service.
func New(deps Dependencies) *Service {
	return &Service{
		model:     deps.Model,
		worker:    deps.Worker,
		validator: deps.Validator,
		memory:    deps.Memory,

		defaultWorktreePath: strings.TrimSpace(deps.DefaultWorktreePath),
		workspaces:          deps.Workspaces,

		states:  make(map[string]ports.RunState),
		running: make(map[string]bool),
		cancels: make(map[string]context.CancelFunc),
	}
}

// RunState returns the most recently recorded in-memory state for runID.
func (s *Service) RunState(runID string) (ports.RunState, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.states[runID]
	return state, ok
}

// Cancel terminally cancels one opaque run ID and signals its execution
// context. Repeated cancellation is idempotent; other terminal states win.
func (s *Service) Cancel(runID string) bool {
	if s == nil || strings.TrimSpace(runID) == "" {
		return false
	}
	s.mu.Lock()
	state, exists := s.states[runID]
	if exists && isTerminal(state) {
		s.mu.Unlock()
		return state == ports.RunStateCanceled
	}
	s.states[runID] = ports.RunStateCanceled
	cancel := s.cancels[runID]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return true
}

// Run plans a task with the Claude executive, delegates each planned subtask,
// validates the collected work, requests an executive review, and returns a
// merge recommendation. A merge recommendation is advisory only; this service
// never applies it.
func (s *Service) Run(ctx context.Context, request ports.OrchestrationRequest) (ports.OrchestrationResult, error) {
	result := ports.OrchestrationResult{
		RunID: request.RunID,
		Task:  request.Task,
		State: ports.RunStatePending,
	}
	if s == nil {
		result.State = ports.RunStateFailed
		return result, errors.New("the Claude orchestrator service is unavailable")
	}
	if strings.TrimSpace(request.RunID) == "" {
		result.State = ports.RunStateFailed
		return result, ErrRunIDRequired
	}
	runCtx, cancel := context.WithCancel(ctx)
	canceled, reserved := s.reserve(request.RunID, cancel)
	if !reserved {
		cancel()
		if canceled {
			result.State = ports.RunStateCanceled
			return result, context.Canceled
		}
		result.State = ports.RunStateFailed
		return result, ErrRunAlreadyInProcess
	}
	defer s.release(request.RunID)
	defer cancel()

	if !s.advance(runCtx, request.RunID, ports.RunStatePending) {
		return canceledResult(result)
	}
	if strings.TrimSpace(request.Task) == "" {
		return s.fail(runCtx, result, ErrTaskRequired)
	}
	if s.model == nil || s.worker == nil || s.validator == nil || s.memory == nil {
		return s.fail(runCtx, result, errors.New("the Claude orchestrator dependencies are incomplete"))
	}

	if !s.advance(runCtx, request.RunID, ports.RunStatePlanning) {
		return canceledResult(result)
	}
	memoryContext, err := s.memory.ReadContext(runCtx, ports.MemoryContextRequest{Task: request.Task})
	if err != nil {
		return s.fail(runCtx, result, fmt.Errorf("read project memory: %w", err))
	}
	plan, err := s.model.Plan(runCtx, ports.PlanRequest{
		Provider:      ports.ModelProviderClaude,
		Task:          request.Task,
		MemoryContext: memoryContext.Content,
	})
	if err != nil {
		return s.fail(runCtx, result, fmt.Errorf("request executive plan: %w", err))
	}
	if err := validatePlan(plan); err != nil {
		result.Plan = plan
		return s.fail(runCtx, result, err)
	}
	result.Workspace, err = s.workspaceFor(runCtx, request)
	if err != nil {
		result.Plan = plan
		return s.fail(runCtx, result, fmt.Errorf("prepare run workspace: %w", err))
	}
	plan = bindTrustedWorkspace(plan, result.Workspace.Path)
	result.Plan = plan

	if !s.advance(runCtx, request.RunID, ports.RunStateExecuting) {
		return canceledResult(result)
	}
	result.Results = s.executePlan(runCtx, request, plan)

	if !s.advance(runCtx, request.RunID, ports.RunStateValidating) {
		return canceledResult(result)
	}
	result.Validation, err = s.validator.Validate(runCtx, ports.ValidationRequest{
		Task:    request.Task,
		Plan:    plan,
		Results: result.Results,
	})
	if err != nil {
		return s.fail(runCtx, result, fmt.Errorf("validate delegated work: %w", err))
	}

	if !s.advance(runCtx, request.RunID, ports.RunStateReviewing) {
		return canceledResult(result)
	}
	result.Review, err = s.model.Review(runCtx, ports.ReviewRequest{
		Provider:   ports.ModelProviderClaude,
		Task:       request.Task,
		Plan:       plan,
		Results:    result.Results,
		Validation: result.Validation,
	})
	if err != nil {
		return s.fail(runCtx, result, fmt.Errorf("request executive review: %w", err))
	}

	result.MergeDecision = decideMerge(result.Results, result.Validation, result.Review)
	if s.workspaces != nil && result.Workspace.Branch != "" {
		// Record the run's changes on its own branch, whatever the
		// recommendation, so held work can be inspected and resumed.
		result.Workspace, err = s.workspaces.Finalize(runCtx, result.Workspace, result)
		if err != nil {
			return s.fail(runCtx, result, fmt.Errorf("commit run workspace: %w", err))
		}
	}
	if result.MergeDecision.Decision == ports.MergeOutcomeMerge {
		result.State = ports.RunStateCompleted
	} else {
		result.State = ports.RunStateHeld
	}
	if !s.finish(runCtx, request.RunID, result.State) {
		return canceledResult(result)
	}
	if err := s.record(runCtx, result); err != nil {
		s.setRecordFailure(request.RunID, result.State)
		result.State = ports.RunStateFailed
		return result, fmt.Errorf("record orchestration outcome: %w", err)
	}
	return result, nil
}

func (s *Service) reserve(runID string, cancel context.CancelFunc) (canceled, reserved bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states[runID] == ports.RunStateCanceled {
		return true, false
	}
	if _, exists := s.states[runID]; exists || s.running[runID] {
		return false, false
	}
	s.running[runID] = true
	s.states[runID] = ports.RunStatePending
	s.cancels[runID] = cancel
	return false, true
}

func (s *Service) release(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, runID)
	delete(s.cancels, runID)
}

func (s *Service) advance(ctx context.Context, runID string, state ports.RunState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.states[runID]
	if current == ports.RunStateCanceled || ctx.Err() != nil {
		s.states[runID] = ports.RunStateCanceled
		return false
	}
	if isTerminal(current) {
		return false
	}
	s.states[runID] = state
	return true
}

// finish atomically arbitrates cancellation versus the run's terminal result.
func (s *Service) finish(ctx context.Context, runID string, state ports.RunState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.states[runID]
	if current == ports.RunStateCanceled || ctx.Err() != nil {
		s.states[runID] = ports.RunStateCanceled
		return false
	}
	if isTerminal(current) {
		return false
	}
	s.states[runID] = state
	return true
}

func (s *Service) setRecordFailure(runID string, prior ports.RunState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states[runID] == prior {
		s.states[runID] = ports.RunStateFailed
	}
}

func (s *Service) currentState(runID string) ports.RunState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.states[runID]
}

func canceledResult(result ports.OrchestrationResult) (ports.OrchestrationResult, error) {
	result.State = ports.RunStateCanceled
	return result, context.Canceled
}

func isTerminal(state ports.RunState) bool {
	switch state {
	case ports.RunStateCompleted, ports.RunStateHeld, ports.RunStateFailed, ports.RunStateCanceled:
		return true
	default:
		return false
	}
}

func (s *Service) fail(ctx context.Context, result ports.OrchestrationResult, cause error) (ports.OrchestrationResult, error) {
	if ctx.Err() != nil || s.currentState(result.RunID) == ports.RunStateCanceled {
		return canceledResult(result)
	}
	result.State = ports.RunStateFailed
	result.MergeDecision = ports.MergeDecision{
		Decision: ports.MergeOutcomeHold,
		Reasons:  []string{cause.Error()},
	}
	if !s.finish(ctx, result.RunID, ports.RunStateFailed) {
		return canceledResult(result)
	}
	if recordErr := s.record(ctx, result); recordErr != nil {
		cause = errors.Join(cause, fmt.Errorf("record failed orchestration outcome: %w", recordErr))
	}
	return result, cause
}

func (s *Service) record(ctx context.Context, result ports.OrchestrationResult) error {
	if s.memory == nil {
		return nil
	}
	summary := strings.TrimSpace(result.Plan.Summary)
	if summary == "" {
		summary = strings.TrimSpace(result.Review.Summary)
	}
	if summary == "" && result.MergeDecision.Decision == ports.MergeOutcomeHold && len(result.MergeDecision.Reasons) > 0 {
		summary = result.MergeDecision.Reasons[0]
	}
	return s.memory.RecordOutcome(context.WithoutCancel(ctx), ports.MemoryOutcome{
		RunID:         result.RunID,
		Task:          result.Task,
		Summary:       summary,
		RunState:      result.State,
		MergeDecision: result.MergeDecision,
	})
}

func (s *Service) executePlan(ctx context.Context, request ports.OrchestrationRequest, plan ports.ExecutionPlan) []ports.CollectedTaskResult {
	collected := make([]ports.CollectedTaskResult, 0, len(plan.Subtasks))
	maxAttempts := request.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for _, task := range plan.Subtasks {
		if ctx.Err() != nil {
			break
		}
		taskResult := ports.CollectedTaskResult{Task: task}
		previousFailure := ""
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			execution, err := s.worker.Execute(ctx, ports.WorkerRequest{
				Task:            task,
				Attempt:         attempt,
				PreviousFailure: previousFailure,
			})
			if err != nil {
				execution = ports.WorkerExecution{Status: ports.WorkerOutcomeFailed, Error: err.Error()}
			}
			taskResult.Attempts = append(taskResult.Attempts, ports.CollectedAttempt{Attempt: attempt, Execution: execution})
			taskResult.FinalExecution = execution
			if err == nil && execution.Status == ports.WorkerOutcomeCompleted {
				break
			}
			previousFailure = workerFailure(execution)
			if ctx.Err() != nil {
				break
			}
		}
		collected = append(collected, taskResult)
	}
	return collected
}

func validatePlan(plan ports.ExecutionPlan) error {
	if len(plan.Subtasks) == 0 {
		return errors.New("executive plan contains no subtasks")
	}
	seen := make(map[string]struct{}, len(plan.Subtasks))
	for i, task := range plan.Subtasks {
		if strings.TrimSpace(task.ID) == "" {
			return fmt.Errorf("executive plan subtask %d has no ID", i+1)
		}
		if strings.TrimSpace(task.WorkerID) == "" {
			return fmt.Errorf("executive plan subtask %q has no worker", task.ID)
		}
		if _, ok := seen[task.ID]; ok {
			return fmt.Errorf("executive plan contains duplicate subtask ID %q", task.ID)
		}
		seen[task.ID] = struct{}{}
	}
	return nil
}

// workspaceFor selects the trusted workspace for a run: the caller's explicit
// worktree, otherwise the operator-configured default, otherwise a new
// per-run worktree when a provisioner is configured.
func (s *Service) workspaceFor(ctx context.Context, request ports.OrchestrationRequest) (ports.RunWorkspace, error) {
	if path := strings.TrimSpace(request.WorktreePath); path != "" {
		return ports.RunWorkspace{Path: path}, nil
	}
	if s.defaultWorktreePath != "" {
		return ports.RunWorkspace{Path: s.defaultWorktreePath}, nil
	}
	if s.workspaces != nil {
		return s.workspaces.Prepare(ctx, request.RunID)
	}
	return ports.RunWorkspace{}, nil
}

// bindTrustedWorkspace replaces all subtask metadata with server-owned values.
// A model plan must never choose where commands run or what gets mounted, so
// any metadata an implementation decoded from model output is discarded.
func bindTrustedWorkspace(plan ports.ExecutionPlan, worktreePath string) ports.ExecutionPlan {
	bound := ports.ExecutionPlan{Summary: plan.Summary, Subtasks: make([]ports.PlannedSubtask, len(plan.Subtasks))}
	for i, task := range plan.Subtasks {
		task.Metadata = nil
		if worktreePath != "" {
			task.Metadata = map[string]string{ports.SubtaskMetadataKeyWorktreePath: worktreePath}
		}
		bound.Subtasks[i] = task
	}
	return bound
}

func workerFailure(execution ports.WorkerExecution) string {
	if detail := strings.TrimSpace(execution.Error); detail != "" {
		return detail
	}
	if summary := strings.TrimSpace(execution.Summary); summary != "" {
		return summary
	}
	if execution.Status != "" {
		return fmt.Sprintf("worker returned %q", execution.Status)
	}
	return "worker did not complete the task"
}

func decideMerge(results []ports.CollectedTaskResult, validation ports.ValidationReport, review ports.ReviewDecision) ports.MergeDecision {
	var reasons []string
	for _, result := range results {
		if result.FinalExecution.Status != ports.WorkerOutcomeCompleted {
			reasons = append(reasons, fmt.Sprintf("subtask %q did not complete: %s", result.Task.ID, workerFailure(result.FinalExecution)))
		}
	}
	if !validation.Passed {
		reasons = append(reasons, validation.Issues...)
		if len(validation.Issues) == 0 {
			reasons = append(reasons, "validation did not pass")
		}
	}
	if review.Decision != ports.ReviewOutcomeApprove {
		reasons = append(reasons, review.Issues...)
		if review.Decision == ports.ReviewOutcomeRequestChanges && strings.TrimSpace(review.Summary) != "" {
			reasons = append(reasons, review.Summary)
		}
		if len(review.Issues) == 0 && (review.Decision != ports.ReviewOutcomeRequestChanges || strings.TrimSpace(review.Summary) == "") {
			reasons = append(reasons, "executive review did not approve the work")
		}
	}
	if len(reasons) > 0 {
		return ports.MergeDecision{Decision: ports.MergeOutcomeHold, Reasons: reasons}
	}
	return ports.MergeDecision{Decision: ports.MergeOutcomeMerge}
}
