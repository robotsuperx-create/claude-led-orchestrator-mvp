package claudeorchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type modelFake struct {
	planFn   func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error)
	reviewFn func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error)
}

func (f *modelFake) Plan(ctx context.Context, request ports.PlanRequest) (ports.ExecutionPlan, error) {
	return f.planFn(ctx, request)
}

func (f *modelFake) Review(ctx context.Context, request ports.ReviewRequest) (ports.ReviewDecision, error) {
	return f.reviewFn(ctx, request)
}

type workerFake struct {
	executeFn func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error)
	requests  []ports.WorkerRequest
}

type workerRuntimeFunc func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error)

func (f workerRuntimeFunc) Execute(ctx context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
	return f(ctx, request)
}

func (f *workerFake) Execute(ctx context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
	f.requests = append(f.requests, request)
	return f.executeFn(ctx, request)
}

type validatorFake struct {
	validateFn func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error)
	requests   []ports.ValidationRequest
}

func (f *validatorFake) Validate(ctx context.Context, request ports.ValidationRequest) (ports.ValidationReport, error) {
	f.requests = append(f.requests, request)
	return f.validateFn(ctx, request)
}

type memoryFake struct {
	context   ports.MemoryContext
	readErr   error
	outcomes  []ports.MemoryOutcome
	recordErr error
}

func (f *memoryFake) ReadContext(_ context.Context, _ ports.MemoryContextRequest) (ports.MemoryContext, error) {
	return f.context, f.readErr
}

func (f *memoryFake) RecordOutcome(_ context.Context, outcome ports.MemoryOutcome) error {
	f.outcomes = append(f.outcomes, outcome)
	return f.recordErr
}

func TestRunCoordinatesAllPortsAndRecordsCompletedState(t *testing.T) {
	var service *Service
	memory := &memoryFake{context: ports.MemoryContext{Content: "project conventions"}}
	plan := ports.ExecutionPlan{
		Summary: "implement the request",
		Subtasks: []ports.PlannedSubtask{{
			ID: "task-1", Title: "Implement", Instructions: "Make the change", WorkerID: "worker-1", Provider: ports.ModelProviderClaude,
		}},
	}
	model := &modelFake{
		planFn: func(_ context.Context, request ports.PlanRequest) (ports.ExecutionPlan, error) {
			if state, ok := service.RunState("run-1"); !ok || state != ports.RunStatePlanning {
				t.Errorf("state during planning = %q, %v", state, ok)
			}
			if request.Provider != ports.ModelProviderClaude || request.Task != "Add the feature" || request.MemoryContext != "project conventions" {
				t.Errorf("plan request = %+v", request)
			}
			return plan, nil
		},
		reviewFn: func(_ context.Context, request ports.ReviewRequest) (ports.ReviewDecision, error) {
			if state, ok := service.RunState("run-1"); !ok || state != ports.RunStateReviewing {
				t.Errorf("state during review = %q, %v", state, ok)
			}
			if !request.Validation.Passed || len(request.Results) != 1 || request.Plan.Summary != plan.Summary {
				t.Errorf("review request did not include plan and collected gates: %+v", request)
			}
			return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove, Summary: "looks good"}, nil
		},
	}
	worker := &workerFake{executeFn: func(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
		if state, ok := service.RunState("run-1"); !ok || state != ports.RunStateExecuting {
			t.Errorf("state during execution = %q, %v", state, ok)
		}
		if request.Attempt != 1 || request.Task.ID != "task-1" {
			t.Errorf("worker request = %+v", request)
		}
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted, Summary: "implemented"}, nil
	}}
	validator := &validatorFake{validateFn: func(_ context.Context, request ports.ValidationRequest) (ports.ValidationReport, error) {
		if state, ok := service.RunState("run-1"); !ok || state != ports.RunStateValidating {
			t.Errorf("state during validation = %q, %v", state, ok)
		}
		if len(request.Results) != 1 || request.Results[0].FinalExecution.Status != ports.WorkerOutcomeCompleted {
			t.Errorf("validation request = %+v", request)
		}
		return ports.ValidationReport{Passed: true}, nil
	}}
	service = New(Dependencies{Model: model, Worker: worker, Validator: validator, Memory: memory})

	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "run-1", Task: "Add the feature"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.State != ports.RunStateCompleted || result.MergeDecision.Decision != ports.MergeOutcomeMerge {
		t.Fatalf("Run() result = %+v, want completed merge recommendation", result)
	}
	if state, ok := service.RunState("run-1"); !ok || state != ports.RunStateCompleted {
		t.Fatalf("final in-memory state = %q, %v", state, ok)
	}
	if len(memory.outcomes) != 1 || memory.outcomes[0].RunState != ports.RunStateCompleted || memory.outcomes[0].MergeDecision.Decision != ports.MergeOutcomeMerge {
		t.Fatalf("recorded outcomes = %+v", memory.outcomes)
	}
}

