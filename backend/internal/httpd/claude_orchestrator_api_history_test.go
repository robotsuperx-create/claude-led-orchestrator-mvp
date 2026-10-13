package httpd

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type memoryRunHistory struct {
	mu      sync.Mutex
	records []ports.ClaudeOrchestratorRunRecord
	saves   int
}

func (h *memoryRunHistory) Load() ([]ports.ClaudeOrchestratorRunRecord, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ports.ClaudeOrchestratorRunRecord(nil), h.records...), nil
}

func (h *memoryRunHistory) Save(records []ports.ClaudeOrchestratorRunRecord) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append([]ports.ClaudeOrchestratorRunRecord(nil), records...)
	h.saves++
	return nil
}

func (h *memoryRunHistory) snapshot() []ports.ClaudeOrchestratorRunRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]ports.ClaudeOrchestratorRunRecord(nil), h.records...)
}

const (
	historyRunDone   = "claude-run-11111111111111111111111111111111"
	historyRunActive = "claude-run-22222222222222222222222222222222"
)

func TestClaudeOrchestratorAPI_RestoresHistoryAndMarksInterruptedRuns(t *testing.T) {
	created := time.Date(2026, 10, 13, 6, 0, 0, 0, time.UTC)
	history := &memoryRunHistory{records: []ports.ClaudeOrchestratorRunRecord{
		{RunID: historyRunDone, State: ports.RunStateCompleted, Title: "Add tests", Branch: "ao/claude-orchestrator/" + historyRunDone, Commit: "abc", Recommendation: ports.MergeOutcomeMerge, CreatedAt: created, UpdatedAt: created},
		{RunID: historyRunActive, State: ports.RunStateExecuting, Title: "Refactor", CreatedAt: created, UpdatedAt: created},
		{RunID: "../not-a-run", State: ports.RunStateCompleted, Title: "forged"},
	}}
	api := &ClaudeOrchestratorAPI{Service: newClaudeOrchestratorAPIFake(), Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}, History: history}
	if err := api.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodGet, "/internal/claude-orchestrator/runs", "", "127.0.0.1", "")
	var list ClaudeOrchestratorRunList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Runs) != 2 {
		t.Fatalf("runs = %+v, want the two valid records", list.Runs)
	}
	active, done := list.Runs[0], list.Runs[1]
	if active.RunID != historyRunActive || active.State != ports.RunStateFailed || !active.Interrupted {
		t.Fatalf("interrupted run = %+v, want failed and interrupted", active)
	}
	if done.State != ports.RunStateCompleted || done.Commit != "abc" || done.Interrupted || done.Title != "Add tests" {
		t.Fatalf("finished run = %+v", done)
	}
	// The interruption is written back, so it survives the next restart too.
	saved := history.snapshot()
	if len(saved) != 2 || saved[1].State != ports.RunStateFailed || !saved[1].Interrupted {
		t.Fatalf("saved history = %+v", saved)
	}
}

func TestClaudeOrchestratorAPI_PersistsRunsAsTheyProgress(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	fake.result = ports.OrchestrationResult{
		State:         ports.RunStateCompleted,
		MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeMerge},
		Workspace:     ports.RunWorkspace{Branch: "ao/claude-orchestrator/x", Commit: "def"},
	}
	history := &memoryRunHistory{}
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}, History: history}
	if err := api.Restore(); err != nil {
		t.Fatal(err)
	}
	code, runID := startClaudeRun(t, api)
	if code != http.StatusAccepted {
		t.Fatalf("start status = %d", code)
	}
	<-fake.finished
	deadline := time.Now().Add(2 * time.Second)
	for {
		saved := history.snapshot()
		if len(saved) == 1 && saved[0].State == ports.RunStateCompleted && saved[0].Commit == "def" {
			if saved[0].RunID != runID || saved[0].Title != "work" {
				t.Fatalf("saved = %+v", saved[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("history never recorded the finished run: %+v", saved)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A fresh API over the same history sees the run after a "restart".
	restarted := &ClaudeOrchestratorAPI{Service: newClaudeOrchestratorAPIFake(), Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}, History: history}
	if err := restarted.Restore(); err != nil {
		t.Fatal(err)
	}
	rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(restarted), http.MethodGet, "/internal/claude-orchestrator/runs/"+runID, "", "127.0.0.1", "")
	var response ClaudeOrchestratorRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.State != ports.RunStateCompleted || response.Commit != "def" {
		t.Fatalf("after restart = %d %+v, %v", rec.Code, response, err)
	}
}
