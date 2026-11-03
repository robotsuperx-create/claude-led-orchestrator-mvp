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
	mountX00(r, service, cfg, fleet)
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
