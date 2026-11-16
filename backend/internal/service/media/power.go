package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// PowerSwitch starts and stops the person's rented GPU server, so it is paid
// for only while there is work.
type PowerSwitch interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// DefaultRunPodURL is RunPod's REST API.
const DefaultRunPodURL = "https://rest.runpod.io"

var runPodIDPattern = regexp.MustCompile(`^[a-z0-9]{6,40}$`)

// ValidRunPodID is true for a pod id RunPod could have given.
func ValidRunPodID(id string) bool { return runPodIDPattern.MatchString(id) }

// RunPod switches one RunPod pod on and off: POST /v1/pods/{id}/start|stop
// with the person's API key. Errors never carry RunPod's own words.
type RunPod struct {
	PodID   string
	APIKey  string
	BaseURL string
	Client  *http.Client
}

// Start switches the pod on.
func (r *RunPod) Start(ctx context.Context) error { return r.call(ctx, "start") }

// Stop switches the pod off.
func (r *RunPod) Stop(ctx context.Context) error { return r.call(ctx, "stop") }

func (r *RunPod) call(ctx context.Context, action string) error {
	if !ValidRunPodID(r.PodID) || r.APIKey == "" {
		return errors.New("media: the RunPod pod is not set")
	}
	base := strings.TrimRight(r.BaseURL, "/")
	if base == "" {
		base = DefaultRunPodURL
	}
	client := r.Client
	if client == nil {
		client = NewHTTPClient()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/pods/"+url.PathEscape(r.PodID)+"/"+action, http.NoBody)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+r.APIKey)
	response, err := client.Do(request)
	if err != nil {
		return errors.New("media: RunPod could not be reached")
	}
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("media: RunPod answered %d to %s", response.StatusCode, action)
	}
	return nil
}

// usageFile keeps how long the server was on, per day, for a month or so.
type usageFile struct {
	Days map[string]int64 `json:"days"`
}

const keepUsageDays = 40

func (s *Service) usagePath() string { return filepath.Join(s.lib.root, "usage.json") }

func (s *Service) readUsage() usageFile {
	usage := usageFile{Days: map[string]int64{}}
	data, err := os.ReadFile(s.usagePath())
	if err == nil && len(data) < 64<<10 {
		_ = json.Unmarshal(data, &usage)
	}
	if usage.Days == nil {
		usage.Days = map[string]int64{}
	}
	return usage
}

// addOnTime counts seconds the server was on today.
func (s *Service) addOnTime(seconds int64, now time.Time) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	usage := s.readUsage()
	usage.Days[now.Format("2006-01-02")] += seconds
	if len(usage.Days) > keepUsageDays {
		days := make([]string, 0, len(usage.Days))
		for day := range usage.Days {
			days = append(days, day)
		}
		sort.Strings(days)
		for _, day := range days[:len(days)-keepUsageDays] {
			delete(usage.Days, day)
		}
	}
	data, err := json.Marshal(usage)
	if err != nil {
		return
	}
	if err := writeFile(s.lib.root, "usage.json", data); err != nil {
		s.log.Warn("media: the usage meter could not be saved", "error", err)
	}
}

// onTime is how long the server was on today and this month.
func (s *Service) onTime(now time.Time) (int64, int64) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	usage := s.readUsage()
	month := now.Format("2006-01")
	var total int64
	for day, seconds := range usage.Days {
		if strings.HasPrefix(day, month) {
			total += seconds
		}
	}
	return usage.Days[now.Format("2006-01-02")], total
}

func cost(seconds int64, price float64) float64 {
	return math.Round(float64(seconds)/3600*price*100) / 100
}

func (s *Service) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending > 0
}

// tick runs every TickEvery while a server is set: due scheduled work goes
// to the queue, the meter counts the time the server is on, and an idle
// server is stopped when the person asked for that.
func (s *Service) tick() {
	s.promote()
	now := s.now()
	if s.busy() {
		s.lastBusy.Store(now.UnixNano())
	}
	ctx, cancel := context.WithTimeout(s.ctx, statusTimeout)
	defer cancel()
	if !s.reachable(ctx) {
		return
	}
	s.addOnTime(int64(s.tickEvery/time.Second), now)
	if s.power == nil || s.idle <= 0 || s.busy() || now.Sub(time.Unix(0, s.lastBusy.Load())) < s.idle {
		return
	}
	if err := s.power.Stop(ctx); err != nil {
		s.log.Warn("media: the idle GPU server could not be stopped", "error", err)
		return
	}
	s.log.Info("media: the GPU server had nothing to do; it is stopped")
	s.lastBusy.Store(now.UnixNano())
}

func (s *Service) reachable(ctx context.Context) bool {
	_, err := s.server.stats(ctx)
	return err == nil
}

// wake starts the server for work when the app may switch it on, and waits
// until it answers.
func (s *Service) wake(ctx context.Context) error {
	if s.power == nil {
		return nil
	}
	s.wakeMu.Lock()
	defer s.wakeMu.Unlock()
	if s.reachable(ctx) {
		return nil
	}
	if err := s.power.Start(ctx); err != nil {
		s.log.Warn("media: the GPU server could not be started", "error", err)
		return failure(FailUnreachable)
	}
	deadline := time.Now().Add(wakeTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.wakeEvery):
		}
		if s.reachable(ctx) {
			return nil
		}
	}
	return failure(FailUnreachable)
}

// promote sends scheduled work whose time has come to the queue, oldest
// first, as far as the queue has room.
func (s *Service) promote() {
	if s.server == nil {
		return
	}
	s.promoteMu.Lock()
	defer s.promoteMu.Unlock()
	items, err := s.lib.list()
	if err != nil {
		return
	}
	now := s.now()
	due := make([]Item, 0)
	for _, item := range items {
		if item.State == StateScheduled && (item.RunAt == nil || !item.RunAt.After(now)) {
			due = append(due, item)
		}
	}
	sort.SliceStable(due, func(a, b int) bool { return runAt(due[a]).Before(runAt(due[b])) })
	for _, item := range due {
		s.mu.Lock()
		if s.pending >= maxQueued {
			s.mu.Unlock()
			return
		}
		s.pending++
		s.mu.Unlock()
		changed := false
		err := s.lib.update(item.ID, func(stored *Item) {
			if stored.State == StateScheduled {
				stored.State = StateQueued
				stored.UpdatedAt = now.UTC()
				changed = true
			}
		})
		if err != nil || !changed {
			s.done()
			continue
		}
		s.queue <- item.ID
	}
}

func runAt(item Item) time.Time {
	if item.RunAt != nil {
		return *item.RunAt
	}
	return item.CreatedAt
}
