package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func writeOrchestratorSettings(t *testing.T, dataDir, content string) {
	t.Helper()
	path := ClaudeOrchestratorSettingsPath(dataDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadReadsMissionControlSettingsSavedByTheApp(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	writeOrchestratorSettings(t, dataDir, `{"version":1,"values":{
		"AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED":"on",
		"AO_CLAUDE_ORCHESTRATOR_CLAUDE_BASE_URL":"https://api.anthropic.com/v1",
		"AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL":"from-file",
		"AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY":"file-key",
		"AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT":"/work/app",
		"AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS":"[{\"name\":\"test\",\"argv\":[\"go\",\"test\",\"./...\"]}]",
		"AO_DATA_DIR":"/elsewhere",
		"AO_PORT":"1"
	}}`)
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL", "from-env")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	orchestrator := cfg.ClaudeOrchestrator
	if !orchestrator.FeatureEnabled || !orchestrator.FromSettingsFile || orchestrator.ConfigError != "" {
		t.Fatalf("orchestrator = %+v", orchestrator)
	}
	if orchestrator.ClaudeProvider.DefaultModel != "from-env" {
		t.Fatalf("model = %q, want the environment to win over the file", orchestrator.ClaudeProvider.DefaultModel)
	}
	if orchestrator.ClaudeProvider.APIKey != "file-key" || orchestrator.Worker.ProjectRoot != "/work/app" || len(orchestrator.Worker.Commands) != 1 {
		t.Fatalf("file values not applied: %+v", orchestrator.Worker)
	}
	if orchestrator.Worker.Timeout != DefaultClaudeOrchestratorWorkerTimeout || orchestrator.Validator.Timeout != 10*time.Minute {
		t.Fatalf("timeouts = %s / %s, want defaults", orchestrator.Worker.Timeout, orchestrator.Validator.Timeout)
	}
	if cfg.DataDir != dataDir || cfg.Port == 1 {
		t.Fatalf("keys outside the orchestrator namespace must be ignored: dataDir=%q port=%d", cfg.DataDir, cfg.Port)
	}
	if orchestrator.ClaudeProvider.Provider != ports.ModelProviderClaude {
		t.Fatalf("provider = %q", orchestrator.ClaudeProvider.Provider)
	}
}

func TestBadSavedSettingsTurnTheOrchestratorOffInsteadOfStoppingTheDaemon(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("AO_DATA_DIR", dataDir)
	writeOrchestratorSettings(t, dataDir, `{"version":1,"values":{
		"AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED":"on",
		"AO_CLAUDE_ORCHESTRATOR_WORKER_TIMEOUT":"soon",
		"AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY":"sk-secret-value"
	}}`)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load = %v, want the daemon to start", err)
	}
	orchestrator := cfg.ClaudeOrchestrator
	if orchestrator.FeatureEnabled || !strings.Contains(orchestrator.ConfigError, "AO_CLAUDE_ORCHESTRATOR_WORKER_TIMEOUT") {
		t.Fatalf("orchestrator = %+v, want off with a reason naming the setting", orchestrator)
	}
	if strings.Contains(orchestrator.ConfigError, "soon") || strings.Contains(orchestrator.ConfigError, "secret") {
		t.Fatalf("reason leaked a value: %q", orchestrator.ConfigError)
	}

	writeOrchestratorSettings(t, dataDir, `not json`)
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED", "")
	if cfg, err = Load(); err != nil || cfg.ClaudeOrchestrator.FeatureEnabled || cfg.ClaudeOrchestrator.ConfigError == "" {
		t.Fatalf("corrupt file: cfg=%+v err=%v", cfg.ClaudeOrchestrator, err)
	}

	// The same mistake made in the environment still stops the daemon.
	if err := os.Remove(ClaudeOrchestratorSettingsPath(dataDir)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED", "on")
	t.Setenv("AO_CLAUDE_ORCHESTRATOR_WORKER_TIMEOUT", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("an invalid environment setting must still fail fast")
	}
}
