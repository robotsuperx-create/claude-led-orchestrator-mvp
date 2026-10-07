package claudeorchestrator

import (
	"context"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// runWithPlan executes one orchestration run whose executive plan is plan and
// returns every request the worker and validator received.
func runWithPlan(t *testing.T, deps Dependencies, request ports.OrchestrationRequest, plan ports.ExecutionPlan) ([]ports.WorkerRequest, []ports.ValidationRequest, ports.OrchestrationResult) {
	t.Helper()
	model := &modelFake{
		planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) { return plan, nil },
		reviewFn: func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
			return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
		},
	}
	worker := &workerFake{executeFn: func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	}}
	validator := &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
		return ports.ValidationReport{Passed: true}, nil
	}}
	deps.Model, deps.Worker, deps.Validator, deps.Memory = model, worker, validator, &memoryFake{}
	result, err := New(deps).Run(context.Background(), request)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return worker.requests, validator.requests, result
}

// The executive model must never choose where commands run or which directory
// is mounted. Any metadata in its plan is replaced with the server's workspace.
func TestRunReplacesModelSuppliedWorktreeWithTrustedDefault(t *testing.T) {
	const trusted = "/repo/.ao/worktrees/task"
	plan := ports.ExecutionPlan{Summary: "plan", Subtasks: []ports.PlannedSubtask{{
		ID: "a", WorkerID: "w", Instructions: "do it",
		Metadata: map[string]string{
			ports.SubtaskMetadataKeyWorktreePath: "/home/user/.ssh",
			"extra":                              "injected",
		},
	}}}
	workerRequests, validationRequests, result := runWithPlan(t,
		Dependencies{DefaultWorktreePath: trusted},
		ports.OrchestrationRequest{RunID: "run-1", Task: "task"}, plan)

	if len(workerRequests) != 1 {
		t.Fatalf("worker requests = %d, want 1", len(workerRequests))
	}
	got := workerRequests[0].Task.Metadata
	if len(got) != 1 || got[ports.SubtaskMetadataKeyWorktreePath] != trusted {
		t.Fatalf("worker metadata = %v, want only the trusted worktree %q", got, trusted)
	}
	if len(validationRequests) != 1 || validationRequests[0].Results[0].Task.Metadata[ports.SubtaskMetadataKeyWorktreePath] != trusted {
		t.Fatalf("validator did not receive the trusted worktree: %+v", validationRequests)
	}
	if result.Plan.Subtasks[0].Metadata[ports.SubtaskMetadataKeyWorktreePath] != trusted {
		t.Fatalf("recorded plan metadata = %v, want trusted worktree", result.Plan.Subtasks[0].Metadata)
	}
	// The model's original map must not be mutated in place.
	if plan.Subtasks[0].Metadata[ports.SubtaskMetadataKeyWorktreePath] != "/home/user/.ssh" {
		t.Fatal("bindTrustedWorkspace mutated the model's plan in place")
	}
}

func TestRunPrefersCallerSelectedWorktreeOverDefault(t *testing.T) {
	const selected = "/repo/.ao/worktrees/selected"
	plan := ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{{ID: "a", WorkerID: "w"}, {ID: "b", WorkerID: "w"}}}
	workerRequests, _, _ := runWithPlan(t,
		Dependencies{DefaultWorktreePath: "/repo"},
		ports.OrchestrationRequest{RunID: "run-2", Task: "task", WorktreePath: selected}, plan)

	if len(workerRequests) != 2 {
		t.Fatalf("worker requests = %d, want 2", len(workerRequests))
	}
	for _, request := range workerRequests {
		if path := request.Task.Metadata[ports.SubtaskMetadataKeyWorktreePath]; path != selected {
			t.Fatalf("subtask %q worktree = %q, want %q", request.Task.ID, path, selected)
		}
	}
}

func TestRunWithoutTrustedWorkspaceStripsModelMetadata(t *testing.T) {
	plan := ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{{
		ID: "a", WorkerID: "w",
		Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: "/etc"},
	}}}
	workerRequests, _, _ := runWithPlan(t, Dependencies{},
		ports.OrchestrationRequest{RunID: "run-3", Task: "task"}, plan)

	if len(workerRequests) != 1 || workerRequests[0].Task.Metadata != nil {
		t.Fatalf("worker metadata = %v, want none when no trusted workspace exists", workerRequests[0].Task.Metadata)
	}
}

func TestServiceForgetsOldestFinishedRunStates(t *testing.T) {
	plan := ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{{ID: "a", WorkerID: "w"}}}
	model := &modelFake{
		planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) { return plan, nil },
		reviewFn: func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
			return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
		},
	}
	service := New(Dependencies{
		Model: model, Memory: &memoryFake{},
		Worker: &workerFake{executeFn: func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
			return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
		}},
		Validator: &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
			return ports.ValidationReport{Passed: true}, nil
		}},
	})
	total := maxRetainedRunStates + 5
	for i := 0; i < total; i++ {
		if _, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: fmt.Sprintf("r-%d", i), Task: "t"}); err != nil {
			t.Fatalf("Run %d: %v", i, err)
		}
	}
	if _, ok := service.RunState("r-0"); ok {
		t.Fatal("the oldest finished run is still retained")
	}
	if state, ok := service.RunState(fmt.Sprintf("r-%d", total-1)); !ok || state != ports.RunStateCompleted {
		t.Fatalf("newest run state = %q, %v; want completed", state, ok)
	}
	if len(service.states) > maxRetainedRunStates {
		t.Fatalf("retained %d states, want at most %d", len(service.states), maxRetainedRunStates)
	}
}
