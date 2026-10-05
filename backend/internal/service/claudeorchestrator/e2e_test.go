package claudeorchestrator

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRunGatedEndToEndOptInRetryAndMerge(t *testing.T) {
	ctx := context.Background()
	events := make([]string, 0, 6)
	plan := ports.ExecutionPlan{
		Summary: "implement and verify the requested change",
		Subtasks: []ports.PlannedSubtask{{
			ID:           "implement",
			Title:        "Implement the change",
			Instructions: "Make the requested change and report the result",
			WorkerID:     "deepseek-worker",
			Provider:     ports.ModelProviderDeepSeek,
		}},
	}
	model := &e2eModelGateway{events: &events, plan: plan}
	worker := &e2eWorkerRuntime{events: &events}
	validator := &e2eValidator{events: &events}
	memory := &e2eProjectMemory{events: &events, context: ports.MemoryContext{Content: "project conventions"}}
	service := New(Dependencies{Model: model, Worker: worker, Validator: validator, Memory: memory})
	gate := ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}

	denied, err := service.RunGated(ctx, gate, ports.ClaudeOrchestratorRunRequest{}, ports.OrchestrationRequest{
		RunID: "e2e-denied",
		Task:  "Implement the change",
	})
	var rejection *ports.ClaudeOrchestratorRunRejectedError
	if !errors.As(err, &rejection) || rejection.Reason != ports.ClaudeOrchestratorRunOptInRequired {
		t.Fatalf("RunGated() without explicit opt-in error = %v, want typed opt-in rejection", err)
	}
	if denied.Rejection == nil || denied.Rejection.Reason != ports.ClaudeOrchestratorRunOptInRequired {
		t.Fatalf("RunGated() without explicit opt-in rejection = %+v", denied.Rejection)
	}
	if denied.Result.State != ports.RunStateFailed || denied.Result.MergeDecision.Decision != ports.MergeOutcomeHold {
		t.Fatalf("denied result = %+v, want failed and held", denied.Result)
	}
	if _, ok := service.RunState("e2e-denied"); ok {
		t.Fatal("denied run unexpectedly changed the service's recorded run state")
	}
	if len(events) != 0 || len(model.planRequests) != 0 || len(model.reviewRequests) != 0 || len(worker.requests) != 0 || len(validator.requests) != 0 || len(memory.readRequests) != 0 || len(memory.outcomes) != 0 {
		t.Fatalf("components were called before opt-in: events=%v model=%+v worker=%+v validator=%+v memory=%+v", events, model, worker.requests, validator.requests, memory)
	}

	result, err := service.RunGated(ctx, gate, ports.ClaudeOrchestratorRunRequest{ExplicitOptIn: true}, ports.OrchestrationRequest{
		RunID:      "e2e-allowed",
		Task:       "Implement the change",
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("RunGated() with explicit opt-in error = %v", err)
	}
	if result.Rejection != nil {
		t.Fatalf("allowed run unexpectedly rejected: %+v", result.Rejection)
	}

	wantEvents := []string{"Plan", "Delegate", "Retry", "Validate", "Review", "Merge"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("orchestration sequence = %v, want %v", events, wantEvents)
	}
	if len(model.planRequests) != 1 || model.planRequests[0].Provider != ports.ModelProviderClaude || model.planRequests[0].Task != "Implement the change" || model.planRequests[0].MemoryContext != "project conventions" {
		t.Fatalf("Claude planning requests = %+v", model.planRequests)
	}
	if len(worker.requests) != 2 {
		t.Fatalf("worker requests = %+v, want first attempt plus retry", worker.requests)
	}
	if worker.requests[0].Task.Provider != ports.ModelProviderDeepSeek || worker.requests[0].Attempt != 1 {
		t.Fatalf("initial delegated request = %+v, want DeepSeek attempt 1", worker.requests[0])
	}
	if worker.requests[1].Task.Provider != ports.ModelProviderDeepSeek || worker.requests[1].Attempt != 2 || worker.requests[1].PreviousFailure != "temporary worker failure" {
		t.Fatalf("retry delegated request = %+v, want DeepSeek attempt 2 with prior failure", worker.requests[1])
	}
	if len(result.Result.Results) != 1 || len(result.Result.Results[0].Attempts) != 2 || result.Result.Results[0].Attempts[0].Execution.Status != ports.WorkerOutcomeFailed || result.Result.Results[0].FinalExecution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("collected retry results = %+v", result.Result.Results)
	}
	if len(validator.requests) != 1 {
		t.Fatalf("validator requests = %+v, want the delegated DeepSeek plan", validator.requests)
	}
	if validator.requests[0].Task != "Implement the change" || len(validator.requests[0].Results) != 1 || len(validator.requests[0].Results[0].Attempts) != 2 || validator.requests[0].Results[0].FinalExecution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("validation input = %+v, want the successful retried work", validator.requests[0])
	}
	if validator.requests[0].Plan.Subtasks[0].Provider != ports.ModelProviderDeepSeek {
		t.Fatalf("validation plan provider = %q, want DeepSeek", validator.requests[0].Plan.Subtasks[0].Provider)
	}
	if len(model.reviewRequests) != 1 || model.reviewRequests[0].Provider != ports.ModelProviderClaude || !model.reviewRequests[0].Validation.Passed {
		t.Fatalf("Claude review requests = %+v", model.reviewRequests)
	}
	if result.Result.State != ports.RunStateCompleted || result.Result.MergeDecision.Decision != ports.MergeOutcomeMerge || len(result.Result.MergeDecision.Reasons) != 0 {
		t.Fatalf("RunGated() result = %+v, want completed with merge recommendation", result.Result)
	}
	if state, ok := service.RunState("e2e-allowed"); !ok || state != ports.RunStateCompleted {
		t.Fatalf("final service state = %q, %v, want completed", state, ok)
	}
	if len(memory.outcomes) != 1 || memory.outcomes[0].RunID != "e2e-allowed" || memory.outcomes[0].RunState != ports.RunStateCompleted || memory.outcomes[0].MergeDecision.Decision != ports.MergeOutcomeMerge {
		t.Fatalf("recorded memory outcomes = %+v, want completed merge outcome", memory.outcomes)
	}
	if len(memory.readRequests) != 1 || memory.readRequests[0].Task != "Implement the change" {
		t.Fatalf("memory context requests = %+v, want one request for the task", memory.readRequests)
	}
}