func TestRunRetriesFailedWorkerWithPreviousFailure(t *testing.T) {
	worker := &workerFake{}
	worker.executeFn = func(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
		if request.Attempt == 1 {
			return ports.WorkerExecution{Status: ports.WorkerOutcomeFailed, Error: "temporary failure"}, nil
		}
		if request.Attempt != 2 || request.PreviousFailure != "temporary failure" {
			t.Errorf("retry request = %+v", request)
		}
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	}
	service := New(Dependencies{
		Model: &modelFake{
			planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) { return validPlan(), nil },
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

	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "retry", Task: "work", MaxRetries: 1})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.State != ports.RunStateCompleted || len(worker.requests) != 2 || len(result.Results[0].Attempts) != 2 {
		t.Fatalf("result = %+v; requests = %+v", result, worker.requests)
	}
}

func TestRunHoldsUnlessWorkersValidationAndReviewAllPass(t *testing.T) {
	tests := []struct {
		name       string
		worker     ports.WorkerExecution
		validation ports.ValidationReport
		review     ports.ReviewDecision
	}{
		{
			name:       "validation rejected",
			worker:     ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted},
			validation: ports.ValidationReport{Passed: false, Issues: []string{"tests failed"}},
			review:     ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove},
		},
		{
			name:       "review requests changes",
			worker:     ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted},
			validation: ports.ValidationReport{Passed: true},
			review:     ports.ReviewDecision{Decision: ports.ReviewOutcomeRequestChanges, Summary: "fix edge case"},
		},
		{
			name:       "worker blocked",
			worker:     ports.WorkerExecution{Status: ports.WorkerOutcomeBlocked, Summary: "waiting for input"},
			validation: ports.ValidationReport{Passed: true},
			review:     ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := New(Dependencies{
				Model: &modelFake{
					planFn:   func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) { return validPlan(), nil },
					reviewFn: func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) { return test.review, nil },
				},
				Worker: &workerFake{executeFn: func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) { return test.worker, nil }},
				Validator: &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
					return test.validation, nil
				}},
				Memory: &memoryFake{},
			})
			result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "hold", Task: "work"})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if result.State != ports.RunStateHeld || result.MergeDecision.Decision != ports.MergeOutcomeHold || len(result.MergeDecision.Reasons) == 0 {
				t.Fatalf("Run() result = %+v, want held with reasons", result)
			}
		})
	}
}

