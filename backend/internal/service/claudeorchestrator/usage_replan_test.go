package claudeorchestrator

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestUsageMeterCountsTokensAndEstimatesCost(t *testing.T) {
	meter := newUsageMeter(UsageBudget{Prices: map[ports.ModelProvider]ModelPrice{
		ports.ModelProviderClaude: {InputUSDPerMTok: 3, OutputUSDPerMTok: 15},
	}})
	meter.Record(ports.ModelProviderClaude, 1000, 200)
	meter.Record(ports.ModelProviderClaude, 500, 100)
	usage := meter.snapshot()
	if usage.InputTokens != 1500 || usage.OutputTokens != 300 || usage.Calls != 2 {
		t.Fatalf("usage = %+v", usage)
	}
	// 1500*3 + 300*15 = 9000 micro-dollars.
	if !usage.Priced || usage.CostMicroUSD != 9000 {
		t.Fatalf("cost = %d priced=%v, want 9000 priced", usage.CostMicroUSD, usage.Priced)
	}
	meter.Record(ports.ModelProviderDeepSeek, 10, 10)
	if meter.snapshot().Priced {
		t.Fatal("a call without a configured price must make the estimate unpriced")
	}
}

func TestUsageMeterRefusesCallsOnceTheBudgetIsSpent(t *testing.T) {
	meter := newUsageMeter(UsageBudget{MaxTokens: 100})
	if err := meter.Reserve(ports.ModelProviderClaude); err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	meter.Record(ports.ModelProviderClaude, 80, 30)
	if err := meter.Reserve(ports.ModelProviderClaude); !errors.Is(err, ports.ErrRunBudgetExceeded) {
		t.Fatalf("reserve after budget = %v, want ErrRunBudgetExceeded", err)
	}
	if usage := meter.snapshot(); !usage.Exhausted || usage.BudgetTokens != 100 {
		t.Fatalf("usage = %+v, want exhausted with budget 100", usage)
	}

	costly := newUsageMeter(UsageBudget{MaxCostUSD: 0.01, Prices: map[ports.ModelProvider]ModelPrice{
		ports.ModelProviderClaude: {InputUSDPerMTok: 10, OutputUSDPerMTok: 10},
	}})
	costly.Record(ports.ModelProviderClaude, 1000, 0) // 10_000 micro-dollars = $0.01
	if err := costly.Reserve(ports.ModelProviderClaude); !errors.Is(err, ports.ErrRunBudgetExceeded) {
		t.Fatalf("reserve after cost budget = %v", err)
	}
}

// meteredModel reports usage through the run's meter, as the gateway does.
type meteredModel struct {
	plans   func(ports.PlanRequest) ports.ExecutionPlan
	mu      sync.Mutex
	planned []ports.PlanRequest
}

func (m *meteredModel) Plan(ctx context.Context, request ports.PlanRequest) (ports.ExecutionPlan, error) {
	meter := ports.UsageMeterFrom(ctx)
	if meter == nil {
		return ports.ExecutionPlan{}, errors.New("no usage meter on the run context")
	}
	if err := meter.Reserve(request.Provider); err != nil {
		return ports.ExecutionPlan{}, err
	}
	meter.Record(request.Provider, 100, 50)
	m.mu.Lock()
	m.planned = append(m.planned, request)
	m.mu.Unlock()
	return m.plans(request), nil
}

func (m *meteredModel) Review(ctx context.Context, request ports.ReviewRequest) (ports.ReviewDecision, error) {
	meter := ports.UsageMeterFrom(ctx)
	if err := meter.Reserve(request.Provider); err != nil {
		return ports.ReviewDecision{}, err
	}
	meter.Record(request.Provider, 40, 10)
	return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
}

func passingValidator() *validatorFake {
	return &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
		return ports.ValidationReport{Passed: true}, nil
	}}
}

func TestRunReportsUsageAndStopsWhenTheBudgetIsSpent(t *testing.T) {
	plan := ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{{ID: "a", WorkerID: "w"}}}
	model := &meteredModel{plans: func(ports.PlanRequest) ports.ExecutionPlan { return plan }}
	complete := workerRuntimeFunc(func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	})

	service := New(Dependencies{Model: model, Worker: complete, Validator: passingValidator(), Memory: &memoryFake{}})
	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "ok", Task: "t"})
	if err != nil || result.State != ports.RunStateCompleted {
		t.Fatalf("Run = %+v, %v", result.State, err)
	}
	if result.Usage.InputTokens != 140 || result.Usage.OutputTokens != 60 || result.Usage.Calls != 2 {
		t.Fatalf("usage = %+v, want plan + review", result.Usage)
	}
	if live, ok := service.UsageFor("ok"); !ok || live.Calls != 2 {
		t.Fatalf("UsageFor = %+v, %v", live, ok)
	}

	// A budget smaller than the plan call leaves nothing for the review.
	tight := New(Dependencies{Model: model, Worker: complete, Validator: passingValidator(), Memory: &memoryFake{}, Budget: UsageBudget{MaxTokens: 100}})
	result, err = tight.Run(context.Background(), ports.OrchestrationRequest{RunID: "tight", Task: "t"})
	if !errors.Is(err, ports.ErrRunBudgetExceeded) || result.State != ports.RunStateFailed {
		t.Fatalf("Run with tight budget = %s, %v; want failed with ErrRunBudgetExceeded", result.State, err)
	}
	if !result.Usage.Exhausted || result.Usage.Calls != 1 {
		t.Fatalf("usage = %+v, want exhausted after one call", result.Usage)
	}
}

