package claudeorchestrator

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func parallelService(plan ports.ExecutionPlan, maxParallel int, execute func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error)) *Service {
	model := &modelFake{
		planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) { return plan, nil },
		reviewFn: func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
			return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
		},
	}
	validator := &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
		return ports.ValidationReport{Passed: true}, nil
	}}
	return New(Dependencies{Model: model, Worker: workerRuntimeFunc(execute), Validator: validator, Memory: &memoryFake{}, MaxParallelSubtasks: maxParallel})
}

func TestIndependentSubtasksRunInParallelAndDependentsWait(t *testing.T) {
	plan := ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{
		{ID: "schema", WorkerID: "w"},
		{ID: "form", WorkerID: "w", DependsOn: []string{"schema"}},
		{ID: "api", WorkerID: "w", DependsOn: []string{"schema"}},
		{ID: "tests", WorkerID: "w", DependsOn: []string{"form", "api"}},
	}}
	var mu sync.Mutex
	var order []string
	var running, peak atomic.Int32
	finished := map[string]bool{}
	service := parallelService(plan, 3, func(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
		now := running.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		mu.Lock()
		for _, dependency := range request.Task.DependsOn {
			if !finished[dependency] {
				t.Errorf("%s started before its dependency %s finished", request.Task.ID, dependency)
			}
		}
		order = append(order, request.Task.ID)
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		finished[request.Task.ID] = true
		mu.Unlock()
		running.Add(-1)
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	})
	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "r", Task: "t"})
	if err != nil || result.State != ports.RunStateCompleted {
		t.Fatalf("Run = %+v, %v", result, err)
	}
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency = %d, want form and api together", peak.Load())
	}
	if order[0] != "schema" || order[3] != "tests" {
		t.Fatalf("order = %v", order)
	}
	ids := make([]string, len(result.Results))
	for i, r := range result.Results {
		ids[i] = r.Task.ID
	}
	if strings.Join(ids, ",") != "schema,form,api,tests" {
		t.Fatalf("results order = %v, want plan order", ids)
	}
}

func TestSubtasksAfterAFailedDependencyAreNotRun(t *testing.T) {
	plan := ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{
		{ID: "a", WorkerID: "w"},
		{ID: "b", WorkerID: "w", DependsOn: []string{"a"}},
		{ID: "c", WorkerID: "w"},
	}}
	var ran sync.Map
	service := parallelService(plan, 2, func(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
		ran.Store(request.Task.ID, true)
		if request.Task.ID == "a" {
			return ports.WorkerExecution{Status: ports.WorkerOutcomeFailed, Error: "broken"}, nil
		}
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	})
	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "r", Task: "t"})
	if err != nil || result.State != ports.RunStateHeld {
		t.Fatalf("Run = %+v, %v; want held", result, err)
	}
	if _, ok := ran.Load("b"); ok {
		t.Fatal("b ran although a failed")
	}
	if _, ok := ran.Load("c"); !ok {
		t.Fatal("independent c did not run")
	}
	b := result.Results[1]
	if b.Task.ID != "b" || b.FinalExecution.Status != ports.WorkerOutcomeBlocked || !strings.Contains(b.FinalExecution.Error, `"a"`) {
		t.Fatalf("b result = %+v", b)
	}
}

func TestPlansWithBadDependenciesAreRejected(t *testing.T) {
	for name, subtasks := range map[string][]ports.PlannedSubtask{
		"unknown": {{ID: "a", WorkerID: "w", DependsOn: []string{"ghost"}}},
		"self":    {{ID: "a", WorkerID: "w", DependsOn: []string{"a"}}},
		"cycle":   {{ID: "a", WorkerID: "w", DependsOn: []string{"b"}}, {ID: "b", WorkerID: "w", DependsOn: []string{"a"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validatePlan(ports.ExecutionPlan{Subtasks: subtasks}); err == nil {
				t.Fatal("plan was accepted")
			}
		})
	}
}
