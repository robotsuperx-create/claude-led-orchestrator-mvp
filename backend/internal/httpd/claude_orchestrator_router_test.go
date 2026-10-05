package httpd

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClaudeOrchestratorRouterWiring(t *testing.T) {
	const path = "/internal/claude-orchestrator/runs"
	const optedInBody = `{"task":"work","explicitOptIn":true}`

	t.Run("route is absent without an injected service", func(t *testing.T) {
		r := newClaudeOrchestratorRouterWithDeps(config.Config{}, APIDeps{})
		rec := serveClaudeOrchestratorRequest(r, http.MethodPost, path, optedInBody, "127.0.0.1", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	})

	t.Run("feature flag defaults off", func(t *testing.T) {
		fake := newClaudeOrchestratorAPIFake()
		r := newClaudeOrchestratorRouterWithDeps(config.Config{}, APIDeps{ClaudeOrchestrator: fake})
		rec := serveClaudeOrchestratorRequest(r, http.MethodPost, path, optedInBody, "127.0.0.1", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want unregistered route (%d): %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
		if fake.callCount() != 0 {
			t.Fatalf("orchestrator calls = %d, want 0 while feature flag is off", fake.callCount())
		}
	})

	t.Run("feature flag still requires local caller and explicit per-run opt-in", func(t *testing.T) {
		fake := newClaudeOrchestratorAPIFake()
		cfg := config.Config{ClaudeOrchestrator: config.ClaudeOrchestratorConfig{FeatureEnabled: true}}
		r := newClaudeOrchestratorRouterWithDeps(cfg, APIDeps{ClaudeOrchestrator: fake})

		for _, tc := range []struct {
			name   string
			body   string
			host   string
			origin string
		}{
			{name: "opt-in missing", body: `{"task":"work"}`, host: "127.0.0.1"},
			{name: "remote host", body: optedInBody, host: "remote.example"},
			{name: "browser origin", body: optedInBody, host: "127.0.0.1", origin: "https://example.com"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				rec := serveClaudeOrchestratorRequest(r, http.MethodPost, path, tc.body, tc.host, tc.origin)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusForbidden, rec.Body.String())
				}
			})
		}
		if fake.callCount() != 0 {
			t.Fatalf("orchestrator calls = %d, want 0 for denied requests", fake.callCount())
		}

		fake.result = ports.OrchestrationResult{State: ports.RunStateCompleted}
		started := serveClaudeOrchestratorRequest(r, http.MethodPost, path, optedInBody, "127.0.0.1", "")
		if started.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want %d: %s", started.Code, http.StatusAccepted, started.Body.String())
		}
		select {
		case <-fake.done:
		case <-time.After(time.Second):
			t.Fatal("orchestrator was not started after both gates passed")
		}
	})
}

func newClaudeOrchestratorRouterWithDeps(cfg config.Config, deps APIDeps) chi.Router {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRouterWithControl(cfg, log, nil, deps, ControlDeps{})
}

func TestClaudeOrchestratorInternalRoutesRemainOutsideOpenAPIPathSpace(t *testing.T) {
	fake := newClaudeOrchestratorAPIFake()
	cfg := config.Config{ClaudeOrchestrator: config.ClaudeOrchestratorConfig{FeatureEnabled: true}}
	r := newClaudeOrchestratorRouterWithDeps(cfg, APIDeps{ClaudeOrchestrator: fake})
	found := false
	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/internal/claude-orchestrator/") {
			found = true
			if strings.HasPrefix(route, "/api/v1/") {
				t.Errorf("internal route %s unexpectedly entered the OpenAPI route namespace", route)
			}
			if method != http.MethodGet && method != http.MethodPost {
				t.Errorf("unexpected method %s on internal orchestrator route %s", method, route)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if !found {
		t.Fatal("Claude orchestrator internal routes were not registered")
	}
}
