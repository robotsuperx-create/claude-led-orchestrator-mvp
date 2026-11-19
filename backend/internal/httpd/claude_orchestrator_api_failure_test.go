package httpd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A failed run says why it stopped, short and with secrets redacted, so the
// person is not left with a bare "failed".
func TestClaudeOrchestratorAPI_AFailedRunSaysWhyItStopped(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	reason := "request executive plan: the plan was cut off at the output limit twice; token sk-ant-api03-" + strings.Repeat("a", 40) + " " + strings.Repeat("x", 600)
	fake.result = ports.OrchestrationResult{State: ports.RunStateFailed, MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeHold, Reasons: []string{reason}}}
	fake.runError = errors.New(reason)
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	runID := finishedRun(t, api, fake)

	rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodGet, "/internal/claude-orchestrator/runs/"+runID, "", "127.0.0.1", "")
	var response ClaudeOrchestratorRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if response.State != ports.RunStateFailed || !strings.HasPrefix(response.Failure, "request executive plan: the plan was cut off") {
		t.Fatalf("response = %+v, want the failed run's reason", response)
	}
	if strings.Contains(response.Failure, "sk-ant-api03") || len([]rune(response.Failure)) > maxRunFailureRunes+1 {
		t.Fatalf("failure = %q, want it redacted and bounded", response.Failure)
	}
}

func TestClaudeOrchestratorAPI_AHeldRunShowsNoFailure(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	fake.result = ports.OrchestrationResult{State: ports.RunStateHeld, MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeHold, Reasons: []string{"review asked for changes"}}}
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	runID := finishedRun(t, api, fake)

	rec := serveClaudeOrchestratorRequest(newClaudeOrchestratorRouter(api), http.MethodGet, "/internal/claude-orchestrator/runs/"+runID, "", "127.0.0.1", "")
	if strings.Contains(rec.Body.String(), `"failure"`) {
		t.Fatalf("held run = %s, want no failure field", rec.Body.String())
	}
}
