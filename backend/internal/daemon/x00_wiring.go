package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/x00"
)

// x00Chatter adapts a Claude provider to X-00's narrow conversation contract.
type x00Chatter struct {
	provider *modelgateway.ProviderAdapter
}

func (c x00Chatter) Converse(ctx context.Context, messages []x00.Message) (string, error) {
	chat := make([]modelgateway.ChatMessage, len(messages))
	for i, message := range messages {
		chat[i] = modelgateway.ChatMessage{Role: message.Role, Content: message.Content}
	}
	return c.provider.Converse(ctx, chat, 0)
}

// newX00 builds X-00 when a Claude connection is configured. A problem only
// leaves X-00 silent; it never stops the daemon.
func newX00(cfg config.CommanderConfig, log *slog.Logger) httpd.X00Service {
	if !cfg.Enabled {
		return nil
	}
	commander, err := buildX00(cfg)
	if err != nil {
		log.Warn("X-00 is unavailable", "error", err)
		return nil
	}
	return commander
}

func buildX00(cfg config.CommanderConfig) (*x00.Commander, error) {
	registry, err := modelgateway.NewRegistry(cfg.Provider)
	if err != nil {
		return nil, fmt.Errorf("configure X-00's Claude connection: %w", err)
	}
	provider, err := registry.Provider(cfg.Provider.Provider)
	if err != nil {
		return nil, fmt.Errorf("configure X-00's Claude connection: %w", err)
	}
	return x00.New(x00Chatter{provider: provider})
}
