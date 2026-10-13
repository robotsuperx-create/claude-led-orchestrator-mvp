package ports

import (
	"context"
	"errors"
	"time"
)

// ErrAgentAttemptFailed marks an agent attempt that the orchestrator treats as
// a failed, retryable attempt rather than an infrastructure fault: the agent
// stopped, asked for help, or produced a change that cannot be integrated.
var ErrAgentAttemptFailed = errors.New("agent attempt failed")

// AgentImplementRequest asks a real AO coding agent to implement one planned
// subtask. WorktreePath is the verified run worktree that receives the agent's
// commits; it is server-trusted and never shown to a model.
type AgentImplementRequest struct {
	WorktreePath    string
	Task            PlannedSubtask
	Attempt         int
	PreviousFailure string
}

// AgentWork is an agent's finished, committed work on its own branch, ready
// to be integrated into the run worktree.
type AgentWork struct {
	// Summary is a short, redacted description for the run record.
	Summary string
	// Branch holds the agent's commits; Base is the run commit it was cut from.
	Branch string
	Base   string
}

// AgentImplementer dispatches a subtask to an AO agent session in two phases.
// Implement may run concurrently for independent subtasks: it returns once the
// agent's work is committed on its own branch. Integrate merges that work into
// the run worktree; callers serialize it with anything else that touches the
// run worktree. Errors wrapping ErrAgentAttemptFailed are retryable.
type AgentImplementer interface {
	Implement(ctx context.Context, request AgentImplementRequest) (AgentWork, error)
	Integrate(ctx context.Context, worktreePath string, work AgentWork) error
}

// ClaudeOrchestratorRunRecord is the durable, display-safe record of one run:
// identity, lifecycle, where its work lives, and a redacted title. It never
// holds prompts, model output, command output, or credentials.
type ClaudeOrchestratorRunRecord struct {
	RunID          string       `json:"runId"`
	State          RunState     `json:"state"`
	Title          string       `json:"title"`
	Branch         string       `json:"branch,omitempty"`
	Commit         string       `json:"commit,omitempty"`
	Recommendation MergeOutcome `json:"recommendation,omitempty"`
	// Interrupted marks a run that was still active when the daemon stopped.
	Interrupted bool      `json:"interrupted,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ClaudeOrchestratorRunHistory persists run records across daemon restarts.
// Save receives the complete, bounded history, oldest first.
type ClaudeOrchestratorRunHistory interface {
	Load() ([]ClaudeOrchestratorRunRecord, error)
	Save([]ClaudeOrchestratorRunRecord) error
}
