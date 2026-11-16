package media

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a time the test moves by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// fakePower switches the fake server on and off.
type fakePower struct {
	comfy  *fakeComfy
	starts atomic.Int32
	stops  atomic.Int32
}

func (p *fakePower) Start(context.Context) error {
	p.starts.Add(1)
	p.comfy.mu.Lock()
	p.comfy.offline = false
	p.comfy.mu.Unlock()
	return nil
}

func (p *fakePower) Stop(context.Context) error {
	p.stops.Add(1)
	p.comfy.mu.Lock()
	p.comfy.offline = true
	p.comfy.mu.Unlock()
	return nil
}

func newTimedGateway(t *testing.T, comfy *fakeComfy, now *clock, power PowerSwitch, idle int) *Service {
	t.Helper()
	comfy.t = t
	comfy.token = "gpu-token"
	comfy.output = outputRef{Filename: "xrobot_00001_.png", Subfolder: "xrobot", Type: "output"}
	comfy.body = pngBytes
	server := httptest.NewServer(comfy)
	t.Cleanup(server.Close)
	service := New(Config{
		LibraryDir: t.TempDir(), BaseURL: server.URL, Token: "gpu-token", PollEvery: 5 * time.Millisecond,
		Now: now.Now, PricePerHour: 0.69, Power: power, IdleMinutes: idle, TickEvery: time.Hour, WakeEvery: 5 * time.Millisecond,
	})
	t.Cleanup(service.Close)
	return service
}

func TestABatchForTonightWaitsThenRunsEachWithItsOwnSeed(t *testing.T) {
	now := &clock{now: time.Date(2026, 11, 16, 22, 0, 0, 0, time.UTC)}
	service := newTimedGateway(t, &fakeComfy{}, now, nil, 0)
	tonight := time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)
	first, err := service.Create(Request{Capability: "image", Prompt: "kids coloring page, robot astronaut", Count: 3, RunAt: &tonight})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if first.State != StateScheduled || first.RunAt == nil || !first.RunAt.Equal(tonight) {
		t.Fatalf("first = %+v", first)
	}
	service.promote()
	items, _ := service.List()
	seeds := map[int64]bool{}
	for _, item := range items {
		if item.State != StateScheduled {
			t.Fatalf("%s ran before its time: %s", item.ID, item.State)
		}
		seeds[item.Seed] = true
	}
	if len(items) != 3 || len(seeds) != 3 {
		t.Fatalf("batch = %d items, %d seeds", len(items), len(seeds))
	}
	if status := service.Status(t.Context()); status.Scheduled != 3 {
		t.Errorf("scheduled = %d", status.Scheduled)
	}
	now.add(4 * time.Hour)
	service.promote()
	for _, item := range items {
		waitFor(t, service, item.ID, StateDone)
	}
	for name, request := range map[string]Request{
		"too many":     {Capability: "image", Prompt: "x", Count: MaxBatch + 1},
		"too far away": {Capability: "image", Prompt: "x", RunAt: ptr(now.Now().Add(8 * 24 * time.Hour))},
	} {
		if _, err := service.Create(request); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTheServerIsStartedForWorkMeteredAndStoppedWhenIdle(t *testing.T) {
	now := &clock{now: time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)}
	comfy := &fakeComfy{offline: true}
	power := &fakePower{comfy: comfy}
	service := newTimedGateway(t, comfy, now, power, 15)
	item, err := service.Create(Request{Capability: "image", Prompt: "hero shot"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitFor(t, service, item.ID, StateDone)
	if power.starts.Load() != 1 {
		t.Fatalf("starts = %d", power.starts.Load())
	}
	// On and busy a moment ago: the meter counts, nothing is stopped.
	service.tick()
	if power.stops.Load() != 0 {
		t.Fatal("a server with recent work was stopped")
	}
	status := service.Status(t.Context())
	if !status.Reachable || !status.AutoPower || status.IdleMinutes != 15 || status.OnSecondsToday != 3600 || status.PricePerHour != 0.69 || status.CostToday != 0.69 {
		t.Fatalf("status = %+v", status)
	}
	now.add(16 * time.Minute)
	service.tick()
	if power.stops.Load() != 1 {
		t.Fatalf("stops after 16 idle minutes = %d", power.stops.Load())
	}
	if status := service.Status(t.Context()); status.Reachable || status.OnSecondsMonth != 7200 || status.CostMonth != 1.38 {
		t.Fatalf("status once stopped = %+v (the meter remembers)", status)
	}
}

func TestRunPodSwitchesThePodWithTheKey(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
		if r.URL.Path == "/v1/pods/abc123xyz/stop" {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()
	pod := &RunPod{PodID: "abc123xyz", APIKey: "rp-key", BaseURL: server.URL, Client: server.Client()}
	if err := pod.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := pod.Stop(t.Context()); err == nil {
		t.Fatal("a refused stop was not an error")
	}
	if len(calls) != 2 || calls[0] != "POST /v1/pods/abc123xyz/start Bearer rp-key" {
		t.Fatalf("calls = %q", calls)
	}
	if err := (&RunPod{PodID: "../x", APIKey: "k"}).Start(t.Context()); err == nil {
		t.Fatal("a bad pod id was used")
	}
}

func ptr[T any](value T) *T { return &value }

func TestARunsCrewGetsItsPictureBackAndTheLibraryKeepsIt(t *testing.T) {
	service, _ := newGateway(t, &fakeComfy{})
	if !service.Enabled() {
		t.Fatal("a gateway with a server is not enabled")
	}
	picture, err := service.MakePicture(t.Context(), "hero image for the tint store", 1344, 768)
	if err != nil || string(picture) != string(pngBytes) {
		t.Fatalf("MakePicture = %d bytes, %v", len(picture), err)
	}
	items, _ := service.List()
	if len(items) != 1 || items[0].Source != SourceCrew || items[0].Width != 1344 {
		t.Fatalf("library = %+v", items)
	}
	failing, _ := newGateway(t, &fakeComfy{submitAs: http.StatusBadRequest})
	if _, err := failing.MakePicture(t.Context(), "x", 1024, 1024); err == nil || codeOf(err) != FailRejected {
		t.Fatalf("a refused picture = %v", err)
	}
}
