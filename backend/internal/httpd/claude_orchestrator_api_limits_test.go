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
		if !api.reserveRun(id, "") {
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

func TestClaudeOrchestratorAPI_ListsRunHistoryNewestFirstWithRedactedTitles(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	fake.result = ports.OrchestrationResult{State: ports.RunStateCompleted, MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeMerge}}
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	router := newClaudeOrchestratorRouter(api)
	for _, task := range []string{"first task", "deploy with api_key=sk-live-should-not-appear please"} {
		rec := serveClaudeOrchestratorRequest(router, http.MethodPost, "/internal/claude-orchestrator/runs", fmt.Sprintf(`{"task":%q,"explicitOptIn":true}`, task), "127.0.0.1", "")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("start %q = %d", task, rec.Code)
		}
		<-fake.finished
	}

	rec := serveClaudeOrchestratorRequest(router, http.MethodGet, "/internal/claude-orchestrator/runs", "", "127.0.0.1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-live-should-not-appear") {
		t.Fatalf("history leaked a secret: %s", rec.Body.String())
	}
	var list ClaudeOrchestratorRunList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Runs) != 2 || !strings.HasPrefix(list.Runs[0].Title, "deploy with api_key=") || list.Runs[1].Title != "first task" {
		t.Fatalf("history = %+v, want newest first with titles", list.Runs)
	}
	if list.Runs[0].CreatedAt.IsZero() || list.Runs[0].RunID == "" {
		t.Fatalf("history row missing identity or time: %+v", list.Runs[0])
	}

	if remote := serveClaudeOrchestratorRequest(router, http.MethodGet, "/internal/claude-orchestrator/runs", "", "example.com", ""); remote.Code != http.StatusForbidden {
		t.Fatalf("non-local list status = %d, want 403", remote.Code)
	}
	if browser := serveClaudeOrchestratorRequest(router, http.MethodGet, "/internal/claude-orchestrator/runs", "", "127.0.0.1", "http://evil.example"); browser.Code != http.StatusForbidden {
		t.Fatalf("browser-origin list status = %d, want 403", browser.Code)
	}
}

func TestClaudeOrchestratorAPI_InfoIsDisplaySafe(t *testing.T) {
	api := &ClaudeOrchestratorAPI{
		Service: newClaudeOrchestratorAPIFake(), Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true},
		Info: ClaudeOrchestratorInfo{Repository: "my-repo", PlannerModel: "claude-opus-5-5", WorkerProvider: ports.ModelProviderDeepSeek, WorkerModel: "deepseek-chat"},
	}
	rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodGet, "/internal/claude-orchestrator", "", "127.0.0.1", "")
	var info ClaudeOrchestratorInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("info = %d %s, %v", rec.Code, rec.Body.String(), err)
	}
	if !info.Enabled || info.Repository != "my-repo" || info.WorkerProvider != ports.ModelProviderDeepSeek || info.MaxActiveRuns != DefaultClaudeOrchestratorMaxActiveRuns {
		t.Fatalf("info = %+v", info)
	}
	for _, forbidden := range []string{"apiKey", "baseUrl", "http", "/"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), strings.ToLower(forbidden)) {
			t.Fatalf("info exposes %q: %s", forbidden, rec.Body.String())
		}
	}
}
