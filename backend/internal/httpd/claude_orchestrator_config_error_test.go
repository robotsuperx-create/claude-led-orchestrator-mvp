package httpd

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

func TestClaudeOrchestratorReportsWhySavedSettingsTurnedItOff(t *testing.T) {
	r := chi.NewRouter()
	mountClaudeOrchestrator(r, nil, nil, nil, config.ClaudeOrchestratorConfig{
		FromSettingsFile: true,
		ConfigError:      "claude orchestrator is enabled but required configuration is missing: AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS",
	}, slog.Default())

	rec := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator", "", "127.0.0.1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var info ClaudeOrchestratorInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Enabled || info.ConfigError == "" {
		t.Fatalf("info = %+v, want disabled with a reason", info)
	}
	if rec := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator", "", "203.0.113.4", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("remote status = %d, want 403", rec.Code)
	}
	if rec := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs", "", "127.0.0.1", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("runs status = %d, want no run routes while off", rec.Code)
	}

	off := chi.NewRouter()
	mountClaudeOrchestrator(off, nil, nil, nil, config.ClaudeOrchestratorConfig{}, slog.Default())
	if rec := serveClaudeOrchestratorRequest(off, http.MethodGet, "/internal/claude-orchestrator", "", "127.0.0.1", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("plain off status = %d, want 404", rec.Code)
	}
}
