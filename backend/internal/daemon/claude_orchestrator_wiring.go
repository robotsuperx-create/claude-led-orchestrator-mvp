package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/claudeorchestrator"
)

// claudeOrchestratorBuildDeps is the composition seam for fake/offline tests
// and for daemon-owned workspace and command-runner dependencies.
type claudeOrchestratorBuildDeps struct {
	Model     ports.ModelGateway
	Worker    ports.WorkerRuntime
	Validator ports.Validator
	Memory    ports.ProjectMemory
	Worktrees ports.WorktreeManager
	Runner    claudeorchestrator.CommandRunner
}

// claudeOrchestratorWiring is the daemon composition boundary. In the default
// off state it deliberately owns no service or network client.
type claudeOrchestratorWiring struct {
	service        *claudeorchestrator.Service
	gate           ports.ClaudeOrchestratorRunGate
	providers      *modelgateway.Registry
	deepSeekWorker *modelgateway.ProviderAdapter
}

// validateClaudeOrchestratorConfig validates presence without constructing
// clients. Diagnostics name missing settings only and never include secret values.
func validateClaudeOrchestratorConfig(cfg config.ClaudeOrchestratorConfig) error {
	var missing []string
	checkProvider := func(name string, provider modelgateway.ProviderConfig, want string) {
		if string(provider.Provider) != want || strings.TrimSpace(provider.BaseURL) == "" {
			missing = append(missing, name+"_BASE_URL")
		}
		if strings.TrimSpace(provider.DefaultModel) == "" {
			missing = append(missing, name+"_MODEL")
		}
	}
	checkProvider("AO_CLAUDE_ORCHESTRATOR_CLAUDE", cfg.ClaudeProvider, string(ports.ModelProviderClaude))
	checkProvider("AO_CLAUDE_ORCHESTRATOR_DEEPSEEK", cfg.DeepSeekProvider, string(ports.ModelProviderDeepSeek))
	if strings.TrimSpace(cfg.Worker.ProjectRoot) == "" {
		missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT")
	}
	if len(cfg.Worker.Commands) == 0 {
		missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS")
	}
	if cfg.Worker.Timeout <= 0 {
		missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_WORKER_TIMEOUT")
	}
	if len(cfg.Validator.Commands) == 0 {
		missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_VALIDATOR_COMMANDS")
	}
	if cfg.Validator.Timeout <= 0 {
		missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_VALIDATOR_TIMEOUT")
	}
	if len(missing) != 0 {
		return fmt.Errorf("Claude orchestrator is enabled but required configuration is missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

func newClaudeOrchestratorWiring(cfg config.Config, injected claudeOrchestratorBuildDeps) (*claudeOrchestratorWiring, error) {
	wiring := &claudeOrchestratorWiring{
		gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: cfg.ClaudeOrchestrator.FeatureEnabled},
	}
	if !cfg.ClaudeOrchestrator.FeatureEnabled {
		return wiring, nil
	}
	if err := validateClaudeOrchestratorConfig(cfg.ClaudeOrchestrator); err != nil {
		return nil, err
	}

	model := injected.Model
	if model == nil {
		registry, err := modelgateway.NewRegistry(cfg.ClaudeOrchestrator.ClaudeProvider, cfg.ClaudeOrchestrator.DeepSeekProvider)
		if err != nil {
			return nil, fmt.Errorf("configure Claude orchestrator model providers: %w", err)
		}
		claude, err := registry.Provider(ports.ModelProviderClaude)
		if err != nil {
			return nil, errors.New("configure Claude orchestrator Claude provider")
		}
		deepSeek, err := registry.Provider(ports.ModelProviderDeepSeek)
		if err != nil {
			return nil, errors.New("configure Claude orchestrator DeepSeek provider")
		}
		wiring.providers = registry
		wiring.deepSeekWorker = deepSeek
		model = claude
	}

	runner := injected.Runner
	if runner == nil {
		runner = claudeorchestrator.ExecCommandRunner{}
	}
	validator := injected.Validator
	if validator == nil {
		var err error
		validator, err = claudeorchestrator.NewValidator(toOrchestratorCommands(cfg.ClaudeOrchestrator.Validator.Commands), cfg.ClaudeOrchestrator.Validator.Timeout, runner)
		if err != nil {
			return nil, fmt.Errorf("configure Claude orchestrator validator: %w", err)
		}
	}
	worker := injected.Worker
	if worker == nil {
		if injected.Worktrees == nil {
			return nil, errors.New("Claude orchestrator is enabled but its worker worktree manager is unavailable")
		}
		worktreeRunner, ok := runner.(claudeorchestrator.WorktreeCommandRunner)
		if !ok {
			return nil, errors.New("Claude orchestrator worker requires a worktree-capable command runner")
		}
		var err error
		worker, err = claudeorchestrator.NewWorkerRuntime(injected.Worktrees, validator, worktreeRunner, claudeorchestrator.WorkerRuntimeConfig{
			ProjectRoot: cfg.ClaudeOrchestrator.Worker.ProjectRoot,
			Commands:    toOrchestratorCommands(cfg.ClaudeOrchestrator.Worker.Commands),
			Timeout:     cfg.ClaudeOrchestrator.Worker.Timeout,
		})
		if err != nil {
			return nil, fmt.Errorf("configure Claude orchestrator worker: %w", err)
		}
	}
	memory := injected.Memory
	if memory == nil {
		memory = ephemeralClaudeOrchestratorMemory{}
	}
	wiring.service = claudeorchestrator.New(claudeorchestrator.Dependencies{
		Model: model, Worker: worker, Validator: validator, Memory: memory,
	})
	return wiring, nil
}

func toOrchestratorCommands(commands []config.ClaudeOrchestratorCommandConfig) []claudeorchestrator.ValidationCommand {
	result := make([]claudeorchestrator.ValidationCommand, len(commands))
	for i, command := range commands {
		result[i] = claudeorchestrator.ValidationCommand{Name: command.Name, Argv: append([]string(nil), command.Argv...)}
	}
	return result
}

// ephemeralClaudeOrchestratorMemory keeps this composition usable without
// inventing a renderer-facing persistence/configuration surface.
type ephemeralClaudeOrchestratorMemory struct{}

func (ephemeralClaudeOrchestratorMemory) ReadContext(context.Context, ports.MemoryContextRequest) (ports.MemoryContext, error) {
	return ports.MemoryContext{}, nil
}

func (ephemeralClaudeOrchestratorMemory) RecordOutcome(context.Context, ports.MemoryOutcome) error {
	return nil
}

func (w *claudeOrchestratorWiring) RunGated(
	ctx context.Context,
	gateRequest ports.ClaudeOrchestratorRunRequest,
	request ports.OrchestrationRequest,
) (claudeorchestrator.GatedRunResult, error) {
	if w == nil {
		var service *claudeorchestrator.Service
		return service.RunGated(ctx, nil, gateRequest, request)
	}
	return w.service.RunGated(ctx, w.gate, gateRequest, request)
}

// Close is safe to call on nil or repeatedly; modelgateway clients own no
// background goroutines or open connections between requests.
func (w *claudeOrchestratorWiring) Close(context.Context) error { return nil }
