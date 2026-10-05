package claudeorchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type runGateFake struct {
	request ports.ClaudeOrchestratorRunRequest
	err     error
	calls   int
}

func (f *runGateFake) CheckRun(request ports.ClaudeOrchestratorRunRequest) error {
	f.calls++
	f.request = request
	return f.err
}

func TestRunGatedDefaultsToDenyWithoutGate(t *testing.T) {
	worker := &workerFake{executeFn: func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
		t.Fatal("worker executed for a run with no gate")
		return ports.WorkerExecution{}, nil
	}}
	service := gatedTestService(worker, nil)

	outcome, err := service.RunGated(context.Background(), nil, ports.ClaudeOrchestratorRunRequest{ExplicitOptIn: true}, ports.OrchestrationRequest{
		RunID: "default-deny",
		Task:  "do not run",
	})

	var rejection *ports.ClaudeOrchestratorRunRejectedError
	if !errors.As(err, &rejection) || rejection.Reason != ports.ClaudeOrchestratorRunFeatureDisabled {
		t.Fatalf("RunGated() error = %v, want typed feature-disabled rejection", err)
	}
	if outcome.Rejection == nil || outcome.Rejection.Reason != ports.ClaudeOrchestratorRunFeatureDisabled {
		t.Fatalf("RunGated() rejection = %+v, want typed feature-disabled result", outcome.Rejection)
	}
	assertGatedDenial(t, outcome.Result, "default-deny", "do not run")
	if len(worker.requests) != 0 {
		t.Fatalf("worker requests = %+v, want none", worker.requests)
	}
}

func TestRunGatedReturnsTypedRejectionWithoutStartingRun(t *testing.T) {
	gate := &runGateFake{err: &ports.ClaudeOrchestratorRunRejectedError{Reason: ports.ClaudeOrchestratorRunOptInRequired}}
	worker := &workerFake{executeFn: func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
		t.Fatal("worker executed after gate rejection")
		return ports.WorkerExecution{}, nil
	}}
	planCalls := 0
	service := gatedTestService(worker, &planCalls)
	gateRequest := ports.ClaudeOrchestratorRunRequest{}

	outcome, err := service.RunGated(context.Background(), gate, gateRequest, ports.OrchestrationRequest{
		RunID: "rejected",
		Task:  "do not run",
	})

	var rejection *ports.ClaudeOrchestratorRunRejectedError
	if !errors.As(err, &rejection) || rejection.Reason != ports.ClaudeOrchestratorRunOptInRequired {
		t.Fatalf("RunGated() error = %v, want typed opt-in rejection", err)
	}
	if outcome.Rejection == nil || outcome.Rejection.Reason != ports.ClaudeOrchestratorRunOptInRequired {
		t.Fatalf("RunGated() result rejection = %+v", outcome.Rejection)
	}
	assertGatedDenial(t, outcome.Result, "rejected", "do not run")
	if gate.calls != 1 || gate.request != gateRequest {
		t.Fatalf("gate calls/request = %d/%+v, want 1/%+v", gate.calls, gate.request, gateRequest)
	}
	if planCalls != 0 || len(worker.requests) != 0 {
		t.Fatalf("run started after rejection: plan calls=%d, worker requests=%+v", planCalls, worker.requests)
	}
}

func TestRunGatedFailsClosedOnGateError(t *testing.T) {
	gateErr := errors.New("gate unavailable")
	gate := &runGateFake{err: gateErr}
	worker := &workerFake{executeFn: func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
		t.Fatal("worker executed after gate error")
		return ports.WorkerExecution{}, nil
	}}
	planCalls := 0
	service := gatedTestService(worker, &planCalls)

	outcome, err := service.RunGated(context.Background(), gate, ports.ClaudeOrchestratorRunRequest{ExplicitOptIn: true}, ports.OrchestrationRequest{
		RunID: "gate-error",
		Task:  "do not run",
	})

	if !errors.Is(err, gateErr) {
		t.Fatalf("RunGated() error = %v, want gate error", err)
	}
	if outcome.Rejection != nil {
		t.Fatalf("RunGated() rejection = %+v, want nil for a non-rejection gate error", outcome.Rejection)
	}
	assertGatedDenial(t, outcome.Result, "gate-error", "do not run")
	if gate.calls != 1 || planCalls != 0 || len(worker.requests) != 0 {
		t.Fatalf("gate failure was not fail-closed: gate calls=%d, plan calls=%d, worker requests=%+v", gate.calls, planCalls, worker.requests)
	}
}

func TestRunGatedRunsOnlyAfterGateAllows(t *testing.T) {
	gate := &runGateFake{}
	worker := &workerFake{executeFn: func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	}}
	planCalls := 0
	service := gatedTestService(worker, &planCalls)

	outcome, err := service.RunGated(context.Background(), gate, ports.ClaudeOrchestratorRunRequest{ExplicitOptIn: true}, ports.OrchestrationRequest{
		RunID: "allowed",
		Task:  "do run",
	})
	if err != nil {
		t.Fatalf("RunGated() error = %v", err)
	}
	if outcome.Rejection != nil || outcome.Result.State != ports.RunStateCompleted {
		t.Fatalf("RunGated() = %+v, want completed result without rejection", outcome)
	}
	if gate.calls != 1 || planCalls != 1 || len(worker.requests) != 1 {
		t.Fatalf("gate/run calls = %d/%d/%d, want 1/1/1", gate.calls, planCalls, len(worker.requests))
	}
}

func assertGatedDenial(t *testing.T, result ports.OrchestrationResult, runID, task string) {
	t.Helper()
	if result.RunID != runID || result.Task != task || result.State != ports.RunStateFailed {
		t.Fatalf("denied orchestration result = %+v, want failed run %q", result, runID)
	}
	if result.MergeDecision.Decision != ports.MergeOutcomeHold || len(result.MergeDecision.Reasons) == 0 {
		t.Fatalf("denied merge decision = %+v, want hold with a reason", result.MergeDecision)
	}
}

func gatedTestService(worker *workerFake, planCalls *int) *Service {
	return New(Dependencies{
		Model: &modelFake{
			planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) {
				if planCalls != nil {
					*planCalls++
				}
				return validPlan(), nil
			},
			reviewFn: func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
				return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
			},
		},
		Worker: worker,
		Validator: &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
			return ports.ValidationReport{Passed: true}, nil
		}},
		Memory: &memoryFake{},
	})
}
