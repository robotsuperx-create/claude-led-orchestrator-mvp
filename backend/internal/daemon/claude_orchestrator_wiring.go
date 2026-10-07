package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/claudeorchestrator"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sandboxrunner"
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
	Sandbox   ports.SandboxRunner
	Author    ports.CodeAuthor
}

// claudeOrchestratorManagedRoot is where per-run worktrees are created. It is
// under the daemon data dir so all AO state stays beneath ~/.ao.
func claudeOrchestratorManagedRoot(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "worktrees", "claude-orchestrator")
}

// claudeOrchestratorWiring is the daemon composition boundary. In the default
// off state it deliberately owns no service or network client.
type claudeOrchestratorWiring struct {
	service   *claudeorchestrator.Service
	gate      ports.ClaudeOrchestratorRunGate
	providers *modelgateway.Registry
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
	if cfg.Worker.SandboxEnabled {
		if strings.TrimSpace(cfg.Worker.SandboxImage) == "" {
			missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_IMAGE")
		}
		if cfg.Worker.SandboxMemoryBytes <= 0 {
			missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_MEMORY_BYTES")
		}
		if cfg.Worker.SandboxNanoCPUs <= 0 {
			missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_NANO_CPUS")
		}
		if cfg.Worker.SandboxPIDs <= 0 {
			missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_PIDS")
		}
	}
	if len(cfg.Validator.Commands) == 0 {
		missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_VALIDATOR_COMMANDS")
	}
	if cfg.Validator.Timeout <= 0 {
		missing = append(missing, "AO_CLAUDE_ORCHESTRATOR_VALIDATOR_TIMEOUT")
	}
	if len(missing) != 0 {
		return fmt.Errorf("claude orchestrator is enabled but required configuration is missing: %s", strings.Join(missing, ", "))
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
	author := injected.Author
	if model == nil {
		registry, err := modelgateway.NewRegistry(cfg.ClaudeOrchestrator.ClaudeProvider, cfg.ClaudeOrchestrator.DeepSeekProvider)
		if err != nil {
			return nil, fmt.Errorf("configure Claude orchestrator model providers: %w", err)
		}
		claude, err := registry.Provider(ports.ModelProviderClaude)
		if err != nil {
			return nil, errors.New("configure Claude orchestrator Claude provider")
		}
		if _, err := registry.Provider(ports.ModelProviderDeepSeek); err != nil {
			return nil, errors.New("configure Claude orchestrator DeepSeek provider")
		}
		wiring.providers = registry
		model = claude
		if author == nil {
			// Claude plans and reviews; each subtask's code is written by the
			// provider its plan names, defaulting to the configured worker.
			author, err = modelgateway.NewAuthorRouter(registry, cfg.ClaudeOrchestrator.Worker.DefaultProvider)
			if err != nil {
				return nil, fmt.Errorf("configure Claude orchestrator code author: %w", err)
			}
		}
	}

	runner := injected.Runner
	if runner == nil {
		runner = claudeorchestrator.ExecCommandRunner{}
	}
	sandbox := injected.Sandbox
	if sandbox == nil && cfg.ClaudeOrchestrator.Worker.SandboxEnabled {
		executor, sandboxErr := sandboxrunner.NewDockerExecutor(sandboxrunner.DockerConfig{
			Enabled:       true,
			AllowedImages: []string{cfg.ClaudeOrchestrator.Worker.SandboxImage},
		})
		if sandboxErr != nil {
			return nil, fmt.Errorf("configure Claude orchestrator Docker sandbox: %w", sandboxErr)
		}
		sandbox = sandboxrunner.New(executor)
	}
	validator := injected.Validator
	if validator == nil {
		// Validation always runs inside a verified worktree of the configured
		// project, so it needs the same worktree boundary as the worker.
		if injected.Worktrees == nil {
			return nil, errors.New("validator worktree manager is unavailable while the Claude orchestrator is enabled")
		}
		workspace := claudeorchestrator.ValidatorWorkspace{
			Worktrees:   injected.Worktrees,
			ProjectRoot: cfg.ClaudeOrchestrator.Worker.ProjectRoot,
		}
		commands := toOrchestratorCommands(cfg.ClaudeOrchestrator.Validator.Commands)
		var err error
		if sandbox != nil {
			validator, err = claudeorchestrator.NewValidatorWithSandbox(
				commands,
				cfg.ClaudeOrchestrator.Validator.Timeout,
				runner,
				sandbox,
				cfg.ClaudeOrchestrator.Worker.SandboxImage,
				ports.SandboxResourceLimits{
					MemoryBytes: cfg.ClaudeOrchestrator.Worker.SandboxMemoryBytes,
					NanoCPUs:    cfg.ClaudeOrchestrator.Worker.SandboxNanoCPUs,
					PIDs:        cfg.ClaudeOrchestrator.Worker.SandboxPIDs,
				},
				workspace,
			)
		} else {
			validator, err = claudeorchestrator.NewValidatorInWorkspace(commands, cfg.ClaudeOrchestrator.Validator.Timeout, runner, workspace)
		}
		if err != nil {
			return nil, fmt.Errorf("configure Claude orchestrator validator: %w", err)
		}
	}
	worker := injected.Worker
	if worker == nil {
		if injected.Worktrees == nil {
			return nil, errors.New("worker worktree manager is unavailable while the Claude orchestrator is enabled")
		}
		worktreeRunner, ok := runner.(claudeorchestrator.WorktreeCommandRunner)
		if !ok {
			return nil, errors.New("the Claude orchestrator worker requires a worktree-capable command runner")
		}
		var err error
		worker, err = claudeorchestrator.NewWorkerRuntime(injected.Worktrees, validator, worktreeRunner, claudeorchestrator.WorkerRuntimeConfig{
			ProjectRoot:  cfg.ClaudeOrchestrator.Worker.ProjectRoot,
			Commands:     toOrchestratorCommands(cfg.ClaudeOrchestrator.Worker.Commands),
			Timeout:      cfg.ClaudeOrchestrator.Worker.Timeout,
			Sandbox:      sandbox,
			SandboxImage: cfg.ClaudeOrchestrator.Worker.SandboxImage,
			SandboxLimits: ports.SandboxResourceLimits{
				MemoryBytes: cfg.ClaudeOrchestrator.Worker.SandboxMemoryBytes,
				NanoCPUs:    cfg.ClaudeOrchestrator.Worker.SandboxNanoCPUs,
				PIDs:        cfg.ClaudeOrchestrator.Worker.SandboxPIDs,
			},
			Author: author,
		})
		if err != nil {
			return nil, fmt.Errorf("configure Claude orchestrator worker: %w", err)
		}
	}
	memory := injected.Memory
	if memory == nil {
		memory = ephemeralClaudeOrchestratorMemory{}
	}
	var workspaces ports.RunWorkspaceProvisioner
	if cfg.ClaudeOrchestrator.Worker.WorktreePath == "" && injected.Worktrees != nil {
		gitRunner, ok := runner.(claudeorchestrator.WorktreeCommandRunner)
		if !ok {
			return nil, errors.New("the Claude orchestrator run workspaces require a worktree-capable command runner")
		}
		provisioner, err := claudeorchestrator.NewRunWorkspaces(injected.Worktrees, gitRunner, claudeorchestrator.RunWorkspacesConfig{
			ProjectRoot: cfg.ClaudeOrchestrator.Worker.ProjectRoot,
			ManagedRoot: claudeOrchestratorManagedRoot(cfg),
		})
		if err != nil {
			return nil, fmt.Errorf("configure Claude orchestrator run workspaces: %w", err)
		}
		workspaces = provisioner
	}
	wiring.service = claudeorchestrator.New(claudeorchestrator.Dependencies{
		Model: model, Worker: worker, Validator: validator, Memory: memory,
		DefaultWorktreePath: cfg.ClaudeOrchestrator.Worker.WorktreePath,
		Workspaces:          workspaces,
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
