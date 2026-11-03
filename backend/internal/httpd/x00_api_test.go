package httpd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/x00"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/x00voice"
)

type x00ServiceFake struct {
	mu      sync.Mutex
	reply   x00.Reply
	err     error
	request x00.Request
	block   chan struct{}
	entered chan struct{}
}

func (f *x00ServiceFake) Reply(ctx context.Context, request x00.Request) (x00.Reply, error) {
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return x00.Reply{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.request = request
	return f.reply, f.err
}

func newX00Router(service X00Service, cfg config.CommanderConfig, fleet func() x00.Fleet) chi.Router {
	r := chi.NewRouter()
	mountX00(r, service, nil, cfg, fleet)
	return r
}

func enabledCommander() config.CommanderConfig {
	return config.CommanderConfig{Enabled: true, Provider: modelgateway.ProviderConfig{DefaultModel: "claude-haiku-5-5", APIKey: "sk-ant-secret"}}
}

const x00Body = `{"turns":[{"role":"user","content":"status?"}],"locale":"ar","screen":"mission_control"}`

func TestX00InfoReportsModelOrReasonWithoutSecrets(t *testing.T) {
	r := newX00Router(&x00ServiceFake{}, enabledCommander(), nil)
	rec := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/x00", "", "127.0.0.1:3001", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":true`) || !strings.Contains(rec.Body.String(), "claude-haiku-5-5") {
		t.Fatalf("info = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-ant") {
		t.Fatal("the key leaked")
	}
	off := newX00Router(nil, config.CommanderConfig{Reason: config.CommanderReasonNoKey}, nil)
	rec = serveClaudeOrchestratorRequest(off, http.MethodGet, "/internal/x00", "", "127.0.0.1:3001", "")
	if !strings.Contains(rec.Body.String(), `"enabled":false`) || !strings.Contains(rec.Body.String(), `"reason":"no_key"`) {
		t.Fatalf("off info = %s", rec.Body.String())
	}
	rec = serveClaudeOrchestratorRequest(off, http.MethodPost, "/internal/x00/chat", x00Body, "127.0.0.1:3001", "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "X00_DISABLED") {
		t.Fatalf("off chat = %d %s", rec.Code, rec.Body.String())
	}
}

func TestX00RefusesBrowsersAndRemoteHosts(t *testing.T) {
	r := newX00Router(&x00ServiceFake{}, enabledCommander(), nil)
	for _, tc := range []struct{ host, origin string }{{"127.0.0.1:3001", "http://localhost:5173"}, {"192.168.1.5:3001", ""}} {
		for _, path := range []string{"/internal/x00", "/internal/x00/chat"} {
			method := http.MethodGet
			if strings.HasSuffix(path, "/chat") {
				method = http.MethodPost
			}
			rec := serveClaudeOrchestratorRequest(r, method, path, x00Body, tc.host, tc.origin)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s from %s/%s = %d", method, path, tc.host, tc.origin, rec.Code)
			}
		}
	}
}

func TestX00ChatPassesTheFleetAndReturnsTheReply(t *testing.T) {
	service := &x00ServiceFake{reply: x00.Reply{Say: "حسناً.", Mood: x00.MoodPleased, Actions: []x00.Action{{Type: x00.ActionOpen, Screen: x00.ScreenMissionControl}}}}
	fleet := func() x00.Fleet {
		return x00.Fleet{RunsEnabled: true, Runs: []x00.RunBrief{{RunID: "claude-run-a", State: "executing"}}}
	}
	r := newX00Router(service, enabledCommander(), fleet)
	rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/chat", x00Body, "127.0.0.1:3001", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("chat = %d %s", rec.Code, rec.Body.String())
	}
	var reply x00.Reply
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || reply.Say != "حسناً." || len(reply.Actions) != 1 {
		t.Fatalf("reply = %+v (%v)", reply, err)
	}
	if service.request.Locale != "ar" || service.request.Screen != "mission_control" || len(service.request.Fleet.Runs) != 1 || !service.request.Fleet.RunsEnabled {
		t.Fatalf("request = %+v", service.request)
	}
}

func TestX00ChatMapsErrorsWithoutProviderText(t *testing.T) {
	cases := []struct {
		err  error
		code int
		want string
	}{
		{x00.ErrInvalidRequest, http.StatusBadRequest, "INVALID_REQUEST"},
		{x00.ErrProvider, http.StatusBadGateway, "X00_PROVIDER_FAILED"},
		{context.DeadlineExceeded, http.StatusGatewayTimeout, "X00_TIMEOUT"},
		{errors.New("401 sk-ant-secret"), http.StatusBadGateway, "X00_PROVIDER_FAILED"},
	}
	for _, tc := range cases {
		r := newX00Router(&x00ServiceFake{err: tc.err}, enabledCommander(), nil)
		rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/chat", x00Body, "127.0.0.1:3001", "")
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.want) || strings.Contains(rec.Body.String(), "sk-ant") {
			t.Fatalf("%v → %d %s", tc.err, rec.Code, rec.Body.String())
		}
	}
	r := newX00Router(&x00ServiceFake{}, enabledCommander(), nil)
	for _, body := range []string{`{`, `{"turns":[],"extra":1}`, `{"turns":"` + strings.Repeat("a", x00MaxBodyBytes) + `"}`} {
		rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/chat", body, "127.0.0.1:3001", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %.20q → %d", body, rec.Code)
		}
	}
}

func TestX00ChatLimitsConcurrentAnswers(t *testing.T) {
	service := &x00ServiceFake{block: make(chan struct{}), entered: make(chan struct{}, x00MaxInFlight)}
	r := newX00Router(service, enabledCommander(), nil)
	var wg sync.WaitGroup
	for range x00MaxInFlight {
		wg.Add(1)
		go func() {
			defer wg.Done()
			serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/chat", x00Body, "127.0.0.1:3001", "")
		}()
	}
	for range x00MaxInFlight {
		<-service.entered
	}
	rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/chat", x00Body, "127.0.0.1:3001", "")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "X00_BUSY") {
		t.Fatalf("third answer = %d %s", rec.Code, rec.Body.String())
	}
	close(service.block)
	wg.Wait()
}

type x00VoiceFake struct {
	mu      sync.Mutex
	status  x00voice.Status
	text    string
	audio   x00voice.Audio
	err     error
	lang    string
	wav     []byte
	said    string
	block   chan struct{}
	entered chan struct{}
}

func (f *x00VoiceFake) Status() x00voice.Status { return f.status }

func (f *x00VoiceFake) wait(ctx context.Context) error {
	if f.entered != nil {
		f.entered <- struct{}{}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (f *x00VoiceFake) Listen(ctx context.Context, wav []byte, lang string) (string, error) {
	if err := f.wait(ctx); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wav, f.lang = wav, lang
	return f.text, f.err
}

func (f *x00VoiceFake) Speak(ctx context.Context, text, lang string) (x00voice.Audio, error) {
	if err := f.wait(ctx); err != nil {
		return x00voice.Audio{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.said, f.lang = text, lang
	return f.audio, f.err
}

func newX00VoiceRouter(voice X00Voice) chi.Router {
	r := chi.NewRouter()
	mountX00(r, &x00ServiceFake{}, voice, enabledCommander(), nil)
	return r
}

func TestX00InfoNamesTheVoiceEngines(t *testing.T) {
	r := newX00VoiceRouter(&x00VoiceFake{status: x00voice.Status{Listen: "local", Speak: "cloud"}})
	rec := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/x00", "", "127.0.0.1:3001", "")
	if !strings.Contains(rec.Body.String(), `"listen":"local"`) || !strings.Contains(rec.Body.String(), `"speak":"cloud"`) {
		t.Fatalf("info = %s", rec.Body.String())
	}
	none := newX00VoiceRouter(&x00VoiceFake{})
	rec = serveClaudeOrchestratorRequest(none, http.MethodGet, "/internal/x00", "", "127.0.0.1:3001", "")
	if strings.Contains(rec.Body.String(), "listen") || strings.Contains(rec.Body.String(), "speak") {
		t.Fatalf("info without engines = %s", rec.Body.String())
	}
}

func TestX00ListensAndSpeaks(t *testing.T) {
	voice := &x00VoiceFake{text: "start a run", audio: x00voice.Audio{Data: []byte("RIFF-audio"), ContentType: "audio/wav"}}
	r := newX00VoiceRouter(voice)
	rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/listen?lang=ar", "RIFF-recording", "127.0.0.1:3001", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"text":"start a run"`) || voice.lang != "ar" || string(voice.wav) != "RIFF-recording" {
		t.Fatalf("listen = %d %s (lang %q)", rec.Code, rec.Body.String(), voice.lang)
	}
	rec = serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/speak", `{"text":"مرحباً","lang":"ar"}`, "127.0.0.1:3001", "")
	if rec.Code != http.StatusOK || rec.Body.String() != "RIFF-audio" || rec.Header().Get("Content-Type") != "audio/wav" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("speak = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if voice.said != "مرحباً" {
		t.Fatalf("said %q", voice.said)
	}
	for _, body := range []string{`{"text":1}`, `{"text":"a","extra":true}`, `not json`} {
		rec = serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/speak", body, "127.0.0.1:3001", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("speak %s = %d", body, rec.Code)
		}
	}
	rec = serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/listen", strings.Repeat("x", x00voice.MaxAudioBytes+1), "127.0.0.1:3001", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized recording = %d", rec.Code)
	}
}

