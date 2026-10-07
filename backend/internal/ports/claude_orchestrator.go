package ports

import "context"

// RunState describes the lifecycle of one orchestration run. These are domain
// states only; the ports package does not persist or expose them over HTTP.
type RunState string

// Run lifecycle states. Completed, held, failed, and canceled are terminal.
const (
	RunStatePending    RunState = "pending"
	RunStatePlanning   RunState = "planning"
	RunStateExecuting  RunState = "executing"
	RunStateValidating RunState = "validating"
	RunStateReviewing  RunState = "reviewing"
	RunStateCompleted  RunState = "completed"
	RunStateHeld       RunState = "held"
	RunStateFailed     RunState = "failed"
	RunStateCanceled   RunState = "canceled"
)

// ModelProvider names a provider selected for an executive or delegated model
// call. Provider adapters and credentials are owned outside this contract.
type ModelProvider string

// Model providers the orchestrator can route planning, review, and work to.
const (
	ModelProviderClaude   ModelProvider = "claude"
	ModelProviderDeepSeek ModelProvider = "deepseek"
	ModelProviderKimi     ModelProvider = "kimi"
	ModelProviderGemini   ModelProvider = "gemini"
	ModelProviderOpenAI   ModelProvider = "openai"
)

// OrchestrationRequest is the typed input for one end-to-end executive run.
type OrchestrationRequest struct {
	RunID      string
	Task       string
	MaxRetries int
	// WorktreePath optionally selects an existing linked worktree for this run.
	// It comes from the trusted caller, never from a model plan, and is still
	// verified against the configured project root before any command runs.
	// When empty, the service's configured default workspace is used.
	WorktreePath string
}

// ExecutiveOrchestrator coordinates planning, delegated work, validation,
// executive review, and a merge-or-hold recommendation. It does not perform a
// git merge or mutate a workspace itself.
type ExecutiveOrchestrator interface {
	Run(ctx context.Context, request OrchestrationRequest) (OrchestrationResult, error)
}

// OrchestrationRunCanceler cancels only the run named by its opaque run ID.
// Implementations should make repeated cancellation idempotent and never
// reopen a terminal non-canceled run.
type OrchestrationRunCanceler interface {
	Cancel(runID string) bool
}

// PlanRequest supplies the task and project context to the executive planner.
type PlanRequest struct {
	Provider      ModelProvider
	Task          string
	MemoryContext string
}

// SubtaskMetadataKeyWorktreePath names metadata containing the path of an
// existing linked worktree selected for a delegated subtask.
const SubtaskMetadataKeyWorktreePath = "worktree_path"

// PlannedSubtask is one bounded task delegated to a named worker and provider.
//
// The JSON tags are the wire contract with the executive model. Metadata is
// deliberately excluded from JSON: it carries server-trusted values such as
// the worktree path, so it is never accepted from, or echoed to, a model.
type PlannedSubtask struct {
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Instructions string            `json:"instructions"`
	WorkerID     string            `json:"worker_id"`
	Provider     ModelProvider     `json:"provider,omitempty"`
	Metadata     map[string]string `json:"-"`
}

// ExecutionPlan is the executive's typed decomposition of a task.
type ExecutionPlan struct {
	Summary  string           `json:"summary"`
	Subtasks []PlannedSubtask `json:"subtasks"`
}

// ReviewRequest supplies the collected implementation and validation results
// to the executive reviewer.
type ReviewRequest struct {
	Provider   ModelProvider         `json:"provider,omitempty"`
	Task       string                `json:"task"`
	Plan       ExecutionPlan         `json:"plan"`
	Results    []CollectedTaskResult `json:"results"`
	Validation ValidationReport      `json:"validation"`
}

// ReviewOutcome is the reviewer's recommendation for the collected work.
type ReviewOutcome string

// Executive review outcomes.
const (
	ReviewOutcomeApprove        ReviewOutcome = "approve"
	ReviewOutcomeRequestChanges ReviewOutcome = "request_changes"
)

// ReviewDecision is the typed result of the executive review.
type ReviewDecision struct {
	Decision ReviewOutcome `json:"decision"`
	Summary  string        `json:"summary"`
	Issues   []string      `json:"issues"`
}

// ModelGateway isolates executive planning and review from provider SDKs and
// transport details. The executive uses Claude; implementations are injected.
type ModelGateway interface {
	Plan(ctx context.Context, request PlanRequest) (ExecutionPlan, error)
	Review(ctx context.Context, request ReviewRequest) (ReviewDecision, error)
}

