package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClaudeOrchestratorWiringDefaultOffCreatesNoProvidersOrRouteService(t *testing.T) {
	wiring, err := newClaudeOrchestratorWiring(config.Config{}, claudeOrchestratorBuildDeps{})
	if err != nil {
		t.Fatalf("newClaudeOrchestratorWiring: %v", err)
	}
	if wiring == nil || wiring.service != nil || wiring.providers != nil || wiring.deepSeekWorker != nil {
		t.Fatalf("disabled wiring created a service or provider clients: %+v", wiring)
	}
	result, err := wiring.RunGated(context.Background(), ports.ClaudeOrchestratorRunRequest{ExplicitOptIn: true}, ports.OrchestrationRequest{
		RunID: "default-off-test", Task: "must not start",
	})
	var rejection *ports.ClaudeOrchestratorRunRejectedError
	if !errors.As(err, &rejection) || rejection.Reason != ports.ClaudeOrchestratorRunFeatureDisabled {
		t.Fatalf("RunGated() error = %v, want feature-disabled rejection", err)
	}
	if result.Rejection == nil || result.Rejection.Reason != ports.ClaudeOrchestratorRunFeatureDisabled {
		t.Fatalf("RunGated() rejection = %+v, want feature-disabled", result.Rejection)
	}
	if err := wiring.Close(context.Background()); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if err := wiring.Close(context.Background()); err != nil {
		t.Fatalf("repeated Close() = %v, want nil", err)
	}
	var nilWiring *claudeOrchestratorWiring
	if err := nilWiring.Close(context.Background()); err != nil {
		t.Fatalf("nil wiring Close() = %v, want nil", err)
	}
}

func TestClaudeOrchestratorWiringEnabledFailsClosedForMissingConfiguration(t *testing.T) {
	cfg := config.Config{ClaudeOrchestrator: config.ClaudeOrchestratorConfig{FeatureEnabled: true}}
	_, err := newClaudeOrchestratorWiring(cfg, claudeOrchestratorBuildDeps{})
	if err == nil {
		t.Fatal("enabled wiring accepted missing config")
	}
	for _, want := range []string{"CLAUDE_BASE_URL", "DEEPSEEK_BASE_URL", "WORKER_COMMANDS", "VALIDATOR_COMMANDS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("configuration error %q does not name missing %s", err, want)
		}
	}
}

func TestClaudeOrchestratorWiringEnabledWithInjectedFakes(t *testing.T) {
	cfg := configuredClaudeOrchestratorConfig()
	model := &fakeOrchestratorModel{}
	worker := &fakeOrchestratorWorker{}
	validator := &fakeOrchestratorValidator{}
	memory := &fakeOrchestratorMemory{}
	wiring, err := newClaudeOrchestratorWiring(config.Config{ClaudeOrchestrator: cfg}, claudeOrchestratorBuildDeps{
		Model: model, Worker: worker, Validator: validator, Memory: memory,
	})
	if err != nil {
		t.Fatalf("newClaudeOrchestratorWiring: %v", err)
	}
	if wiring.service == nil || wiring.providers != nil || wiring.deepSeekWorker != nil {
		t.Fatalf("fake composition has unexpected service/provider state: %+v", wiring)
	}
	got, err := wiring.RunGated(context.Background(), ports.ClaudeOrchestratorRunRequest{ExplicitOptIn: true}, ports.OrchestrationRequest{
		RunID: "enabled-fake-test", Task: "complete this bounded task",
	})
	if err != nil {
		t.Fatalf("RunGated: %v", err)
	}
	if got.Result.State != ports.RunStateCompleted || got.Rejection != nil {
		t.Fatalf("RunGated result = %+v, want completed", got)
	}
	if model.plans != 1 || model.reviews != 1 || worker.calls != 1 || validator.calls != 1 || memory.recorded != 1 {
		t.Fatalf("fake calls model=%d/%d worker=%d validator=%d memory=%d; want 1 each", model.plans, model.reviews, worker.calls, validator.calls, memory.recorded)
	}
}

func configuredClaudeOrchestratorConfig() config.ClaudeOrchestratorConfig {
	return config.ClaudeOrchestratorConfig{
		FeatureEnabled:   true,
		ClaudeProvider:   modelProviderConfig(ports.ModelProviderClaude, "claude"),
		DeepSeekProvider: modelProviderConfig(ports.ModelProviderDeepSeek, "deepseek"),
		Worker: config.ClaudeOrchestratorWorkerConfig{
			ProjectRoot: "/tmp/project", Commands: []config.ClaudeOrchestratorCommandConfig{{Name: "worker", Argv: []string{"go", "test", "./..."}}}, Timeout: 30 * time.Second,
		},
		Validator: config.ClaudeOrchestratorValidatorConfig{
			Commands: []config.ClaudeOrchestratorCommandConfig{{Name: "validate", Argv: []string{"go", "test", "./..."}}}, Timeout: 30 * time.Second,
		},
	}
}

func modelProviderConfig(provider ports.ModelProvider, name string) modelgateway.ProviderConfig {
	return modelgateway.ProviderConfig{Provider: provider, BaseURL: "https://" + name + ".invalid/v1", DefaultModel: name + "-model"}
}

type fakeOrchestratorModel struct{ plans, reviews int }

func (f *fakeOrchestratorModel) Plan(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) {
	f.plans++
	return ports.ExecutionPlan{Summary: "test plan", Subtasks: []ports.PlannedSubtask{{ID: "one", WorkerID: "worker", Provider: ports.ModelProviderDeepSeek}}}, nil
}
func (f *fakeOrchestratorModel) Review(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
	f.reviews++
	return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
}

type fakeOrchestratorWorker struct{ calls int }

func (f *fakeOrchestratorWorker) Execute(context.Context, ports.WorkerRequest) (ports.WorkerExecution, error) {
	f.calls++
	return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
}

type fakeOrchestratorValidator struct{ calls int }

func (f *fakeOrchestratorValidator) Validate(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
	f.calls++
	return ports.ValidationReport{Passed: true}, nil
}

type fakeOrchestratorMemory struct{ recorded int }

func (*fakeOrchestratorMemory) ReadContext(context.Context, ports.MemoryContextRequest) (ports.MemoryContext, error) {
	return ports.MemoryContext{}, nil
}
func (f *fakeOrchestratorMemory) RecordOutcome(context.Context, ports.MemoryOutcome) error {
	f.recorded++
	return nil
}
