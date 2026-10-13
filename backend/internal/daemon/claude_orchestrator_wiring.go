package daemon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/claudeorchestrator"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
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

// claudeOrchestratorHistoryPath is where run history persists, under ~/.ao.
func claudeOrchestratorHistoryPath(cfg config.Config) string {
	return filepath.Join(cfg.DataDir, "claude-orchestrator", "runs.json")
}

// claudeOrchestratorWiring is the daemon composition boundary. In the default
// off state it deliberately owns no service or network client.
type claudeOrchestratorWiring struct {
	service   *claudeorchestrator.Service
	gate      ports.ClaudeOrchestratorRunGate
	providers *modelgateway.Registry
	// agents is set in agents mode and bound to the AO session service once
	// the daemon has built it.
	agents *claudeorchestrator.AgentSessions
	// publisher pushes a run branch for review when a person asks.
	publisher *claudeorchestrator.RunPublisher
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
	required := map[ports.ModelProvider]bool{plannerProvider(cfg): true}
	if cfg.Worker.Mode != config.ClaudeOrchestratorWorkerModeAgents {
		// In agents mode each agent harness brings its own credentials.
		required[workerProvider(cfg)] = true
	}
	for _, provider := range []ports.ModelProvider{ports.ModelProviderClaude, ports.ModelProviderDeepSeek, ports.ModelProviderLocal} {
		if required[provider] {
			checkProvider(providerEnvPrefix(provider), providerConfig(cfg, provider), string(provider))
		}
	}
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
	agentsMode := cfg.ClaudeOrchestrator.Worker.Mode == config.ClaudeOrchestratorWorkerModeAgents
	if model == nil {
		// Register every provider that is configured; validation above made
		// sure the planner and, in model mode, the default worker are.
		var providers []modelgateway.ProviderConfig
		for _, provider := range []ports.ModelProvider{ports.ModelProviderClaude, ports.ModelProviderDeepSeek, ports.ModelProviderLocal} {
			if candidate := providerConfig(cfg.ClaudeOrchestrator, provider); strings.TrimSpace(candidate.BaseURL) != "" {
				providers = append(providers, candidate)
			}
		}
		registry, err := modelgateway.NewRegistry(providers...)
		if err != nil {
			return nil, fmt.Errorf("configure Claude orchestrator model providers: %w", err)
		}
		planner, err := registry.Provider(plannerProvider(cfg.ClaudeOrchestrator))
		if err != nil {
			return nil, errors.New("configure Claude orchestrator planner provider")
		}
		if _, err := registry.Provider(workerProvider(cfg.ClaudeOrchestrator)); err != nil && !agentsMode {
			return nil, errors.New("configure Claude orchestrator worker provider")
		}
		wiring.providers = registry
		model = planner
		if author == nil && !agentsMode {
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
		var agents ports.AgentImplementer
		if agentsMode {
			sessions, err := claudeorchestrator.NewAgentSessions(worktreeRunner, claudeorchestrator.AgentSessionsConfig{
				Harnesses:       cfg.ClaudeOrchestrator.Worker.AgentHarnesses,
				DefaultProvider: cfg.ClaudeOrchestrator.Worker.DefaultProvider,
			})
			if err != nil {
				return nil, fmt.Errorf("configure Claude orchestrator agent sessions: %w", err)
			}
			wiring.agents = sessions
			agents = sessions
			author = nil
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
			Agents: agents,
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
		publisher, err := claudeorchestrator.NewRunPublisher(gitRunner, cfg.ClaudeOrchestrator.Worker.ProjectRoot, cfg.ClaudeOrchestrator.PublishRemote)
		if err != nil {
			return nil, fmt.Errorf("configure Claude orchestrator run publishing: %w", err)
		}
		wiring.publisher = publisher
	}
	wiring.service = claudeorchestrator.New(claudeorchestrator.Dependencies{
		Model: model, Worker: worker, Validator: validator, Memory: memory,
		DefaultWorktreePath: cfg.ClaudeOrchestrator.Worker.WorktreePath,
		Workspaces:          workspaces,
		MaxParallelSubtasks: maxParallelSubtasks(cfg.ClaudeOrchestrator.Worker, agentsMode),
		PlannerProvider:     plannerProvider(cfg.ClaudeOrchestrator),
		Budget:              runBudget(cfg.ClaudeOrchestrator.Budget),
		MaxReplans:          cfg.ClaudeOrchestrator.MaxReplans,
	})
	return wiring, nil
}

func plannerProvider(cfg config.ClaudeOrchestratorConfig) ports.ModelProvider {
	if cfg.PlannerProvider == "" {
		return ports.ModelProviderClaude
	}
	return cfg.PlannerProvider
}

func workerProvider(cfg config.ClaudeOrchestratorConfig) ports.ModelProvider {
	if cfg.Worker.DefaultProvider == "" {
		return ports.ModelProviderDeepSeek
	}
	return cfg.Worker.DefaultProvider
}

func providerEnvPrefix(provider ports.ModelProvider) string {
	return "AO_CLAUDE_ORCHESTRATOR_" + strings.ToUpper(string(provider))
}

func providerConfig(cfg config.ClaudeOrchestratorConfig, provider ports.ModelProvider) modelgateway.ProviderConfig {
	switch provider {
	case ports.ModelProviderClaude:
		return cfg.ClaudeProvider
	case ports.ModelProviderDeepSeek:
		return cfg.DeepSeekProvider
	case ports.ModelProviderLocal:
		return cfg.LocalProvider
	default:
		return modelgateway.ProviderConfig{}
	}
}

func runBudget(budget config.ClaudeOrchestratorBudgetConfig) claudeorchestrator.UsageBudget {
	result := claudeorchestrator.UsageBudget{MaxTokens: budget.MaxTokens, MaxCostUSD: budget.MaxCostUSD}
	if len(budget.Prices) > 0 {
		result.Prices = make(map[ports.ModelProvider]claudeorchestrator.ModelPrice, len(budget.Prices))
		for provider, price := range budget.Prices {
			result.Prices[provider] = claudeorchestrator.ModelPrice{InputUSDPerMTok: price.InputUSDPerMTok, OutputUSDPerMTok: price.OutputUSDPerMTok}
		}
	}
	return result
}

// maxParallelSubtasks allows parallel subtasks only in agents mode, where each
// agent codes in its own worktree; model edits share the run worktree.
func maxParallelSubtasks(worker config.ClaudeOrchestratorWorkerConfig, agentsMode bool) int {
	if !agentsMode || worker.MaxParallelSubtasks < 1 {
		return 1
	}
	return worker.MaxParallelSubtasks
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

// agentProjectLister lists registered AO projects.
type agentProjectLister interface {
	List(ctx context.Context) ([]projectsvc.Summary, error)
}

// bindAgentSessions connects agents mode to the daemon's session, report, and
// project services. It is a no-op unless agents mode is configured.
func (w *claudeOrchestratorWiring) bindAgentSessions(
	cfg config.ClaudeOrchestratorConfig,
	sessions claudeorchestrator.AgentSessionAPI,
	reports claudeorchestrator.AgentReportReader,
	projects agentProjectLister,
) error {
	if w == nil || w.agents == nil {
		return nil
	}
	if projects == nil {
		return errors.New("agent sessions require the project service")
	}
	return w.agents.Bind(sessions, reports, agentProjectResolver(cfg.Worker, projects))
}

// agentProjectResolver finds the AO project for the orchestrator repository.
// It is resolved per spawn, so a project registered after startup works.
func agentProjectResolver(worker config.ClaudeOrchestratorWorkerConfig, projects agentProjectLister) claudeorchestrator.AgentProjectResolver {
	return func(ctx context.Context) (domain.ProjectID, error) {
		list, err := projects.List(ctx)
		if err != nil {
			return "", err
		}
		root := canonicalPath(worker.ProjectRoot)
		for _, project := range list {
			if worker.AgentProjectID != "" && string(project.ID) != worker.AgentProjectID {
				continue
			}
			if project.Kind != domain.ProjectKindSingleRepo || canonicalPath(project.Path) != root {
				continue
			}
			return project.ID, nil
		}
		if worker.AgentProjectID != "" {
			return "", fmt.Errorf("AO project %q is not a registered single-repository project for the orchestrator repository", worker.AgentProjectID)
		}
		return "", errors.New("the orchestrator repository is not an AO project; add it in AO first")
	}
}

func canonicalPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}
