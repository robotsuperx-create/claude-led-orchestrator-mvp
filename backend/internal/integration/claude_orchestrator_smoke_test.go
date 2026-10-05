package integration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const claudeOrchestratorRunsPath = "/internal/claude-orchestrator/runs"

type smokeOrchestratorService struct {
	mu      sync.Mutex
	request ports.OrchestrationRequest
	states  map[string]ports.RunState
	done    chan struct{}
}

func newSmokeOrchestratorService() *smokeOrchestratorService {
	return &smokeOrchestratorService{states: make(map[string]ports.RunState), done: make(chan struct{})}
}

func (s *smokeOrchestratorService) Run(_ context.Context, request ports.OrchestrationRequest) (ports.OrchestrationResult, error) {
	s.mu.Lock()
	s.request = request
	s.states[request.RunID] = ports.RunStateCompleted
	s.mu.Unlock()
	close(s.done)

	return ports.OrchestrationResult{
		RunID: request.RunID,
		State: ports.RunStateCompleted,
		Task:  request.Task,
		Plan:  ports.ExecutionPlan{Summary: "private planner output"},
		Results: []ports.CollectedTaskResult{{FinalExecution: ports.WorkerExecution{
			Output: "private worker output",
			Error:  "private provider error: api_secret=never-expose-this",
		}}},
	}, nil
}

func (s *smokeOrchestratorService) RunState(runID string) (ports.RunState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[runID]
	return state, ok
}

func (s *smokeOrchestratorService) lastRequest() ports.OrchestrationRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.request
}

type smokeOrchestratorGate struct {
	mu       sync.Mutex
	allow    bool
	calls    int
	lastSeen ports.ClaudeOrchestratorRunRequest
}

func (g *smokeOrchestratorGate) CheckRun(request ports.ClaudeOrchestratorRunRequest) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	g.lastSeen = request
	if !g.allow || !request.ExplicitOptIn {
		return errors.New("private gate policy details")
	}
	return nil
}

func (g *smokeOrchestratorGate) snapshot() (int, ports.ClaudeOrchestratorRunRequest) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls, g.lastSeen
}

func TestClaudeOrchestratorHTTPIntegrationSmoke(t *testing.T) {
	service := newSmokeOrchestratorService()
	gate := &smokeOrchestratorGate{allow: true}
	router := chi.NewRouter()
	(&httpd.ClaudeOrchestratorAPI{Service: service, Gate: gate}).Register(router)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	client := server.Client()
	secretTask := "private task: deploy with api_key=never-expose-this"

	t.Run("remote caller is denied", func(t *testing.T) {
		req := newSmokeRequest(t, http.MethodPost, server.URL+claudeOrchestratorRunsPath, `{"task":"work","explicitOptIn":true}`)
		req.Host = "remote.example"
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST remote request: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("remote POST status = %d, want %d: %s", resp.StatusCode, http.StatusForbidden, body)
		}
	})

	t.Run("missing opt-in is denied", func(t *testing.T) {
		resp := postSmokeJSON(t, client, server.URL+claudeOrchestratorRunsPath, `{"task":"work"}`)
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("POST without opt-in status = %d, want %d: %s", resp.StatusCode, http.StatusForbidden, body)
		}
	})

	startBody, err := json.Marshal(map[string]any{"task": secretTask, "explicitOptIn": true, "maxRetries": 1})
	if err != nil {
		t.Fatal(err)
	}
	started := postSmokeJSON(t, client, server.URL+claudeOrchestratorRunsPath, string(startBody))
	startResponse := decodeSmokeResponse(t, started, http.StatusAccepted)
	if runID, ok := startResponse["runId"].(string); !ok || runID == "" {
		t.Fatalf("POST response runId = %#v, want non-empty string", startResponse["runId"])
	}
	if startResponse["state"] != string(ports.RunStatePending) {
		t.Fatalf("POST response state = %#v, want pending", startResponse["state"])
	}
	runID := startResponse["runId"].(string)

	select {
	case <-service.done:
	case <-time.After(2 * time.Second):
		t.Fatal("orchestrator service did not run")
	}
	gotRequest := service.lastRequest()
	if gotRequest.RunID != runID || gotRequest.Task != secretTask || gotRequest.MaxRetries != 1 {
		t.Fatalf("service request = %+v, want runID %q, task %q, maxRetries 1", gotRequest, runID, secretTask)
	}
	gateCalls, gateRequest := gate.snapshot()
	if gateCalls != 1 || !gateRequest.ExplicitOptIn {
		t.Fatalf("gate calls/request = %d/%+v, want exactly one opted-in call", gateCalls, gateRequest)
	}

	statusURL := server.URL + claudeOrchestratorRunsPath + "/" + runID
	statusResp, err := client.Get(statusURL)
	if err != nil {
		t.Fatalf("GET run status: %v", err)
	}
	statusResponse := decodeSmokeResponse(t, statusResp, http.StatusOK)
	if statusResponse["runId"] != runID || statusResponse["state"] != string(ports.RunStateCompleted) {
		t.Fatalf("GET status response = %#v, want runId %q and state completed", statusResponse, runID)
	}
}

func newSmokeRequest(t *testing.T, method, url, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func postSmokeJSON(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	resp, err := client.Do(newSmokeRequest(t, http.MethodPost, url, body))
	if err != nil {
		t.Fatalf("POST request: %v", err)
	}
	return resp
}

func decodeSmokeResponse(t *testing.T, response *http.Response, wantStatus int) map[string]any {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read HTTP response: %v", err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("HTTP status = %d, want %d: %s", response.StatusCode, wantStatus, body)
	}
	assertSmokeResponseIsMinimalAndRedacted(t, body)
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode HTTP response %q: %v", body, err)
	}
	return decoded
}

func assertSmokeResponseIsMinimalAndRedacted(t *testing.T, body []byte) {
	t.Helper()
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode response for redaction check %q: %v", body, err)
	}
	if len(decoded) != 2 || decoded["runId"] == nil || decoded["state"] == nil {
		t.Fatalf("response keys = %v, want only runId and state", decoded)
	}
	for _, secret := range []string{
		"private task", "deploy with", "api_key", "never-expose-this",
		"private planner output", "private worker output", "private provider error", "private gate policy details",
	} {
		if strings.Contains(string(body), secret) {
			t.Errorf("response leaked %q: %s", secret, body)
		}
	}
}