type e2eModelGateway struct {
	events         *[]string
	plan           ports.ExecutionPlan
	planRequests   []ports.PlanRequest
	reviewRequests []ports.ReviewRequest
}

func (f *e2eModelGateway) Plan(_ context.Context, request ports.PlanRequest) (ports.ExecutionPlan, error) {
	*f.events = append(*f.events, "Plan")
	f.planRequests = append(f.planRequests, request)
	return f.plan, nil
}

func (f *e2eModelGateway) Review(_ context.Context, request ports.ReviewRequest) (ports.ReviewDecision, error) {
	*f.events = append(*f.events, "Review")
	f.reviewRequests = append(f.reviewRequests, request)
	return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove, Summary: "verified"}, nil
}

type e2eWorkerRuntime struct {
	events   *[]string
	requests []ports.WorkerRequest
}

func (f *e2eWorkerRuntime) Execute(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
	f.requests = append(f.requests, request)
	if request.Attempt == 1 {
		*f.events = append(*f.events, "Delegate")
		return ports.WorkerExecution{Status: ports.WorkerOutcomeFailed, Error: "temporary worker failure"}, nil
	}
	*f.events = append(*f.events, "Retry")
	return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted, Summary: "change implemented", Output: "ok"}, nil
}

type e2eValidator struct {
	events   *[]string
	requests []ports.ValidationRequest
}

func (f *e2eValidator) Validate(_ context.Context, request ports.ValidationRequest) (ports.ValidationReport, error) {
	*f.events = append(*f.events, "Validate")
	f.requests = append(f.requests, request)
	return ports.ValidationReport{Passed: true}, nil
}

type e2eProjectMemory struct {
	events       *[]string
	context      ports.MemoryContext
	readRequests []ports.MemoryContextRequest
	outcomes     []ports.MemoryOutcome
}

func (f *e2eProjectMemory) ReadContext(_ context.Context, request ports.MemoryContextRequest) (ports.MemoryContext, error) {
	f.readRequests = append(f.readRequests, request)
	return f.context, nil
}

func (f *e2eProjectMemory) RecordOutcome(_ context.Context, outcome ports.MemoryOutcome) error {
	*f.events = append(*f.events, "Merge")
	f.outcomes = append(f.outcomes, outcome)
	return nil
}