func TestFailedSubtasksAreReplannedOnceAndTheRunCompletes(t *testing.T) {
	first := ports.ExecutionPlan{Summary: "first", Subtasks: []ports.PlannedSubtask{
		{ID: "a", WorkerID: "w", Title: "Parser"},
		{ID: "b", WorkerID: "w", Title: "Broken approach"},
	}}
	revised := ports.ExecutionPlan{Summary: "revised", Subtasks: []ports.PlannedSubtask{
		{ID: "b", WorkerID: "w", Title: "Better approach"},
		{ID: "c", WorkerID: "w", Title: "Tests", DependsOn: []string{"b"}},
	}}
	model := &meteredModel{plans: func(request ports.PlanRequest) ports.ExecutionPlan {
		if request.PreviousAttempt != "" {
			return revised
		}
		return first
	}}
	var mu sync.Mutex
	var ran []string
	worker := workerRuntimeFunc(func(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
		mu.Lock()
		ran = append(ran, request.Task.ID)
		mu.Unlock()
		if request.Task.ID == "b" {
			return ports.WorkerExecution{Status: ports.WorkerOutcomeFailed, Error: "compile error password=hunter2-secretvalue"}, nil
		}
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	})
	service := New(Dependencies{Model: model, Worker: worker, Validator: passingValidator(), Memory: &memoryFake{}, MaxReplans: 1})
	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "replan", Task: "t"})
	if err != nil || result.State != ports.RunStateCompleted {
		t.Fatalf("Run = %s, %v; want completed after a revised plan (%+v)", result.State, err, result.MergeDecision)
	}
	if result.Replans != 1 {
		t.Fatalf("Replans = %d, want 1", result.Replans)
	}
	if got := strings.Join(ran, ","); got != "a,b,r1-b,r1-c" {
		t.Fatalf("ran = %s, want the revised subtasks under new ids", got)
	}
	ids := make([]string, len(result.Results))
	for i, r := range result.Results {
		ids[i] = r.Task.ID
	}
	if strings.Join(ids, ",") != "a,r1-b,r1-c" {
		t.Fatalf("results = %v, want the completed subtask and the revised ones", ids)
	}
	if len(model.planned) != 2 {
		t.Fatalf("planned %d times, want 2", len(model.planned))
	}
	report := model.planned[1].PreviousAttempt
	if !strings.Contains(report, "a (Parser): completed") || !strings.Contains(report, "b (Broken approach): FAILED") {
		t.Fatalf("report does not describe the previous attempt:\n%s", report)
	}
	if strings.Contains(report, "secretvalue") {
		t.Fatalf("report leaked a secret:\n%s", report)
	}
	if got := len(result.Plan.Subtasks); got != 3 {
		t.Fatalf("final plan has %d subtasks, want the completed one plus the revised two", got)
	}
}

func TestReplanningIsOffWhenMaxReplansIsZero(t *testing.T) {
	plan := ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{{ID: "a", WorkerID: "w"}}}
	model := &meteredModel{plans: func(ports.PlanRequest) ports.ExecutionPlan { return plan }}
	worker := workerRuntimeFunc(func(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
		return ports.WorkerExecution{Status: ports.WorkerOutcomeFailed, Error: "nope"}, nil
	})
	service := New(Dependencies{Model: model, Worker: worker, Validator: passingValidator(), Memory: &memoryFake{}})
	result, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "no-replan", Task: "t"})
	if err != nil || result.State != ports.RunStateHeld || result.Replans != 0 || len(model.planned) != 1 {
		t.Fatalf("Run = %s, %v, replans=%d plans=%d; want held without a revised plan", result.State, err, result.Replans, len(model.planned))
	}
}

func TestARevisedPlanWaitsForApprovalAgain(t *testing.T) {
	first := ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{{ID: "a", WorkerID: "w"}}}
	revised := ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{{ID: "a", WorkerID: "w", Title: "again"}}}
	model := &meteredModel{plans: func(request ports.PlanRequest) ports.ExecutionPlan {
		if request.PreviousAttempt != "" {
			return revised
		}
		return first
	}}
	worker := workerRuntimeFunc(func(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
		if request.Task.ID == "a" {
			return ports.WorkerExecution{Status: ports.WorkerOutcomeFailed, Error: "nope"}, nil
		}
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	})
	service := New(Dependencies{Model: model, Worker: worker, Validator: passingValidator(), Memory: &memoryFake{}, MaxReplans: 1})
	done := make(chan ports.OrchestrationResult, 1)
	go func() {
		result, _ := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "gated", Task: "t", RequirePlanApproval: true})
		done <- result
	}()
	waitForState(t, service, "gated", ports.RunStateAwaitingApproval)
	if !service.Approve("gated") {
		t.Fatal("first approval refused")
	}
	waitForPlanTitle(t, service, "gated", "again")
	waitForState(t, service, "gated", ports.RunStateAwaitingApproval)
	if !service.Reject("gated") {
		t.Fatal("rejecting the revised plan refused")
	}
	if result := <-done; result.State != ports.RunStateCanceled {
		t.Fatalf("state = %s, want canceled after the revised plan was rejected", result.State)
	}
}

func waitForPlanTitle(t *testing.T, service *Service, runID, title string) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if plan, ok := service.PlanFor(runID); ok && len(plan.Subtasks) > 0 && plan.Subtasks[0].Title == title {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("plan with title %q never appeared", title)
}
