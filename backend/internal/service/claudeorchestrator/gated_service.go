package claudeorchestrator

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// GatedRunResult contains either the orchestration result or the typed run-gate
// rejection. A rejected run has a failed, held result for callers that need a
// uniform result shape, but is not recorded as an orchestration outcome.
type GatedRunResult struct {
	Result    ports.OrchestrationResult
	Rejection *ports.ClaudeOrchestratorRunRejectedError
}

// RunGated applies the run gate before delegating to Run. A missing gate is
// treated as disabled (default-deny); any gate error prevents the run from
// starting. Gate rejections are returned both as a typed result and as an error.
func (s *Service) RunGated(
	ctx context.Context,
	gate ports.ClaudeOrchestratorRunGate,
	gateRequest ports.ClaudeOrchestratorRunRequest,
	request ports.OrchestrationRequest,
) (GatedRunResult, error) {
	result := ports.OrchestrationResult{
		RunID: request.RunID,
		Task:  request.Task,
		State: ports.RunStateFailed,
	}

	if gate == nil {
		rejection := &ports.ClaudeOrchestratorRunRejectedError{Reason: ports.ClaudeOrchestratorRunFeatureDisabled}
		return rejectedRun(result, rejection), rejection
	}
	if err := gate.CheckRun(gateRequest); err != nil {
		var rejection *ports.ClaudeOrchestratorRunRejectedError
		if errors.As(err, &rejection) {
			return rejectedRun(result, rejection), err
		}
		result.MergeDecision = ports.MergeDecision{
			Decision: ports.MergeOutcomeHold,
			Reasons:  []string{err.Error()},
		}
		return GatedRunResult{Result: result}, err
	}

	runResult, err := s.Run(ctx, request)
	return GatedRunResult{Result: runResult}, err
}

func rejectedRun(result ports.OrchestrationResult, rejection *ports.ClaudeOrchestratorRunRejectedError) GatedRunResult {
	result.MergeDecision = ports.MergeDecision{
		Decision: ports.MergeOutcomeHold,
		Reasons:  []string{rejection.Error()},
	}
	return GatedRunResult{Result: result, Rejection: rejection}
}
