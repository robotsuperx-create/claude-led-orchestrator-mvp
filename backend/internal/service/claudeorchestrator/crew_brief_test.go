package claudeorchestrator

import (
	"context"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Every writer sees the whole job: the person's task and the approach the plan
// says the crew shares, whatever part it was given.
func TestEveryWriterIsGivenTheTaskAndTheCrewPlanAsItsBrief(t *testing.T) {
	plan := ports.ExecutionPlan{
		Summary: "Red and cream, system fonts, one page in five sections.",
		Subtasks: []ports.PlannedSubtask{
			{ID: "base", WorkerID: "w", Instructions: "Lay out the page"},
			{ID: "story", WorkerID: "w", Instructions: "Fill the story markers", DependsOn: []string{"base"}},
			{ID: "products", WorkerID: "w", Instructions: "Fill the products markers", DependsOn: []string{"base"}},
		},
	}
	model := &modelFake{
		planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) { return plan, nil },
		reviewFn: func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
			return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
		},
	}
	var mu sync.Mutex
	briefs := map[string]string{}
	worker := workerRuntimeFunc(func(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
		mu.Lock()
		briefs[request.Task.ID] = request.Task.Brief
		mu.Unlock()
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	})
	validator := &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
		return ports.ValidationReport{Passed: true}, nil
	}}
	service := New(Dependencies{Model: model, Worker: worker, Validator: validator, Memory: &memoryFake{}})

	if _, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "run-brief", Task: "  Build the homepage.  "}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "Build the homepage.\n\nCREW PLAN\nRed and cream, system fonts, one page in five sections."
	for _, id := range []string{"base", "story", "products"} {
		if briefs[id] != want {
			t.Fatalf("brief for %s = %q, want %q", id, briefs[id], want)
		}
	}
}

func TestACrewBriefWithoutAPlanSummaryIsTheTask(t *testing.T) {
	if got := crewBrief(" Build it ", "  "); got != "Build it" {
		t.Fatalf("crewBrief = %q", got)
	}
}

// The tier is model output: only "design" means anything, and it survives.
func TestAPlanKeepsOnlyTheKnownTier(t *testing.T) {
	plan := keepKnownSkills(ports.ExecutionPlan{Subtasks: []ports.PlannedSubtask{
		{ID: "a", Tier: ports.SubtaskTierDesign},
		{ID: "b", Tier: "premium"},
		{ID: "c"},
	}})
	if plan.Subtasks[0].Tier != ports.SubtaskTierDesign || plan.Subtasks[1].Tier != "" || plan.Subtasks[2].Tier != "" {
		t.Fatalf("tiers = %q, %q, %q", plan.Subtasks[0].Tier, plan.Subtasks[1].Tier, plan.Subtasks[2].Tier)
	}
}
