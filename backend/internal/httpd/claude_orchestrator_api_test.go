package httpd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type claudeOrchestratorAPIFake struct {
	mu               sync.Mutex
	calls            int
	request          ports.OrchestrationRequest
	states           map[string]ports.RunState
	done             chan struct{}
	doneOnce         sync.Once
	started          chan string
	finished         chan string
	ctxDone          map[string]<-chan struct{}
	blockUntilCancel bool
	result           ports.OrchestrationResult
	runError         error
}

func newClaudeOrchestratorAPIFake() *claudeOrchestratorAPIFake {
	return &claudeOrchestratorAPIFake{
		states: make(map[string]ports.RunState), done: make(chan struct{}),
		started: make(chan string, 8), finished: make(chan string, 8), ctxDone: make(map[string]<-chan struct{}),
	}
}

func (f *claudeOrchestratorAPIFake) Run(ctx context.Context, request ports.OrchestrationRequest) (ports.OrchestrationResult, error) {
	f.mu.Lock()
	f.calls++
	f.request = request
	f.ctxDone[request.RunID] = ctx.Done()
	f.started <- request.RunID
	f.states[request.RunID] = ports.RunStateExecuting
	if f.blockUntilCancel {
		f.mu.Unlock()
		<-ctx.Done()
		f.mu.Lock()
		f.states[request.RunID] = ports.RunStateCanceled
		f.mu.Unlock()
		f.doneOnce.Do(func() { close(f.done) })
		f.finished <- request.RunID
		return ports.OrchestrationResult{RunID: request.RunID, State: ports.RunStateCanceled}, ctx.Err()
	}
	result, err := f.result, f.runError
	f.states[request.RunID] = result.State
	f.mu.Unlock()
	f.doneOnce.Do(func() { close(f.done) })
	f.finished <- request.RunID
	return result, err
}

func (f *claudeOrchestratorAPIFake) RunState(runID string) (ports.RunState, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok := f.states[runID]
	return state, ok
}

func (f *claudeOrchestratorAPIFake) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestClaudeOrchestratorAPI_DefaultDenyAndLocalOnly(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	validBody := `{"task":"work","explicitOptIn":true}`

	t.Run("missing gate denies by default", func(t *testing.T) {
		r := newClaudeOrchestratorRouter(&ClaudeOrchestratorAPI{Service: fake})
		rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/claude-orchestrator/runs", validBody, "127.0.0.1", "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("explicit opt-in is required", func(t *testing.T) {
		api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
		r := newClaudeOrchestratorRouter(api)
		rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/claude-orchestrator/runs", `{"task":"work"}`, "127.0.0.1", "")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})

	t.Run("remote and origin-bearing requests are denied", func(t *testing.T) {
		api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
		r := newClaudeOrchestratorRouter(api)
		for _, tc := range []struct {
			name   string
			host   string
			origin string
		}{
			{name: "remote host", host: "evil.example"},
			{name: "browser origin", host: "127.0.0.1", origin: "https://evil.example"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/claude-orchestrator/runs", validBody, tc.host, tc.origin)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
				}
			})
		}
	})

	if fake.callCount() != 0 {
		t.Fatalf("orchestrator calls = %d, want 0 for denied requests", fake.callCount())
	}
}

func TestClaudeOrchestratorAPI_StartAndPollHideSensitiveData(t *testing.T) {
	const secretTask = "deploy using api_key=do-not-return"
	fake := newClaudeOrchestratorAPIFake()
	fake.result = ports.OrchestrationResult{
		State: ports.RunStateCompleted,
		Task:  secretTask,
		Plan:  ports.ExecutionPlan{Summary: "private model response"},
		Results: []ports.CollectedTaskResult{{FinalExecution: ports.WorkerExecution{
			Output: "private worker output",
			Error:  "private provider error",
		}}},
	}
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	r := newClaudeOrchestratorRouter(api)

	started := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/claude-orchestrator/runs", fmt.Sprintf(`{"task":%q,"explicitOptIn":true}`, secretTask), "127.0.0.1", "")
	if started.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d, want %d: %s", started.Code, http.StatusAccepted, started.Body.String())
	}
	var initial ClaudeOrchestratorRunResponse
	if err := json.Unmarshal(started.Body.Bytes(), &initial); err != nil {
		t.Fatalf("decode POST response: %v", err)
	}
	if initial.RunID == "" || initial.State != ports.RunStatePending {
		t.Fatalf("initial response = %+v", initial)
	}
	assertNoSensitiveClaudeOrchestratorData(t, started.Body.String())

	select {
	case <-fake.done:
	case <-time.After(time.Second):
		t.Fatal("orchestrator was not started")
	}
	fake.mu.Lock()
	if fake.request.Task != secretTask || fake.request.RunID != initial.RunID {
		t.Fatalf("service request = %+v", fake.request)
	}
	fake.mu.Unlock()

	status := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/"+initial.RunID, "", "127.0.0.1", "")
	if status.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d: %s", status.Code, http.StatusOK, status.Body.String())
	}
	var current ClaudeOrchestratorRunResponse
	if err := json.Unmarshal(status.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if current.RunID != initial.RunID || current.State != ports.RunStateCompleted {
		t.Fatalf("status response = %+v", current)
	}
	assertNoSensitiveClaudeOrchestratorData(t, status.Body.String())

	missing := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/not-a-run", "", "127.0.0.1", "")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown run status = %d, want %d", missing.Code, http.StatusNotFound)
	}
}

