package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/x00"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/x00voice"
)

const (
	// x00MaxBodyBytes fits the conversation limits in UTF-8 with JSON overhead.
	x00MaxBodyBytes = 160 * 1024
	// x00ReplyTimeout bounds one answer; the provider's own timeout is shorter.
	x00ReplyTimeout = 70 * time.Second
	// x00MaxInFlight keeps one person from queueing many paid calls.
	x00MaxInFlight = 2
	// x00ListenTimeout leaves room for a local model on a slow CPU.
	x00ListenTimeout = 90 * time.Second
	// x00SpeakTimeout bounds one spoken line.
	x00SpeakTimeout = 60 * time.Second
	// x00SpeakMaxBodyBytes fits MaxSpeakRunes of UTF-8 with JSON overhead.
	x00SpeakMaxBodyBytes = 16 * 1024
)

// X00Voice is how X-00 hears and speaks.
type X00Voice interface {
	Status() x00voice.Status
	Listen(ctx context.Context, wav []byte, lang string) (string, error)
	Speak(ctx context.Context, text, lang string) (x00voice.Audio, error)
}

// X00Service answers the person as X-00.
type X00Service interface {
	Reply(context.Context, x00.Request) (x00.Reply, error)
}

// X00Info tells the app whether X-00 can talk and, if not, why. It holds no
// URLs or credentials.
type X00Info struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model,omitempty"`
	Reason  string `json:"reason,omitempty"`
	// Listen and Speak name the engine that hears and speaks: "cloud",
	// "local", or empty when none is installed.
	Listen string `json:"listen,omitempty"`
	Speak  string `json:"speak,omitempty"`
}

// X00API serves the local-only X-00 routes. Like the orchestrator routes they
// refuse any request carrying an Origin header or arriving from off-machine.
type X00API struct {
	Service X00Service
	Info    X00Info
	// Fleet returns the state of the person's runs; nil means runs are off.
	Fleet func() x00.Fleet
	// Voice hears and speaks; nil means only the system voice is available.
	Voice         X00Voice
	inflight      chan struct{}
	voiceInflight chan struct{}
}

// Register mounts the X-00 routes.
func (api *X00API) Register(r chi.Router) {
	api.inflight = make(chan struct{}, x00MaxInFlight)
	api.voiceInflight = make(chan struct{}, x00MaxInFlight)
	r.Get("/internal/x00", api.info)
	r.Post("/internal/x00/chat", api.chat)
	r.Post("/internal/x00/listen", api.listen)
	r.Post("/internal/x00/speak", api.speak)
}

type x00ChatRequest struct {
	Turns  []x00.Turn `json:"turns"`
	Locale string     `json:"locale,omitempty"`
	Screen string     `json:"screen,omitempty"`
}

func (api *X00API) info(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	info := api.Info
	if api.Voice != nil {
		status := api.Voice.Status()
		info.Listen, info.Speak = status.Listen, status.Speak
	}
	envelope.WriteJSON(w, http.StatusOK, info)
}

func (api *X00API) chat(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	if api.Service == nil || !api.Info.Enabled {
		writeClaudeOrchestratorAPIError(w, r, http.StatusServiceUnavailable, "X00_DISABLED", "X-00 needs a Claude key in Mission control settings")
		return
	}
	var request x00ChatRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, x00MaxBodyBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The conversation is not valid")
		return
	}
	select {
	case api.inflight <- struct{}{}:
		defer func() { <-api.inflight }()
	default:
		writeClaudeOrchestratorAPIError(w, r, http.StatusTooManyRequests, "X00_BUSY", "X-00 is still answering")
		return
	}
	fleet := x00.Fleet{Runs: []x00.RunBrief{}}
	if api.Fleet != nil {
		fleet = api.Fleet()
	}
	ctx, cancel := context.WithTimeout(r.Context(), x00ReplyTimeout)
	defer cancel()
	reply, err := api.Service.Reply(ctx, x00.Request{Turns: request.Turns, Locale: request.Locale, Screen: request.Screen, Fleet: fleet})
	switch {
	case err == nil:
		envelope.WriteJSON(w, http.StatusOK, reply)
	case errors.Is(err, x00.ErrInvalidRequest):
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The conversation is not valid")
	case errors.Is(err, context.DeadlineExceeded):
		writeClaudeOrchestratorAPIError(w, r, http.StatusGatewayTimeout, "X00_TIMEOUT", "Claude took too long to answer")
	case errors.Is(err, context.Canceled):
		writeClaudeOrchestratorAPIError(w, r, http.StatusServiceUnavailable, "X00_CANCELED", "The request was canceled")
	default:
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadGateway, "X00_PROVIDER_FAILED", "Claude did not answer; check the key and model in Mission control settings")
	}
}

