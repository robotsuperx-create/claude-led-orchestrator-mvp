package ports_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type orchestrationContractDouble struct{}

var (
	_ ports.ExecutiveOrchestrator = orchestrationContractDouble{}
	_ ports.ModelGateway          = orchestrationContractDouble{}
	_ ports.WorkerRuntime         = orchestrationContractDouble{}
	_ ports.Validator             = orchestrationContractDouble{}
	_ ports.ProjectMemory         = orchestrationContractDouble{}
)

func (orchestrationContractDouble) Run(_ context.Context, request ports.OrchestrationRequest) (ports.OrchestrationResult, error) {
	return ports.OrchestrationResult{
		RunID: request.RunID,
		State: ports.RunStateCompleted,
		Task:  request.Task,
		Plan: ports.ExecutionPlan{
			Summary: "planned",
			Subtasks: []ports.PlannedSubtask{{
				ID:       "task-1",
				Title:    "Implement change",
				WorkerID: "worker-1",
				Provider: ports.ModelProviderClaude,
			}},
		},
		Validation: ports.ValidationReport{Passed: true},
		Review: ports.ReviewDecision{
			Decision: ports.ReviewOutcomeApprove,
			Summary:  "approved",
		},
		MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeMerge},
	}, nil
}

func (orchestrationContractDouble) Plan(_ context.Context, request ports.PlanRequest) (ports.ExecutionPlan, error) {
	return ports.ExecutionPlan{Summary: request.Task}, nil
}

func (orchestrationContractDouble) Review(_ context.Context, _ ports.ReviewRequest) (ports.ReviewDecision, error) {
	return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
}

func (orchestrationContractDouble) Execute(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
	return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted, Summary: request.Task.ID}, nil
}

func (orchestrationContractDouble) Validate(_ context.Context, _ ports.ValidationRequest) (ports.ValidationReport, error) {
	return ports.ValidationReport{Passed: true}, nil
}

func (orchestrationContractDouble) ReadContext(_ context.Context, request ports.MemoryContextRequest) (ports.MemoryContext, error) {
	return ports.MemoryContext{Content: request.Task}, nil
}

func (orchestrationContractDouble) RecordOutcome(_ context.Context, _ ports.MemoryOutcome) error {
	return nil
}

func TestClaudeOrchestrationPortsUseTypedRequestsAndResults(t *testing.T) {
	ctx := context.Background()
	contract := orchestrationContractDouble{}

	result, err := contract.Run(ctx, ports.OrchestrationRequest{
		RunID:      "run-1",
		Task:       "Add a typed contract",
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.RunID != "run-1" || result.Task != "Add a typed contract" || result.State != ports.RunStateCompleted {
		t.Fatalf("Run() returned unexpected result: %+v", result)
	}
	if result.Plan.Subtasks[0].Provider != ports.ModelProviderClaude {
		t.Fatalf("planned provider = %q, want %q", result.Plan.Subtasks[0].Provider, ports.ModelProviderClaude)
	}

	plan, err := contract.Plan(ctx, ports.PlanRequest{Provider: ports.ModelProviderClaude, Task: "plan"})
	if err != nil || plan.Summary != "plan" {
		t.Fatalf("Plan() = (%+v, %v), want summary plan and no error", plan, err)
	}
	review, err := contract.Review(ctx, ports.ReviewRequest{Provider: ports.ModelProviderClaude, Plan: plan})
	if err != nil || review.Decision != ports.ReviewOutcomeApprove {
		t.Fatalf("Review() = (%+v, %v), want approve and no error", review, err)
	}

	execution, err := contract.Execute(ctx, ports.WorkerRequest{Task: ports.PlannedSubtask{ID: "task-1"}, Attempt: 1})
	if err != nil || execution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("Execute() = (%+v, %v), want completed and no error", execution, err)
	}
	validation, err := contract.Validate(ctx, ports.ValidationRequest{Task: "validate", Plan: plan})
	if err != nil || !validation.Passed {
		t.Fatalf("Validate() = (%+v, %v), want passed and no error", validation, err)
	}

	memory, err := contract.ReadContext(ctx, ports.MemoryContextRequest{Task: "memory"})
	if err != nil || memory.Content != "memory" {
		t.Fatalf("ReadContext() = (%+v, %v), want task context and no error", memory, err)
	}
	if err := contract.RecordOutcome(ctx, ports.MemoryOutcome{
		RunID:         result.RunID,
		Task:          result.Task,
		RunState:      result.State,
		MergeDecision: result.MergeDecision,
	}); err != nil {
		t.Fatalf("RecordOutcome() error = %v", err)
	}
}

func TestRunStateValues(t *testing.T) {
	states := []ports.RunState{
		ports.RunStatePending,
		ports.RunStatePlanning,
		ports.RunStateExecuting,
		ports.RunStateValidating,
		ports.RunStateReviewing,
		ports.RunStateCompleted,
		ports.RunStateHeld,
		ports.RunStateFailed,
	}
	seen := make(map[ports.RunState]bool, len(states))
	for _, state := range states {
		if state == "" {
			t.Fatal("run state must not be empty")
		}
		if seen[state] {
			t.Fatalf("duplicate run state %q", state)
		}
		seen[state] = true
	}
}
