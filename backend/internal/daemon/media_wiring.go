package daemon

import (
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/media"
)

// newMedia opens the media gateway. The library is always there, so what was
// made stays visible; the GPU server is used only when its settings are valid.
func newMedia(cfg config.MediaConfig, log *slog.Logger) *media.Service {
	gateway := media.Config{LibraryDir: cfg.LibraryDir, Reason: cfg.Reason, Logger: log}
	if cfg.GPU != nil {
		gateway.BaseURL = cfg.GPU.BaseURL
		gateway.Token = cfg.GPU.Token
		gateway.Voice = cfg.GPU.Voice
		gateway.PricePerHour = cfg.GPU.PricePerHour
		if cfg.GPU.RunPodPodID != "" {
			gateway.Power = &media.RunPod{PodID: cfg.GPU.RunPodPodID, APIKey: cfg.GPU.RunPodAPIKey}
			gateway.IdleMinutes = cfg.GPU.IdleMinutes
		}
	}
	return media.New(gateway)
}
