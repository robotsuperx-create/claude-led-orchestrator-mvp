package httpd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func startClaudeRun(t *testing.T, api *ClaudeOrchestratorAPI) (int, string) {
	t.Helper()
	rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodPost,
		"/internal/claude-orchestrator/runs", `{"task":"work","explicitOptIn":true}`, "127.0.0.1", "")
	var response ClaudeOrchestratorRunResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &response)
	return rec.Code, response.RunID
}

func TestClaudeOrchestratorAPI_LimitsConcurrentRuns(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	fake.blockUntilCancel = true
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}, MaxActiveRuns: 2}

	var running []string
	for i := 0; i < 2; i++ {
		code, runID := startClaudeRun(t, api)
		if code != http.StatusAccepted {
			t.Fatalf("run %d status = %d, want 202", i, code)
		}
		running = append(running, runID)
		<-fake.started
	}
	if code, _ := startClaudeRun(t, api); code != http.StatusTooManyRequests {
		t.Fatalf("third concurrent run status = %d, want 429", code)
	}
	if fake.callCount() != 2 {
		t.Fatalf("service calls = %d, want 2: a rejected run must not start", fake.callCount())
	}

	// Finishing one run frees a slot.
	cancel := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodPost,
		"/internal/claude-orchestrator/runs/"+running[0]+"/cancel", "", "127.0.0.1", "")
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel status = %d: %s", cancel.Code, cancel.Body.String())
	}
	select {
	case <-fake.finished:
	case <-time.After(time.Second):
		t.Fatal("canceled run did not finish")
	}
	if code, _ := startClaudeRun(t, api); code != http.StatusAccepted {
		t.Fatalf("run after a slot freed status = %d, want 202", code)
	}
}

func TestClaudeOrchestratorAPI_ReportsWorkspaceAndRecommendation(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	fake.result = ports.OrchestrationResult{
		State:         ports.RunStateHeld,
		MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeHold, Reasons: []string{"private reason"}},
		Workspace:     ports.RunWorkspace{Path: "/Users/someone/.ao/worktrees/x", Branch: "ao/claude-orchestrator/r", Commit: "abc123"},
	}
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	code, runID := startClaudeRun(t, api)
	if code != http.StatusAccepted {
		t.Fatalf("start status = %d", code)
	}
	<-fake.finished
	var response ClaudeOrchestratorRunResponse
	deadline := time.Now().Add(time.Second)
	for {
		rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodGet, "/internal/claude-orchestrator/runs/"+runID, "", "127.0.0.1", "")
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Recommendation != "" || time.Now().After(deadline) {
			body := rec.Body.String()
			for _, private := range []string{"private reason", "/Users/someone"} {
				if strings.Contains(body, private) {
					t.Fatalf("status leaked %q: %s", private, body)
				}
			}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if response.State != ports.RunStateHeld || response.Branch != "ao/claude-orchestrator/r" || response.Commit != "abc123" || response.Recommendation != ports.MergeOutcomeHold {
		t.Fatalf("status = %+v, want held with branch, commit and hold recommendation", response)
	}
}

func TestClaudeOrchestratorAPI_ForgetsOldestTerminalRuns(t *testing.T) {
	api := &ClaudeOrchestratorAPI{}
	for i := 0; i < claudeOrchestratorMaxRetainedRuns+10; i++ {
		id := fmt.Sprintf("run-%d", i)
		if !api.reserveRun(id) {
			t.Fatalf("reserveRun(%s) refused", id)
		}
		api.setRunState(id, ports.RunStateCompleted)
	}
	if len(api.runs) > claudeOrchestratorMaxRetainedRuns+1 {
		t.Fatalf("retained %d runs, want at most %d", len(api.runs), claudeOrchestratorMaxRetainedRuns+1)
	}
	if _, ok := api.getRunState("run-0"); ok {
		t.Fatal("the oldest terminal run was not forgotten")
	}
	if _, ok := api.getRunState(fmt.Sprintf("run-%d", claudeOrchestratorMaxRetainedRuns+9)); !ok {
		t.Fatal("the newest run was forgotten")
	}
}