// WorkerRequest describes one attempt to execute a planned subtask.
type WorkerRequest struct {
	Task            PlannedSubtask
	Attempt         int
	PreviousFailure string
}

// WorkerOutcome is the typed disposition of one delegated attempt.
type WorkerOutcome string

// Worker attempt outcomes.
const (
	WorkerOutcomeCompleted WorkerOutcome = "completed"
	WorkerOutcomeFailed    WorkerOutcome = "failed"
	WorkerOutcomeBlocked   WorkerOutcome = "blocked"
)

// WorkerExecution contains the worker's textual output and any failure detail.
// It intentionally has no untyped payload or runtime-specific process handle.
type WorkerExecution struct {
	Status  WorkerOutcome `json:"status"`
	Summary string        `json:"summary,omitempty"`
	Output  string        `json:"output,omitempty"`
	Error   string        `json:"error,omitempty"`
}

// WorkerRuntime executes a single delegated attempt using the target in its
// request; lifecycle and process-management details remain adapter-owned.
type WorkerRuntime interface {
	Execute(ctx context.Context, request WorkerRequest) (WorkerExecution, error)
}

// CollectedAttempt pairs a numbered attempt with its typed execution result.
type CollectedAttempt struct {
	Attempt   int             `json:"attempt"`
	Execution WorkerExecution `json:"execution"`
}

// CollectedTaskResult records the attempts and final disposition for a subtask.
type CollectedTaskResult struct {
	Task           PlannedSubtask     `json:"task"`
	Attempts       []CollectedAttempt `json:"attempts"`
	FinalExecution WorkerExecution    `json:"final_execution"`
}

// ValidationRequest is the complete, typed input needed to validate a run.
type ValidationRequest struct {
	Task    string
	Plan    ExecutionPlan
	Results []CollectedTaskResult
}

// ValidationReport contains the outcome and human-readable validation issues.
type ValidationReport struct {
	Passed bool     `json:"passed"`
	Issues []string `json:"issues"`
}

// Validator checks collected work without prescribing a command runner or
// external service.
type Validator interface {
	Validate(ctx context.Context, request ValidationRequest) (ValidationReport, error)
}

// MergeOutcome is a recommendation only; applying it requires separate
// authorization and workspace/git operations.
type MergeOutcome string

// Merge recommendations. They are advisory; the service never merges.
const (
	MergeOutcomeMerge MergeOutcome = "merge"
	MergeOutcomeHold  MergeOutcome = "hold"
)

// MergeDecision is the executive's recommendation and its reasons.
type MergeDecision struct {
	Decision MergeOutcome
	Reasons  []string
}

// MemoryContextRequest asks for context relevant to the task being planned.
type MemoryContextRequest struct {
	Task string
}

// MemoryContext is the project memory's bounded textual context for planning.
type MemoryContext struct {
	Content string
}

// MemoryOutcome records the completed executive decision without prescribing
// the storage mechanism or persistence policy.
type MemoryOutcome struct {
	RunID         string
	Task          string
	Summary       string
	RunState      RunState
	MergeDecision MergeDecision
}

// ProjectMemory is the narrow read/record boundary used by an orchestration
// run. Implementations choose whether and how to retain the supplied values.
type ProjectMemory interface {
	ReadContext(ctx context.Context, request MemoryContextRequest) (MemoryContext, error)
	RecordOutcome(ctx context.Context, outcome MemoryOutcome) error
}

// OrchestrationResult is the typed result of one complete run. RunState is
// terminal for returned results: completed for a merge recommendation, held
// when the outcome needs changes or other gates, failed on run-level error, or
// canceled when cancellation wins the run's terminal-state race.
type OrchestrationResult struct {
	RunID         string
	State         RunState
	Task          string
	Plan          ExecutionPlan
	Results       []CollectedTaskResult
	Validation    ValidationReport
	Review        ReviewDecision
	MergeDecision MergeDecision
	// Workspace identifies where the run's changes live. It is empty when the
	// service ran without a trusted workspace.
	Workspace RunWorkspace
}

// RunWorkspace is the server-owned worktree a run executed in, its branch,
// and the commit recording the run's changes (empty when nothing changed).
type RunWorkspace struct {
	Path   string
	Branch string
	Commit string
}

// RunWorkspaceProvisioner gives each run an isolated worktree and records
// the result on the run's branch. Both steps use fixed Git argv only.
type RunWorkspaceProvisioner interface {
	Prepare(ctx context.Context, runID string) (RunWorkspace, error)
	Finalize(ctx context.Context, workspace RunWorkspace, result OrchestrationResult) (RunWorkspace, error)
}
