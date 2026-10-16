package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/claudeorchestrator"
)

// fakeGitHubToken is assembled at runtime so secret scanners do not flag the fixture.
var fakeGitHubToken = "gh" + "p_" + strings.Repeat("a", 36)

type planModel struct{}

func (planModel) Plan(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) {
	return ports.ExecutionPlan{Summary: "Two steps; token " + fakeGitHubToken + " must not leak", Subtasks: []ports.PlannedSubtask{
		{ID: "a", WorkerID: "w", Title: "Write the parser", Instructions: "Implement parse()", Provider: ports.ModelProviderDeepSeek},
		{ID: "b", WorkerID: "w", Title: "Test it", Instructions: strings.Repeat("x", 5000), DependsOn: []string{"a"}},
	}}, nil
}

func (planModel) Review(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
	return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
}

type planWorker struct{}

func (planWorker) Execute(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
	return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
}

type planValidator struct{}

func (planValidator) Validate(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
	return ports.ValidationReport{Passed: true}, nil
}

type planMemory struct{}

func (planMemory) ReadContext(context.Context, ports.MemoryContextRequest) (ports.MemoryContext, error) {
	return ports.MemoryContext{}, nil
}
func (planMemory) RecordOutcome(context.Context, ports.MemoryOutcome) error { return nil }

func pollRunState(t *testing.T, api *ClaudeOrchestratorAPI, runID string, want ports.RunState) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodGet, "/internal/claude-orchestrator/runs/"+runID, "", "127.0.0.1", "")
		var response ClaudeOrchestratorRunResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &response)
		if response.State == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %q, want %q", response.State, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestClaudeOrchestratorAPI_PlanApprovalFlow(t *testing.T) {
	newAPI := func() *ClaudeOrchestratorAPI {
		service := claudeorchestrator.New(claudeorchestrator.Dependencies{Model: planModel{}, Worker: planWorker{}, Validator: planValidator{}, Memory: planMemory{}})
		return &ClaudeOrchestratorAPI{Service: service, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	}
	start := func(api *ClaudeOrchestratorAPI) string {
		rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodPost, "/internal/claude-orchestrator/runs",
			`{"task":"work","explicitOptIn":true,"approvePlan":true}`, "127.0.0.1", "")
		var response ClaudeOrchestratorRunResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &response)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("start = %d %s", rec.Code, rec.Body.String())
		}
		return response.RunID
	}

	t.Run("approve", func(t *testing.T) {
		api := newAPI()
		runID := start(api)
		pollRunState(t, api, runID, ports.RunStateAwaitingApproval)

		rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodGet, "/internal/claude-orchestrator/runs/"+runID+"/plan", "", "127.0.0.1", "")
		var plan ClaudeOrchestratorPlan
		if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("plan = %d %s", rec.Code, rec.Body.String())
		}
		if len(plan.Subtasks) != 2 || plan.Subtasks[0].Provider != ports.ModelProviderDeepSeek || plan.Subtasks[1].DependsOn[0] != "a" {
			t.Fatalf("plan = %+v", plan)
		}
		if strings.Contains(plan.Summary, "ghp_") {
			t.Fatalf("plan summary leaked a token: %q", plan.Summary)
		}
		if runes := []rune(plan.Subtasks[1].Instructions); len(runes) > claudeOrchestratorMaxPlanInstrRunes+1 {
			t.Fatalf("instructions were not bounded: %d runes", len(runes))
		}

		rec = serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodPost, "/internal/claude-orchestrator/runs/"+runID+"/approve", "", "127.0.0.1", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("approve = %d %s", rec.Code, rec.Body.String())
		}
		pollRunState(t, api, runID, ports.RunStateCompleted)
		rec = serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodPost, "/internal/claude-orchestrator/runs/"+runID+"/approve", "", "127.0.0.1", "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("second approve = %d, want 409", rec.Code)
		}
	})

	t.Run("reject", func(t *testing.T) {
		api := newAPI()
		runID := start(api)
		pollRunState(t, api, runID, ports.RunStateAwaitingApproval)
		rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodPost, "/internal/claude-orchestrator/runs/"+runID+"/reject", "", "127.0.0.1", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("reject = %d %s", rec.Code, rec.Body.String())
		}
		pollRunState(t, api, runID, ports.RunStateCanceled)
	})

	t.Run("remote callers are refused", func(t *testing.T) {
		api := newAPI()
		rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodPost, "/internal/claude-orchestrator/runs/claude-run-x/approve", "", "192.0.2.10", "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("remote approve = %d, want 403", rec.Code)
		}
	})
}
