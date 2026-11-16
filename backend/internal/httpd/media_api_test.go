package httpd

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/media"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/x00"
)

type mediaServiceFake struct {
	info     media.Info
	created  []media.Request
	uploaded []string
	deleted  []string
	err      error
}

func (f *mediaServiceFake) Info() media.Info { return f.info }

func (f *mediaServiceFake) Status(context.Context) media.ServerStatus {
	return media.ServerStatus{Reachable: true, Device: "NVIDIA GeForce RTX 4090"}
}

func (f *mediaServiceFake) List() ([]media.Item, error) {
	return []media.Item{{ID: strings.Repeat("a", 32), Kind: media.KindImage, State: media.StateDone, Files: []media.File{{Name: "1.png", Type: "image/png", Bytes: 9}}}}, f.err
}

func (f *mediaServiceFake) Get(id string) (media.Item, error) {
	if id != strings.Repeat("a", 32) {
		return media.Item{}, media.ErrNotFound
	}
	return media.Item{ID: id, State: media.StateRunning, Files: []media.File{}}, nil
}

func (f *mediaServiceFake) Create(request media.Request) (media.Item, error) {
	if f.err != nil {
		return media.Item{}, f.err
	}
	f.created = append(f.created, request)
	return media.Item{ID: strings.Repeat("b", 32), Capability: request.Capability, State: media.StateQueued, Files: []media.File{}}, nil
}

func (f *mediaServiceFake) Upload(name string, data []byte) (media.Item, error) {
	f.uploaded = append(f.uploaded, name+":"+string(data[:4]))
	return media.Item{ID: strings.Repeat("c", 32), State: media.StateDone, Files: []media.File{}}, nil
}

func (f *mediaServiceFake) Delete(id string) error {
	if id != strings.Repeat("a", 32) {
		return media.ErrNotFound
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func newMediaRouter(service MediaService) chi.Router {
	r := chi.NewRouter()
	mountMedia(r, service)
	return r
}

func TestMediaRoutesAnswerOnlyThisMachine(t *testing.T) {
	r := newMediaRouter(&mediaServiceFake{info: media.Info{Enabled: true, Capabilities: []media.Capability{}}})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/internal/media"},
		{http.MethodGet, "/internal/media/items"},
		{http.MethodPost, "/internal/media/items"},
		{http.MethodDelete, "/internal/media/items/" + strings.Repeat("a", 32)},
	} {
		if rec := serveClaudeOrchestratorRequest(r, route.method, route.path, `{}`, "127.0.0.1:3001", "http://evil.example"); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with an Origin = %d", route.method, route.path, rec.Code)
		}
	}
}

func TestMediaRoutesMakeListAndDelete(t *testing.T) {
	service := &mediaServiceFake{info: media.Info{Enabled: true, Root: "/home/me/.ao/media", Capabilities: []media.Capability{{ID: "image", Kind: media.KindImage, BuiltIn: true}}}}
	r := newMediaRouter(service)
	info := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/media", "", "127.0.0.1:3001", "")
	if info.Code != http.StatusOK || !strings.Contains(info.Body.String(), `"enabled":true`) || !strings.Contains(info.Body.String(), `"id":"image"`) {
		t.Fatalf("info = %d %s", info.Code, info.Body.String())
	}
	created := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/media/items", `{"capability":"image","prompt":"a robot","width":1024,"source":"x00"}`, "127.0.0.1:3001", "")
	if created.Code != http.StatusAccepted || len(service.created) != 1 || service.created[0].Prompt != "a robot" || service.created[0].Source != "x00" {
		t.Fatalf("create = %d %s %+v", created.Code, created.Body.String(), service.created)
	}
	if odd := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/media/items", `{"capability":"image","prompt":"x","shell":"rm -rf"}`, "127.0.0.1:3001", ""); odd.Code != http.StatusBadRequest {
		t.Fatalf("an unknown field was accepted: %d", odd.Code)
	}
	list := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/media/items", "", "127.0.0.1:3001", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"name":"1.png"`) {
		t.Fatalf("list = %d %s", list.Code, list.Body.String())
	}
	if status := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/media/status", "", "127.0.0.1:3001", ""); !strings.Contains(status.Body.String(), `"reachable":true`) {
		t.Fatalf("status = %s", status.Body.String())
	}
	upload := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/media/uploads?name=shoe.png", "\x89PNGdata", "127.0.0.1:3001", "")
	if upload.Code != http.StatusCreated || len(service.uploaded) != 1 || service.uploaded[0] != "shoe.png:\x89PNG" {
		t.Fatalf("upload = %d %v", upload.Code, service.uploaded)
	}
	if gone := serveClaudeOrchestratorRequest(r, http.MethodDelete, "/internal/media/items/"+strings.Repeat("a", 32), "", "127.0.0.1:3001", ""); gone.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", gone.Code)
	}
	if missing := serveClaudeOrchestratorRequest(r, http.MethodDelete, "/internal/media/items/nope", "", "127.0.0.1:3001", ""); missing.Code != http.StatusNotFound {
		t.Fatalf("delete of an unknown item = %d", missing.Code)
	}
}

func TestMediaErrorsBecomeStatuses(t *testing.T) {
	for err, want := range map[error]int{
		media.ErrNotConfigured:     http.StatusServiceUnavailable,
		media.ErrInvalidRequest:    http.StatusBadRequest,
		media.ErrUnknownCapability: http.StatusNotFound,
		media.ErrBusy:              http.StatusTooManyRequests,
	} {
		r := newMediaRouter(&mediaServiceFake{err: err})
		if rec := serveClaudeOrchestratorRequest(r, http.MethodPost, "/internal/media/items", `{"capability":"image","prompt":"x"}`, "127.0.0.1:3001", ""); rec.Code != want {
			t.Errorf("%v = %d, want %d", err, rec.Code, want)
		}
	}
}

func TestX00LearnsWhatTheGPUServerCanMake(t *testing.T) {
	service := &mediaServiceFake{info: media.Info{Enabled: true, Capabilities: []media.Capability{
		{ID: "image", Kind: media.KindImage},
		{ID: "music", Kind: media.KindAudio},
		{ID: "animate", Kind: media.KindVideo, Input: media.KindImage},
	}}}
	fleet := withMedia(nil, service)()
	if !fleet.MediaEnabled || strings.Join(fleet.MediaKinds, ",") != "image,audio" || fleet.RunsEnabled {
		t.Fatalf("fleet = %+v (a capability that needs a picture is not one X-00 can start from words)", fleet)
	}
	runs := withMedia(func() x00.Fleet { return x00.Fleet{RunsEnabled: true, Runs: []x00.RunBrief{{RunID: "r"}}} }, service)()
	if !runs.RunsEnabled || len(runs.Runs) != 1 || !runs.MediaEnabled {
		t.Fatalf("fleet with runs = %+v", runs)
	}
	if withMedia(nil, nil) != nil {
		t.Fatal("no gateway and no runs should leave the fleet nil")
	}
}
