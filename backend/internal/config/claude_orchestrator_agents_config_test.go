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