func TestRunFailureIsRecordedAndReturned(t *testing.T) {
	planErr := errors.New("planner unavailable")
	memory := &memoryFake{readErr: nil}
	service := New(Dependencies{
		Model: &modelFake{planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) {
			return ports.ExecutionPlan{}, planErr
		}},
		Worker:    &workerFake{},
		Validator: &validatorFake{},
		Memory:    memory,
	})

	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "failed", Task: "work"})
	if !errors.Is(err, planErr) {
		t.Fatalf("Run() error = %v, want wrapped planner error", err)
	}
	if result.State != ports.RunStateFailed || result.MergeDecision.Decision != ports.MergeOutcomeHold {
		t.Fatalf("Run() result = %+v, want failed and held recommendation", result)
	}
	if len(memory.outcomes) != 1 || memory.outcomes[0].RunState != ports.RunStateFailed {
		t.Fatalf("failed outcome not recorded: %+v", memory.outcomes)
	}
	if state, ok := service.RunState("failed"); !ok || state != ports.RunStateFailed {
		t.Fatalf("recorded state = %q, %v", state, ok)
	}
}

func TestRunRejectsInvalidRequestAndPlan(t *testing.T) {
	t.Run("missing task", func(t *testing.T) {
		memory := &memoryFake{}
		service := New(Dependencies{Memory: memory})
		result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "bad"})
		if !errors.Is(err, ErrTaskRequired) || result.State != ports.RunStateFailed {
			t.Fatalf("Run() = (%+v, %v), want failed task-required", result, err)
		}
		if len(memory.outcomes) != 1 {
			t.Fatalf("failure was not recorded: %+v", memory.outcomes)
		}
	})

	t.Run("plan has no subtasks", func(t *testing.T) {
		memory := &memoryFake{}
		service := New(Dependencies{
			Model: &modelFake{
				planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) {
					return ports.ExecutionPlan{}, nil
				},
			},
			Worker: &workerFake{}, Validator: &validatorFake{}, Memory: memory,
		})
		result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "empty-plan", Task: "work"})
		if err == nil || !strings.Contains(err.Error(), "no subtasks") || result.State != ports.RunStateFailed {
			t.Fatalf("Run() = (%+v, %v), want failed invalid plan", result, err)
		}
		if len(memory.outcomes) != 1 || memory.outcomes[0].RunState != ports.RunStateFailed {
			t.Fatalf("invalid-plan failure was not recorded: %+v", memory.outcomes)
		}
	})
}

func validPlan() ports.ExecutionPlan {
	return ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{{ID: "task-1", WorkerID: "worker-1"}}}
}

func TestCancelBeforeStartIsFinalAndDoesNotDispatch(t *testing.T) {
	workerCalls := 0
	service := New(Dependencies{Worker: &workerFake{executeFn: func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
		workerCalls++
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	}}})
	first, second := service.Cancel("not-started"), service.Cancel("not-started")
	if !first || !second {
		t.Fatal("Cancel should accept and idempotently repeat cancellation before Run starts")
	}
	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "not-started", Task: "work"})
	if !errors.Is(err, context.Canceled) || result.State != ports.RunStateCanceled {
		t.Fatalf("Run() = (%+v, %v), want canceled", result, err)
	}
	if state, ok := service.RunState("not-started"); !ok || state != ports.RunStateCanceled {
		t.Fatalf("RunState = (%q, %v), want canceled", state, ok)
	}
	if workerCalls != 0 {
		t.Fatalf("worker calls = %d, want 0 for canceled-before-start run", workerCalls)
	}
}

