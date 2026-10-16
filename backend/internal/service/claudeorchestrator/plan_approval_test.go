package claudeorchestrator

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func approvalService(t *testing.T, workerCalls *atomic.Int32) *Service {
	t.Helper()
	model := &modelFake{
		planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) {
			return ports.ExecutionPlan{Summary: "two steps", Subtasks: []ports.PlannedSubtask{
				{ID: "a", WorkerID: "w", Title: "First"},
				{ID: "b", WorkerID: "w", Title: "Second", DependsOn: []string{"a"}},
			}}, nil
		},
		reviewFn: func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
			return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
		},
	}
	worker := workerRuntimeFunc(func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
		workerCalls.Add(1)
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	})
	validator := &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
		return ports.ValidationReport{Passed: true}, nil
	}}
	return New(Dependencies{Model: model, Worker: worker, Validator: validator, Memory: &memoryFake{}})
}

func waitForState(t *testing.T, service *Service, runID string, want ports.RunState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if state, _ := service.RunState(runID); state == want {
			return
		}
		if time.Now().After(deadline) {
			state, _ := service.RunState(runID)
			t.Fatalf("state = %q, want %q", state, want)
		}
		time.Sleep(time.Millisecond)
	}
}

type runOutcome struct {
	result ports.OrchestrationResult
	err    error
}

func startApprovalRun(service *Service) <-chan runOutcome {
	done := make(chan runOutcome, 1)
	go func() {
		result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "run-a", Task: "Do it", RequirePlanApproval: true})
		done <- runOutcome{result, err}
	}()
	return done
}

func TestPlanApprovalPausesUntilApprovedThenRuns(t *testing.T) {
	var workerCalls atomic.Int32
	service := approvalService(t, &workerCalls)
	done := startApprovalRun(service)
	waitForState(t, service, "run-a", ports.RunStateAwaitingApproval)

	plan, ok := service.PlanFor("run-a")
	if !ok || len(plan.Subtasks) != 2 || plan.Subtasks[1].DependsOn[0] != "a" {
		t.Fatalf("PlanFor = %+v, %v", plan, ok)
	}
	time.Sleep(20 * time.Millisecond)
	if workerCalls.Load() != 0 {
		t.Fatal("a worker ran before the plan was approved")
	}
	if !service.Approve("run-a") {
		t.Fatal("Approve returned false while awaiting approval")
	}
	if service.Approve("run-a") {
		t.Fatal("a second approval was accepted")
	}
	outcome := <-done
	if outcome.err != nil || outcome.result.State != ports.RunStateCompleted || workerCalls.Load() != 2 {
		t.Fatalf("outcome = %+v, worker calls %d", outcome, workerCalls.Load())
	}
}

func TestPlanRejectionCancelsWithoutRunningAnything(t *testing.T) {
	var workerCalls atomic.Int32
	service := approvalService(t, &workerCalls)
	done := startApprovalRun(service)
	waitForState(t, service, "run-a", ports.RunStateAwaitingApproval)
	if !service.Reject("run-a") {
		t.Fatal("Reject returned false")
	}
	outcome := <-done
	if !errors.Is(outcome.err, context.Canceled) || outcome.result.State != ports.RunStateCanceled || workerCalls.Load() != 0 {
		t.Fatalf("outcome = %+v, worker calls %d", outcome, workerCalls.Load())
	}
	if state, _ := service.RunState("run-a"); state != ports.RunStateCanceled {
		t.Fatalf("state = %q", state)
	}
}

func TestCancelWhileAwaitingApprovalStopsTheRun(t *testing.T) {
	var workerCalls atomic.Int32
	service := approvalService(t, &workerCalls)
	done := startApprovalRun(service)
	waitForState(t, service, "run-a", ports.RunStateAwaitingApproval)
	if !service.Cancel("run-a") {
		t.Fatal("Cancel returned false")
	}
	outcome := <-done
	if outcome.result.State != ports.RunStateCanceled || workerCalls.Load() != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if service.Approve("run-a") {
		t.Fatal("a canceled run accepted approval")
	}
}

func TestRunsWithoutApprovalNeverPause(t *testing.T) {
	var workerCalls atomic.Int32
	service := approvalService(t, &workerCalls)
	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "run-b", Task: "Do it"})
	if err != nil || result.State != ports.RunStateCompleted {
		t.Fatalf("Run = %+v, %v", result, err)
	}
	if service.Approve("run-b") {
		t.Fatal("a finished run accepted approval")
	}
}