type x00SpeakRequest struct {
	Text string `json:"text"`
	Lang string `json:"lang,omitempty"`
}

// listen turns a recording (16 kHz mono WAV, the request body) into text.
func (api *X00API) listen(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	if api.Voice == nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusServiceUnavailable, "X00_VOICE_UNAVAILABLE", "No speech engine is installed")
		return
	}
	wav, err := io.ReadAll(io.LimitReader(r.Body, x00voice.MaxAudioBytes+1))
	if err != nil || len(wav) > x00voice.MaxAudioBytes {
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The recording is not valid")
		return
	}
	if !api.takeVoiceSlot(w, r) {
		return
	}
	defer api.releaseVoiceSlot()
	ctx, cancel := context.WithTimeout(r.Context(), x00ListenTimeout)
	defer cancel()
	text, err := api.Voice.Listen(ctx, wav, r.URL.Query().Get("lang"))
	if err != nil {
		writeX00VoiceError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, map[string]string{"text": text})
}

// speak voices one line and answers with the audio itself.
func (api *X00API) speak(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	if api.Voice == nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusServiceUnavailable, "X00_VOICE_UNAVAILABLE", "No speech engine is installed")
		return
	}
	var request x00SpeakRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, x00SpeakMaxBodyBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The line is not valid")
		return
	}
	if !api.takeVoiceSlot(w, r) {
		return
	}
	defer api.releaseVoiceSlot()
	ctx, cancel := context.WithTimeout(r.Context(), x00SpeakTimeout)
	defer cancel()
	audio, err := api.Voice.Speak(ctx, request.Text, request.Lang)
	if err != nil {
		writeX00VoiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", audio.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio.Data)
}

func (api *X00API) takeVoiceSlot(w http.ResponseWriter, r *http.Request) bool {
	select {
	case api.voiceInflight <- struct{}{}:
		return true
	default:
		writeClaudeOrchestratorAPIError(w, r, http.StatusTooManyRequests, "X00_BUSY", "X-00 is still busy with his voice")
		return false
	}
}

func (api *X00API) releaseVoiceSlot() { <-api.voiceInflight }

func writeX00VoiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, x00voice.ErrInvalidAudio), errors.Is(err, x00voice.ErrInvalidText):
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "The recording or line is not valid")
	case errors.Is(err, x00voice.ErrUnavailable):
		writeClaudeOrchestratorAPIError(w, r, http.StatusServiceUnavailable, "X00_VOICE_UNAVAILABLE", "No speech engine is installed")
	case errors.Is(err, context.DeadlineExceeded):
		writeClaudeOrchestratorAPIError(w, r, http.StatusGatewayTimeout, "X00_TIMEOUT", "The speech engine took too long")
	case errors.Is(err, context.Canceled):
		writeClaudeOrchestratorAPIError(w, r, http.StatusServiceUnavailable, "X00_CANCELED", "The request was canceled")
	default:
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadGateway, "X00_VOICE_FAILED", "The speech engine failed")
	}
}

// mountX00 registers X-00. Without a working Claude connection only the info
// route answers, with the reason, so the app can show how to set it up.
func mountX00(r chi.Router, service X00Service, voice X00Voice, cfg config.CommanderConfig, fleet func() x00.Fleet) {
	api := &X00API{Service: service, Voice: voice, Fleet: fleet, Info: X00Info{Enabled: cfg.Enabled && service != nil, Reason: cfg.Reason}}
	if api.Info.Enabled {
		api.Info.Model = cfg.Provider.DefaultModel
	} else if api.Info.Reason == "" {
		api.Info.Reason = config.CommanderReasonNoKey
	}
	api.Register(r)
}