func TestX00VoiceRefusesBrowsersAndMapsErrors(t *testing.T) {
	voice := &x00VoiceFake{}
	r := newX00VoiceRouter(voice)
	for _, path := range []string{"/internal/x00/listen", "/internal/x00/speak"} {
		rec := serveClaudeOrchestratorRequest(r, http.MethodPost, path, `{"text":"hi"}`, "127.0.0.1:3001", "http://localhost:5173")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s from a browser = %d", path, rec.Code)
		}
	}
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{x00voice.ErrInvalidAudio, http.StatusBadRequest, "INVALID_REQUEST"},
		{x00voice.ErrUnavailable, http.StatusServiceUnavailable, "X00_VOICE_UNAVAILABLE"},
		{x00voice.ErrFailed, http.StatusBadGateway, "X00_VOICE_FAILED"},
		{context.DeadlineExceeded, http.StatusGatewayTimeout, "X00_TIMEOUT"},
		{errors.New("whisper crashed at C:\\Users\\secret"), http.StatusBadGateway, "X00_VOICE_FAILED"},
	}
	for _, tc := range cases {
		voice.err = tc.err
		rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/listen", "RIFF", "127.0.0.1:3001", "")
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) || strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("%v: %d %s", tc.err, rec.Code, rec.Body.String())
		}
	}
	unwired := chi.NewRouter()
	mountX00(unwired, nil, nil, config.CommanderConfig{Reason: config.CommanderReasonNoKey}, nil)
	rec := serveClaudeOrchestratorRequest(unwired, http.MethodPost, "/internal/x00/speak", `{"text":"hi"}`, "127.0.0.1:3001", "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "X00_VOICE_UNAVAILABLE") {
		t.Fatalf("no voice = %d %s", rec.Code, rec.Body.String())
	}
}

func TestX00VoiceIsBusyBeyondTwoAtOnce(t *testing.T) {
	voice := &x00VoiceFake{text: "ok", block: make(chan struct{}), entered: make(chan struct{}, 4)}
	r := newX00VoiceRouter(voice)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/listen", "RIFF", "127.0.0.1:3001", "")
		}()
	}
	<-voice.entered
	<-voice.entered
	rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/x00/speak", `{"text":"hi"}`, "127.0.0.1:3001", "")
	close(voice.block)
	wg.Wait()
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "X00_BUSY") {
		t.Fatalf("third call = %d %s", rec.Code, rec.Body.String())
	}
}