func TestClaudeOrchestratorAPI_CancelIsLocalIdempotentAndRunScoped(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	fake.blockUntilCancel = true
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	r := newClaudeOrchestratorRouter(api)
	start := func() string {
		t.Helper()
		rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/claude-orchestrator/runs", `{"task":"work","explicitOptIn":true}`, "127.0.0.1", "")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("start status = %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
		}
		var response ClaudeOrchestratorRunResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode start response: %v", err)
		}
		return response.RunID
	}
	runA, runB := start(), start()
	for range 2 {
		select {
		case <-fake.started:
		case <-time.After(time.Second):
			t.Fatal("both HTTP runs did not start")
		}
	}
	base := "/internal/claude-orchestrator/runs/"
	pathA := base + runA + "/cancel"
	remote := serveClaudeOrchestratorRequest(r, http.MethodPost, pathA, "", "remote.example", "")
	if remote.Code != http.StatusForbidden {
		t.Fatalf("remote cancel status = %d, want %d", remote.Code, http.StatusForbidden)
	}
	canceled := serveClaudeOrchestratorRequest(r, http.MethodPost, pathA, "", "127.0.0.1", "")
	if canceled.Code != http.StatusOK || !strings.Contains(canceled.Body.String(), `"state":"canceled"`) {
		t.Fatalf("cancel response = %d %s, want canceled", canceled.Code, canceled.Body.String())
	}
	duplicate := serveClaudeOrchestratorRequest(r, http.MethodPost, pathA, "", "127.0.0.1", "")
	if duplicate.Code != http.StatusOK || !strings.Contains(duplicate.Body.String(), `"state":"canceled"`) {
		t.Fatalf("duplicate cancel response = %d %s, want canceled", duplicate.Code, duplicate.Body.String())
	}
	select {
	case finished := <-fake.finished:
		if finished != runA {
			t.Fatalf("finished run = %q, want canceled run %q", finished, runA)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not interrupt the selected run context")
	}
	fake.mu.Lock()
	secondContext := fake.ctxDone[runB]
	fake.mu.Unlock()
	select {
	case <-secondContext:
		t.Fatal("canceling run A unexpectedly canceled run B")
	default:
	}
	status := serveClaudeOrchestratorRequest(r, http.MethodGet, base+runA, "", "127.0.0.1", "")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"state":"canceled"`) {
		t.Fatalf("canceled run polling = %d %s", status.Code, status.Body.String())
	}
	unknown := serveClaudeOrchestratorRequest(r, http.MethodPost, base+"unknown/cancel", "", "127.0.0.1", "")
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown run cancel status = %d, want %d", unknown.Code, http.StatusNotFound)
	}
	cleanup := serveClaudeOrchestratorRequest(r, http.MethodPost, base+runB+"/cancel", "", "127.0.0.1", "")
	if cleanup.Code != http.StatusOK {
		t.Fatalf("cleanup cancel status = %d, want %d: %s", cleanup.Code, http.StatusOK, cleanup.Body.String())
	}
	select {
	case finished := <-fake.finished:
		if finished != runB {
			t.Fatalf("cleanup finished run = %q, want %q", finished, runB)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup cancellation did not finish the second run")
	}
}

func TestClaudeOrchestratorAPI_BoundsAndStrictlyParsesBody(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	r := newClaudeOrchestratorRouter(api)

	oversized := fmt.Sprintf(`{"task":%q,"explicitOptIn":true}`, strings.Repeat("x", claudeOrchestratorMaxBodyBytes))
	tooLarge := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/claude-orchestrator/runs", oversized, "127.0.0.1", "")
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want %d: %s", tooLarge.Code, http.StatusRequestEntityTooLarge, tooLarge.Body.String())
	}

	unknown := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/claude-orchestrator/runs", `{"task":"work","explicitOptIn":true,"credential":"secret"}`, "127.0.0.1", "")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want %d", unknown.Code, http.StatusBadRequest)
	}
	if fake.callCount() != 0 {
		t.Fatalf("orchestrator calls = %d, want 0", fake.callCount())
	}
}

func newClaudeOrchestratorRouter(api *ClaudeOrchestratorAPI) chi.Router {
	r := chi.NewRouter()
	api.Register(r)
	return r
}

func serveClaudeOrchestratorRequest(r chi.Router, method, path, body, host, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func assertNoSensitiveClaudeOrchestratorData(t *testing.T, body string) {
	t.Helper()
	for _, forbidden := range []string{"deploy using", "api_key", "do-not-return", "private model response", "private worker output", "private provider error"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked sensitive data %q: %s", forbidden, body)
		}
	}
}
