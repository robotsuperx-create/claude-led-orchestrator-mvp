package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/claudeorchestrator"
)

type publisherFake struct {
	mu       sync.Mutex
	requests []claudeorchestrator.PublishRequest
	result   claudeorchestrator.PublishResult
	err      error
}

func (p *publisherFake) Publish(_ context.Context, request claudeorchestrator.PublishRequest) (claudeorchestrator.PublishResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	return p.result, p.err
}

func finishedRun(t *testing.T, api *ClaudeOrchestratorAPI, fake *claudeOrchestratorAPIFake) string {
	t.Helper()
	code, runID := startClaudeRun(t, api)
	if code != http.StatusAccepted {
		t.Fatalf("start status = %d", code)
	}
	<-fake.finished
	deadline := time.Now().Add(time.Second)
	for {
		if state, _ := api.getRunState(runID); terminalClaudeOrchestratorRunState(state) {
			api.mu.RLock()
			recorded := api.details[runID].Recommendation != ""
			api.mu.RUnlock()
			if recorded {
				return runID
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("run never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestClaudeOrchestratorAPI_PublishPushesOnlyWhenAsked(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	fake.result = ports.OrchestrationResult{
		State:         ports.RunStateCompleted,
		MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeMerge},
		Usage:         ports.RunUsage{InputTokens: 1200, OutputTokens: 300, Calls: 3, BudgetTokens: 2_000_000, Priced: true, CostMicroUSD: 8100},
		Replans:       1,
	}
	publisher := &publisherFake{result: claudeorchestrator.PublishResult{
		PullRequestURL: "https://github.com/acme/app/pull/7",
		CompareURL:     "https://github.com/acme/app/compare/main...x?expand=1",
	}}
	history := &memoryRunHistory{}
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}, Publisher: publisher, History: history}
	fake.result.Workspace = ports.RunWorkspace{Branch: "placeholder", Commit: "abc123"}
	runID := finishedRun(t, api, fake)
	// The real branch is named after the run.
	api.mu.Lock()
	details := api.details[runID]
	details.Branch = claudeorchestrator.DefaultRunBranchPrefix + runID
	api.details[runID] = details
	api.mu.Unlock()
	if len(publisher.requests) != 0 {
		t.Fatal("a run was published without being asked")
	}

	router := newClaudeOrchestratorRouter(api)
	if rec := serveClaudeOrchestratorRequest(router, http.MethodPost, "/internal/claude-orchestrator/runs/"+runID+"/publish", "", "203.0.113.9", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("remote publish status = %d, want 403", rec.Code)
	}
	rec := serveClaudeOrchestratorRequest(router, http.MethodPost, "/internal/claude-orchestrator/runs/"+runID+"/publish", "", "127.0.0.1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status = %d: %s", rec.Code, rec.Body.String())
	}
	var response ClaudeOrchestratorRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.PullRequestURL != "https://github.com/acme/app/pull/7" || response.CompareURL == "" {
		t.Fatalf("response = %+v", response)
	}
	if response.Usage == nil || response.Usage.InputTokens != 1200 || response.Usage.CostUSD == nil || *response.Usage.CostUSD != 0.0081 || response.Replans != 1 {
		t.Fatalf("usage = %+v replans=%d", response.Usage, response.Replans)
	}
	if got := publisher.requests[0]; got.Branch != claudeorchestrator.DefaultRunBranchPrefix+runID || got.RunID != runID || !strings.Contains(got.Body, "nothing was merged") {
		t.Fatalf("publish request = %+v", got)
	}

	// Publishing again returns the pull request without pushing again.
	serveClaudeOrchestratorRequest(router, http.MethodPost, "/internal/claude-orchestrator/runs/"+runID+"/publish", "", "127.0.0.1", "")
	if len(publisher.requests) != 1 {
		t.Fatalf("publish pushed %d times, want once", len(publisher.requests))
	}
	records := history.snapshot()
	if len(records) != 1 || records[0].PullRequestURL != "https://github.com/acme/app/pull/7" || records[0].Usage == nil || records[0].Replans != 1 {
		t.Fatalf("history = %+v", records)
	}
}

func TestClaudeOrchestratorAPI_PublishRefusesUnfinishedOrFailingRuns(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	fake.result = ports.OrchestrationResult{State: ports.RunStateFailed, MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeHold}}
	publisher := &publisherFake{err: errors.New("remote https://user:secret@example.com rejected")}
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}, Publisher: publisher}
	runID := finishedRun(t, api, fake)
	router := newClaudeOrchestratorRouter(api)
	if rec := serveClaudeOrchestratorRequest(router, http.MethodPost, "/internal/claude-orchestrator/runs/"+runID+"/publish", "", "127.0.0.1", ""); rec.Code != http.StatusConflict {
		t.Fatalf("publishing a failed run = %d, want 409", rec.Code)
	}

	// A completed run whose push fails reports a fixed message.
	api.mu.Lock()
	api.runs[runID] = ports.RunStateHeld
	details := api.details[runID]
	details.Branch, details.Commit = claudeorchestrator.DefaultRunBranchPrefix+runID, "abc"
	api.details[runID] = details
	api.mu.Unlock()
	rec := serveClaudeOrchestratorRequest(router, http.MethodPost, "/internal/claude-orchestrator/runs/"+runID+"/publish", "", "127.0.0.1", "")
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("failed push = %d %s", rec.Code, rec.Body.String())
	}

	without := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	if rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(without), http.MethodPost, "/internal/claude-orchestrator/runs/"+runID+"/publish", "", "127.0.0.1", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("publish without a publisher = %d, want 404", rec.Code)
	}
}

func TestSafeReviewURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://github.com/acme/app/pull/7": "https://github.com/acme/app/pull/7",
		"http://github.com/acme/app/pull/7":  "",
		"https://user:pw@github.com/x":       "",
		"javascript:alert(1)":                "",
		"":                                   "",
	} {
		if got := safeReviewURL(raw); got != want {
			t.Errorf("safeReviewURL(%q) = %q, want %q", raw, got, want)
		}
	}
}
