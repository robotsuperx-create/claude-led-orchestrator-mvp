package config

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	if orchestrator.Budget.MaxCostUSD != DefaultClaudeOrchestratorRunCostBudgetUSD {
		t.Fatalf("a run with no cost budget set = %v, want the default cap", orchestrator.Budget.MaxCostUSD)
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

func TestLoadClaudeOrchestratorClaudeOnly(t *testing.T) {
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED", "on")
	cfg, err := Load()
	if err != nil || cfg.ClaudeOrchestrator.ClaudeOnly {
		t.Fatalf("ClaudeOnly = %v, %v; want off by default", cfg.ClaudeOrchestrator.ClaudeOnly, err)
	}

	// Whatever the other choices say, the one Claude key runs everything.
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_ONLY", "on")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_PLANNER_PROVIDER", "local")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_WORKER_DEFAULT_PROVIDER", "deepseek")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_WORKER_MODE", "agents")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	orchestrator := cfg.ClaudeOrchestrator
	if !orchestrator.ClaudeOnly || orchestrator.PlannerProvider != ports.ModelProviderClaude ||
		orchestrator.Worker.DefaultProvider != ports.ModelProviderClaude ||
		orchestrator.Worker.Mode != ClaudeOrchestratorWorkerModeModel {
		t.Fatalf("Claude-only config = only %v planner %q writer %q mode %q", orchestrator.ClaudeOnly,
			orchestrator.PlannerProvider, orchestrator.Worker.DefaultProvider, orchestrator.Worker.Mode)
	}
	// Only the key is needed: the address and model have defaults.
	if orchestrator.ClaudeProvider.BaseURL != DefaultCommanderBaseURL || orchestrator.ClaudeProvider.DefaultModel != DefaultClaudeOrchestratorClaudeModel {
		t.Fatalf("Claude provider = %v, want the default address and model", orchestrator.ClaudeProvider)
	}
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL", "claude-sonnet-5-5")
	if cfg, err = Load(); err != nil || cfg.ClaudeOrchestrator.ClaudeProvider.DefaultModel != "claude-sonnet-5-5" {
		t.Fatalf("a chosen model was not kept: %v, %v", cfg.ClaudeOrchestrator.ClaudeProvider, err)
	}

	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_ONLY", "sometimes")
	if _, err := Load(); err == nil {
		t.Fatal("an invalid Claude-only toggle was accepted")
	}
}

func TestLoadClaudeOrchestratorVisualReview(t *testing.T) {
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED", "on")
	cfg, err := Load()
	if err != nil || cfg.ClaudeOrchestrator.Visual.Enabled {
		t.Fatalf("visual = %+v, %v; want off without the one Claude key", cfg.ClaudeOrchestrator.Visual, err)
	}
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_ONLY", "on")
	if cfg, err = Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	visual := cfg.ClaudeOrchestrator.Visual
	if !visual.Enabled || visual.Rounds != DefaultClaudeOrchestratorVisualRounds || visual.Target != DefaultClaudeOrchestratorVisualTarget || visual.Browser != "" || len(visual.Pages) != 0 {
		t.Fatalf("Claude-only visual defaults = %+v", visual)
	}

	browser := filepath.Join(t.TempDir(), "chrome")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_VISUAL_ROUNDS", "4")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_VISUAL_TARGET", "8.5")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_VISUAL_BROWSER", browser)
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_VISUAL_PAGES", " index.html, shop/cart.html ,")
	if cfg, err = Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	visual = cfg.ClaudeOrchestrator.Visual
	if visual.Rounds != 4 || visual.Target != 8.5 || visual.Browser != browser || strings.Join(visual.Pages, "|") != "index.html|shop/cart.html" {
		t.Fatalf("visual = %+v", visual)
	}
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_VISUAL_REVIEW", "off")
	if cfg, err = Load(); err != nil || cfg.ClaudeOrchestrator.Visual.Enabled {
		t.Fatalf("visual review could not be turned off: %+v, %v", cfg.ClaudeOrchestrator.Visual, err)
	}

	for name, env := range map[string][2]string{
		"bad toggle":         {"AO_CLAUDE_ORCHESTRATOR_VISUAL_REVIEW", "maybe"},
		"too many rounds":    {"AO_CLAUDE_ORCHESTRATOR_VISUAL_ROUNDS", "9"},
		"score out of range": {"AO_CLAUDE_ORCHESTRATOR_VISUAL_TARGET", "11"},
		"relative browser":   {"AO_CLAUDE_ORCHESTRATOR_VISUAL_BROWSER", "chrome"},
		"page outside":       {"AO_CLAUDE_ORCHESTRATOR_VISUAL_PAGES", "../other/index.html"},
		"too many pages":     {"AO_CLAUDE_ORCHESTRATOR_VISUAL_PAGES", "a.html,b.html,c.html,d.html,e.html,f.html"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(env[0], env[1])
			if _, err := Load(); err == nil {
				t.Fatalf("Load accepted %s=%s", env[0], env[1])
			}
		})
	}
}

func TestLoadClaudeOrchestratorClaudeThinkingRoomAndWriterModel(t *testing.T) {
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED", "on")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	claude := cfg.ClaudeOrchestrator.ClaudeProvider
	// Claude always thinks first, so it gets room and time for that.
	if claude.MaxTokens != DefaultClaudeOrchestratorClaudeMaxTokens || claude.Timeout != DefaultClaudeOrchestratorClaudeTimeout {
		t.Fatalf("Claude defaults = %d tokens, %s; want room to think", claude.MaxTokens, claude.Timeout)
	}
	if cfg.ClaudeOrchestrator.ClaudeWriterModel != "" {
		t.Fatalf("writer model = %q, want none by default", cfg.ClaudeOrchestrator.ClaudeWriterModel)
	}

	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_MAX_TOKENS", "8000")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_TIMEOUT", "4m")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL", "claude-fable-5-2")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_WRITER_MODEL", " claude-sonnet-5-5 ")
	if cfg, err = Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	orchestrator := cfg.ClaudeOrchestrator
	if orchestrator.ClaudeProvider.MaxTokens != 8000 || orchestrator.ClaudeProvider.Timeout != 4*time.Minute {
		t.Fatalf("chosen limits were not kept: %v", orchestrator.ClaudeProvider)
	}
	if orchestrator.ClaudeProvider.DefaultModel != "claude-fable-5-2" || orchestrator.ClaudeWriterModel != "claude-sonnet-5-5" {
		t.Fatalf("models = planner %q writer %q", orchestrator.ClaudeProvider.DefaultModel, orchestrator.ClaudeWriterModel)
	}

	// A saved Claude key is enough: the address and model default.
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL", "")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY", "test-only-key")
	if cfg, err = Load(); err != nil || cfg.ClaudeOrchestrator.ClaudeProvider.BaseURL != DefaultCommanderBaseURL ||
		cfg.ClaudeOrchestrator.ClaudeProvider.DefaultModel != DefaultClaudeOrchestratorClaudeModel {
		t.Fatalf("Claude with a key = %v, %v; want the default address and model", cfg.ClaudeOrchestrator.ClaudeProvider, err)
	}

	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_WRITER_MODEL", "two words")
	if _, err := Load(); err == nil {
		t.Fatal("an invalid writer model was accepted")
	}
}
