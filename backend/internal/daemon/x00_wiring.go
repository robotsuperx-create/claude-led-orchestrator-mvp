package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/x00"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/x00voice"
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

// newX00Voice gives X-00 his ears and voice: the cloud speech service first
// when its key is saved, then the local engines under the speech dir, which
// are looked for on every request so installing them needs no restart.
func newX00Voice(cfg config.X00VoiceConfig) *x00voice.Voice {
	var ears []x00voice.Ear
	var mouths []x00voice.Mouth
	if cfg.Cloud != nil {
		cloud := x00voice.CloudConfig{
			BaseURL:         cfg.Cloud.BaseURL,
			APIKey:          cfg.Cloud.APIKey,
			TranscribeModel: cfg.Cloud.TranscribeModel,
			SpeechModel:     cfg.Cloud.SpeechModel,
			Voice:           cfg.Cloud.Voice,
		}
		client := x00voice.NewCloudClient()
		ears = append(ears, &x00voice.CloudEar{Config: cloud, Client: client})
		mouths = append(mouths, &x00voice.CloudMouth{Config: cloud, Client: client})
	}
	ears = append(ears, &x00voice.LocalEar{Dir: cfg.SpeechDir})
	mouths = append(mouths, &x00voice.LocalMouth{Dir: cfg.SpeechDir})
	return x00voice.New(ears, mouths)
}