func TestCancelIsIdempotentAndScopedToOneWorkerContext(t *testing.T) {
	type execution struct {
		id   string
		done <-chan struct{}
	}
	entered := make(chan execution, 2)
	service := New(Dependencies{
		Model: &modelFake{
			planFn: func(_ context.Context, request ports.PlanRequest) (ports.ExecutionPlan, error) {
				return ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{{ID: request.Task, WorkerID: "worker"}}}, nil
			},
			reviewFn: func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
				return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
			},
		},
		Worker: workerRuntimeFunc(func(ctx context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
			entered <- execution{id: request.Task.ID, done: ctx.Done()}
			<-ctx.Done()
			return ports.WorkerExecution{Status: ports.WorkerOutcomeFailed}, ctx.Err()
		}),
		Validator: &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
			return ports.ValidationReport{Passed: true}, nil
		}},
		Memory: &memoryFake{},
	})
	type runOutcome struct {
		id     string
		result ports.OrchestrationResult
		err    error
	}
	finished := make(chan runOutcome, 2)
	for _, id := range []string{"cancel-a", "cancel-b"} {
		go func(runID string) {
			result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: runID, Task: runID})
			finished <- runOutcome{id: runID, result: result, err: err}
		}(id)
	}
	contexts := make(map[string]<-chan struct{})
	for range 2 {
		select {
		case started := <-entered:
			contexts[started.id] = started.done
		case <-time.After(time.Second):
			t.Fatal("both workers did not start")
		}
	}
	if first, second := service.Cancel("cancel-a"), service.Cancel("cancel-a"); !first || !second {
		t.Fatal("duplicate Cancel on the same active run should be idempotent")
	}
	select {
	case <-contexts["cancel-a"]:
	case <-time.After(time.Second):
		t.Fatal("cancel did not stop the selected worker context")
	}
	select {
	case <-contexts["cancel-b"]:
		t.Fatal("canceling cancel-a unexpectedly canceled cancel-b")
	default:
	}
	if !service.Cancel("cancel-b") {
		t.Fatal("cleanup cancellation for cancel-b was rejected")
	}
	for range 2 {
		select {
		case outcome := <-finished:
			if outcome.err != nil && !errors.Is(outcome.err, context.Canceled) {
				t.Fatalf("Run(%s) error = %v", outcome.id, outcome.err)
			}
			if outcome.result.State != ports.RunStateCanceled {
				t.Fatalf("Run(%s) state = %q, want canceled", outcome.id, outcome.result.State)
			}
		case <-time.After(time.Second):
			t.Fatal("canceled runs did not finish")
		}
	}
}

func TestCancelRacesWithCompletionWithoutReopeningTerminalState(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	service := New(Dependencies{
		Model: &modelFake{
			planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) { return validPlan(), nil },
			reviewFn: func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
				return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
			},
		},
		Worker: &workerFake{executeFn: func(ctx context.Context, _ ports.WorkerRequest) (ports.WorkerExecution, error) {
			close(started)
			select {
			case <-release:
				return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
			case <-ctx.Done():
				return ports.WorkerExecution{Status: ports.WorkerOutcomeFailed}, ctx.Err()
			}
		}},
		Validator: &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
			return ports.ValidationReport{Passed: true}, nil
		}},
		Memory: &memoryFake{},
	})
	type outcome struct {
		result ports.OrchestrationResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "race", Task: "work"})
		finished <- outcome{result: result, err: err}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	ready := make(chan struct{}, 2)
	gate := make(chan struct{})
	go func() { ready <- struct{}{}; <-gate; close(release) }()
	go func() { ready <- struct{}{}; <-gate; service.Cancel("race") }()
	<-ready
	<-ready
	close(gate)
	var got outcome
	select {
	case got = <-finished:
	case <-time.After(time.Second):
		t.Fatal("racing run did not finish")
	}
	state, ok := service.RunState("race")
	if !ok || (state != ports.RunStateCanceled && state != ports.RunStateCompleted) {
		t.Fatalf("final state = %q, %v; want canceled or completed", state, ok)
	}
	if got.result.State != state {
		t.Fatalf("returned state %q disagrees with final state %q", got.result.State, state)
	}
	if state == ports.RunStateCanceled && !errors.Is(got.err, context.Canceled) {
		t.Fatalf("canceled result error = %v, want context.Canceled", got.err)
	}
	if state == ports.RunStateCompleted && got.err != nil {
		t.Fatalf("completed result error = %v, want nil", got.err)
	}
	if state == ports.RunStateCanceled && !service.Cancel("race") {
		t.Fatal("duplicate cancellation after cancellation won must remain idempotent")
	}
	if state == ports.RunStateCompleted && service.Cancel("race") {
		t.Fatal("cancellation must not replace an already completed run")
	}
}
