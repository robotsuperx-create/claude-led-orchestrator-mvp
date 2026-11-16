package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/media"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/x00"
)

// mediaMaxBodyBytes fits a request's text limits in UTF-8 with JSON overhead.
const mediaMaxBodyBytes = 32 * 1024

// MediaService is the media gateway: the person's GPU server and the library.
type MediaService interface {
	Info() media.Info
	Status(ctx context.Context) media.ServerStatus
	List() ([]media.Item, error)
	Get(id string) (media.Item, error)
	Create(request media.Request) (media.Item, error)
	Upload(name string, data []byte) (media.Item, error)
	Delete(id string) error
}

// MediaAPI serves the local-only media routes. Like every /internal route
// they refuse a request that carries an Origin header or comes from off the
// machine: only the desktop app's main process calls them.
type MediaAPI struct {
	Service MediaService
}

func mountMedia(r chi.Router, service MediaService) {
	if service == nil {
		return
	}
	api := &MediaAPI{Service: service}
	r.Get("/internal/media", api.info)
	r.Get("/internal/media/status", api.status)
	r.Get("/internal/media/items", api.list)
	r.Post("/internal/media/items", api.create)
	r.Post("/internal/media/uploads", api.upload)
	r.Get("/internal/media/items/{itemId}", api.get)
	r.Delete("/internal/media/items/{itemId}", api.remove)
}

func (api *MediaAPI) local(w http.ResponseWriter, r *http.Request) bool {
	if localControlRequest(r) {
		return true
	}
	writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
	return false
}

func (api *MediaAPI) info(w http.ResponseWriter, r *http.Request) {
	if !api.local(w, r) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, api.Service.Info())
}

func (api *MediaAPI) status(w http.ResponseWriter, r *http.Request) {
	if !api.local(w, r) {
		return
	}
	envelope.WriteJSON(w, http.StatusOK, api.Service.Status(r.Context()))
}

func (api *MediaAPI) list(w http.ResponseWriter, r *http.Request) {
	if !api.local(w, r) {
		return
	}
	items, err := api.Service.List()
	if err != nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusInternalServerError, "MEDIA_LIBRARY_UNREADABLE", "The media library could not be read")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (api *MediaAPI) get(w http.ResponseWriter, r *http.Request) {
	if !api.local(w, r) {
		return
	}
	item, err := api.Service.Get(chi.URLParam(r, "itemId"))
	if err != nil {
		writeMediaError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, item)
}

func (api *MediaAPI) create(w http.ResponseWriter, r *http.Request) {
	if !api.local(w, r) {
		return
	}
	var request media.Request
	decoder := json.NewDecoder(io.LimitReader(r.Body, mediaMaxBodyBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The media request is not valid")
		return
	}
	item, err := api.Service.Create(request)
	if err != nil {
		writeMediaError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, item)
}

// upload takes a picture as the raw body; its name is the "name" query.
func (api *MediaAPI) upload(w http.ResponseWriter, r *http.Request) {
	if !api.local(w, r) {
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, media.MaxUploadBytes+1))
	if err != nil || len(data) > media.MaxUploadBytes {
		writeClaudeOrchestratorAPIError(w, r, http.StatusRequestEntityTooLarge, "MEDIA_TOO_LARGE", "The picture is too large")
		return
	}
	name, _ := url.QueryUnescape(r.URL.Query().Get("name"))
	item, err := api.Service.Upload(name, data)
	if err != nil {
		writeMediaError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, item)
}

func (api *MediaAPI) remove(w http.ResponseWriter, r *http.Request) {
	if !api.local(w, r) {
		return
	}
	if err := api.Service.Delete(chi.URLParam(r, "itemId")); err != nil {
		writeMediaError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// withMedia tells X-00 whether he can propose pictures, videos and sound:
// only when the GPU server is set. The fleet stays nil when runs are off and
// there is no gateway, as before.
func withMedia(fleet func() x00.Fleet, service MediaService) func() x00.Fleet {
	if service == nil {
		return fleet
	}
	return func() x00.Fleet {
		state := x00.Fleet{Runs: []x00.RunBrief{}}
		if fleet != nil {
			state = fleet()
		}
		info := service.Info()
		state.MediaEnabled = info.Enabled
		for _, capability := range info.Capabilities {
			if capability.Input == "" {
				state.MediaKinds = appendKind(state.MediaKinds, capability.Kind)
			}
		}
		return state
	}
}

func appendKind(kinds []string, kind string) []string {
	for _, have := range kinds {
		if have == kind {
			return kinds
		}
	}
	return append(kinds, kind)
}

func writeMediaError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, media.ErrNotConfigured):
		writeClaudeOrchestratorAPIError(w, r, http.StatusServiceUnavailable, "MEDIA_NO_SERVER", "Set the GPU server in Mission control settings")
	case errors.Is(err, media.ErrInvalidRequest):
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The media request is not valid")
	case errors.Is(err, media.ErrUnknownCapability):
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "MEDIA_UNKNOWN_CAPABILITY", "No workflow has that name")
	case errors.Is(err, media.ErrBusy):
		writeClaudeOrchestratorAPIError(w, r, http.StatusTooManyRequests, "MEDIA_BUSY", "The media queue is full")
	case errors.Is(err, media.ErrNotFound):
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "MEDIA_NOT_FOUND", "No such media item")
	default:
		writeClaudeOrchestratorAPIError(w, r, http.StatusInternalServerError, "MEDIA_FAILED", "The media library could not be changed")
	}
}
