package config

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLoadClaudeOrchestratorWorkerMode(t *testing.T) {
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED", "on")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	worker := cfg.ClaudeOrchestrator.Worker
	if worker.Mode != ClaudeOrchestratorWorkerModeModel {
		t.Fatalf("Mode = %q, want model by default", worker.Mode)
	}
	if worker.AgentHarnesses[ports.ModelProviderClaude] != domain.HarnessClaudeCode ||
		worker.AgentHarnesses[ports.ModelProviderDeepSeek] != domain.HarnessDeepSeek {
		t.Fatalf("default harnesses = %v", worker.AgentHarnesses)
	}

	t.Setenv("AO_CLAUDE_ORCHESTRATOR_WORKER_MODE", "Agents")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_AGENT_HARNESS_DEEPSEEK", "kimi")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_AGENT_PROJECT_ID", " proj-1 ")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	worker = cfg.ClaudeOrchestrator.Worker
	if worker.Mode != ClaudeOrchestratorWorkerModeAgents || worker.AgentProjectID != "proj-1" ||
		worker.AgentHarnesses[ports.ModelProviderDeepSeek] != domain.HarnessKimi {
		t.Fatalf("worker = %+v", worker)
	}

	for name, env := range map[string][2]string{
		"unknown mode":    {"AO_CLAUDE_ORCHESTRATOR_WORKER_MODE", "swarm"},
		"unknown harness": {"AO_CLAUDE_ORCHESTRATOR_AGENT_HARNESS_CLAUDE", "not-a-harness"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(env[0], env[1])
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted %s=%s", env[0], env[1])
			}
		})
	}
}

func TestLoadClaudeOrchestratorParallelSubtasks(t *testing.T) {
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED", "on")
	cfg, err := Load()
	if err != nil || cfg.ClaudeOrchestrator.Worker.MaxParallelSubtasks != DefaultClaudeOrchestratorMaxParallelSubtasks {
		t.Fatalf("default parallel = %d, %v", cfg.ClaudeOrchestrator.Worker.MaxParallelSubtasks, err)
	}
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_MAX_PARALLEL_SUBTASKS", "5")
	if cfg, err = Load(); err != nil || cfg.ClaudeOrchestrator.Worker.MaxParallelSubtasks != 5 {
		t.Fatalf("parallel = %d, %v", cfg.ClaudeOrchestrator.Worker.MaxParallelSubtasks, err)
	}
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_MAX_PARALLEL_SUBTASKS", "99")
	if _, err := Load(); err == nil {
		t.Fatal("an excessive parallel limit was accepted")
	}
}

func TestLoadClaudeOrchestratorBudgetLocalModelAndReplans(t *testing.T) {
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED", "on")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	orchestrator := cfg.ClaudeOrchestrator
	if orchestrator.Budget.MaxTokens != DefaultClaudeOrchestratorRunTokenBudget || orchestrator.MaxReplans != DefaultClaudeOrchestratorMaxReplans || orchestrator.PlannerProvider != ports.ModelProviderClaude {
		t.Fatalf("defaults = budget %d replans %d planner %q", orchestrator.Budget.MaxTokens, orchestrator.MaxReplans, orchestrator.PlannerProvider)
	}
	if price, ok := orchestrator.Budget.Prices[ports.ModelProviderLocal]; !ok || price.InputUSDPerMTok != 0 {
		t.Fatalf("a local model should count as free by default: %+v", orchestrator.Budget.Prices)
	}

	t.Setenv("AO_CLAUDE_ORCHESTRATOR_RUN_TOKEN_BUDGET", "off")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_RUN_COST_BUDGET_USD", "2.5")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_PRICE_INPUT_PER_MTOK", "3")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_PRICE_OUTPUT_PER_MTOK", "15")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_MAX_REPLANS", "0")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_PLANNER_PROVIDER", "Local")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_LOCAL_BASE_URL", "http://127.0.0.1:11434/v1")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_LOCAL_MODEL", "qwen2.5-coder:14b")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_WORKER_DEFAULT_PROVIDER", "local")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_PUBLISH_REMOTE", "upstream")
	if cfg, err = Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	orchestrator = cfg.ClaudeOrchestrator
	if orchestrator.Budget.MaxTokens != 0 || orchestrator.Budget.MaxCostUSD != 2.5 || orchestrator.MaxReplans != 0 ||
		orchestrator.PlannerProvider != ports.ModelProviderLocal || orchestrator.LocalProvider.DefaultModel != "qwen2.5-coder:14b" ||
		orchestrator.Worker.DefaultProvider != ports.ModelProviderLocal || orchestrator.PublishRemote != "upstream" {
		t.Fatalf("config = %+v", orchestrator)
	}
	if price := orchestrator.Budget.Prices[ports.ModelProviderClaude]; price.InputUSDPerMTok != 3 || price.OutputUSDPerMTok != 15 {
		t.Fatalf("claude price = %+v", price)
	}

	for name, env := range map[string][2]string{
		"plain http to another machine": {"AO_CLAUDE_ORCHESTRATOR_LOCAL_BASE_URL", "http://192.168.1.20:11434/v1"},
		"negative budget":               {"AO_CLAUDE_ORCHESTRATOR_RUN_TOKEN_BUDGET", "-5"},
		"too many replans":              {"AO_CLAUDE_ORCHESTRATOR_MAX_REPLANS", "9"},
		"unknown planner":               {"AO_CLAUDE_ORCHESTRATOR_PLANNER_PROVIDER", "deepseek"},
		"bad price":                     {"AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_PRICE_INPUT_PER_MTOK", "cheap"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(env[0], env[1])
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted %s=%s", env[0], env[1])
			}
		})
	}
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_LOCAL_BASE_URL", "https://models.example.com/v1")
	if _, err := Load(); err != nil {
		t.Fatalf("an https endpoint elsewhere should be allowed: %v", err)
	}
}
