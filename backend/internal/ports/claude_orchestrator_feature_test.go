package ports_test

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.ClaudeOrchestratorFeatureFlag = ports.ClaudeOrchestratorRunPolicy{}
	_ ports.ClaudeOrchestratorRunGate     = ports.ClaudeOrchestratorRunPolicy{}
)

func TestClaudeOrchestratorRunPolicyDefaultsDisabled(t *testing.T) {
	var policy ports.ClaudeOrchestratorRunPolicy
	if policy.Enabled() {
		t.Fatal("zero-value feature flag is enabled; want inactive by default")
	}

	err := policy.CheckRun(ports.ClaudeOrchestratorRunRequest{ExplicitOptIn: true})
	assertRunRejected(t, err, ports.ClaudeOrchestratorRunFeatureDisabled)
}

func TestClaudeOrchestratorRunRequiresExplicitOptIn(t *testing.T) {
	policy := ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}
	if !policy.Enabled() {
		t.Fatal("explicitly enabled feature flag reports disabled")
	}

	err := policy.CheckRun(ports.ClaudeOrchestratorRunRequest{})
	assertRunRejected(t, err, ports.ClaudeOrchestratorRunOptInRequired)
}

func TestClaudeOrchestratorRunAllowsExplicitOptIn(t *testing.T) {
	policy := ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}
	if err := policy.CheckRun(ports.ClaudeOrchestratorRunRequest{ExplicitOptIn: true}); err != nil {
		t.Fatalf("CheckRun() error = %v, want nil", err)
	}
}

func assertRunRejected(t *testing.T, err error, want ports.ClaudeOrchestratorRunRejectionReason) {
	t.Helper()
	if err == nil {
		t.Fatal("CheckRun() error = nil, want typed rejection")
	}
	var rejection *ports.ClaudeOrchestratorRunRejectedError
	if !errors.As(err, &rejection) {
		t.Fatalf("CheckRun() error type = %T, want *ports.ClaudeOrchestratorRunRejectedError", err)
	}
	if rejection.Reason != want {
		t.Fatalf("rejection reason = %q, want %q", rejection.Reason, want)
	}
}
