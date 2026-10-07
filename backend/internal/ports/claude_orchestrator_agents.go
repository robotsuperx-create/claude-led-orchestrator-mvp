package ports

import (
	"context"
	"errors"
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

// AgentImplementer dispatches a subtask to an AO agent session and integrates
// the finished work into the run worktree. It returns a short summary. Errors
// wrapping ErrAgentAttemptFailed are retryable attempt failures.
type AgentImplementer interface {
	Implement(ctx context.Context, request AgentImplementRequest) (string, error)
}
