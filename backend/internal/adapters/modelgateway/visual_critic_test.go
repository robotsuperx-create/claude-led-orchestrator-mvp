package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func newTestCritic(t *testing.T, url string) *VisualCritic {
	t.Helper()
	critic, err := NewVisualCritic(ProviderConfig{Provider: ports.ModelProviderClaude, BaseURL: url + "/v1", APIKey: "sk-test", DefaultModel: "claude-fable-5-2", MaxTokens: 9000})
	if err != nil {
		t.Fatalf("NewVisualCritic: %v", err)
	}
	critic.sleep = func(context.Context, time.Duration) error { return nil }
	return critic
}

func shotsForCritic() []ports.VisualShot {
	return []ports.VisualShot{
		{Page: "index.html", Viewport: "desktop", Width: 1440, Height: 2200, JPEG: []byte{0xFF, 0xD8, 0x01}},
		{Page: "index.html", Viewport: "phone", Width: 390, Height: 4100, JPEG: []byte{0xFF, 0xD8, 0x02}, Findings: []string{"The page scrolls sideways."}},
	}
}

// The critic sees every screenshot as an image through the Messages API,
// with the brief and the measurements, on the run's key and model.
func TestVisualCriticSendsThePicturesThroughTheMessagesAPI(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "sk-test" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("request = %s %s, key %q, version %q", r.Method, r.URL.Path, r.Header.Get("x-api-key"), r.Header.Get("anthropic-version"))
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		_, _ = io.WriteString(w, `{"content":[{"type":"thinking","thinking":"..."},{"type":"text","text":"`+"```json\\n"+`{\"score\":11,\"scores\":{\"hierarchy\":7,\"colour\":-2},\"summary\":\"Strong hero, broken phone layout.\",\"fixes\":[\"  Make the hero   stack on phones  \",\"\",\"Raise body text contrast to #333\"]}`+"\\n```"+`"}],"usage":{"input_tokens":4200,"output_tokens":800,"cache_read_input_tokens":100}}`)
	}))
	t.Cleanup(server.Close)
	critic := newTestCritic(t, server.URL)
	meter := &recordingMeter{}
	critique, err := critic.Critique(ports.WithUsageMeter(context.Background(), meter), ports.VisualCritiqueRequest{
		Task: "A landing page", Brief: "Red #F40009, Reem Kufi headings", Shots: shotsForCritic(), Round: 2, PreviousFixes: []string{"bigger hero"},
	})
	if err != nil {
		t.Fatalf("Critique: %v", err)
	}
	if critique.Score != 10 || critique.Scores["hierarchy"] != 7 || critique.Scores["colour"] != 0 {
		t.Fatalf("critique scores = %+v, want them kept within 0..10", critique)
	}
	if strings.Join(critique.Fixes, "|") != "Make the hero stack on phones|Raise body text contrast to #333" {
		t.Fatalf("fixes = %q", critique.Fixes)
	}
	if meter.input != 4300 || meter.output != 800 || meter.provider != ports.ModelProviderClaude {
		t.Fatalf("meter = %+v, want the reported usage", meter)
	}
	if body["model"] != "claude-fable-5-2" || body["max_tokens"] != float64(9000) || !strings.Contains(body["system"].(string), "art director") {
		t.Fatalf("request = model %v, max_tokens %v", body["model"], body["max_tokens"])
	}
	if _, set := body["temperature"]; set {
		t.Fatal("temperature was sent; Fable 5.2 rejects it")
	}
	content := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	images := 0
	for _, block := range content {
		part := block.(map[string]any)
		if part["type"] == "image" {
			images++
			source := part["source"].(map[string]any)
			if source["type"] != "base64" || source["media_type"] != "image/jpeg" || source["data"] == "" {
				t.Fatalf("image block = %v", source)
			}
		}
	}
	facts := content[0].(map[string]any)["text"].(string)
	if images != 2 || !strings.Contains(facts, "Reem Kufi") || !strings.Contains(facts, "scrolls sideways") || !strings.Contains(facts, "bigger hero") {
		t.Fatalf("images = %d, facts = %s", images, facts)
	}
}

func TestVisualCriticRetriesWhenClaudeIsBusy(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(529)
			return
		}
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"{\"score\":8.5,\"summary\":\"ok\",\"fixes\":[]}"}]}`)
	}))
	t.Cleanup(server.Close)
	critique, err := newTestCritic(t, server.URL).Critique(context.Background(), ports.VisualCritiqueRequest{Task: "t", Shots: shotsForCritic()})
	if err != nil || critique.Score != 8.5 || calls.Load() != 2 {
		t.Fatalf("Critique = %+v, %v after %d calls", critique, err, calls.Load())
	}
}

func TestVisualCriticRefusesWhatItCannotDo(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"not json"}]}`)
	}))
	t.Cleanup(server.Close)
	critic := newTestCritic(t, server.URL)
	if _, err := critic.Critique(context.Background(), ports.VisualCritiqueRequest{Task: "t"}); err == nil {
		t.Fatal("a critique without screenshots was sent")
	}
	refused := ports.WithUsageMeter(context.Background(), &recordingMeter{refuse: true})
	if _, err := critic.Critique(refused, ports.VisualCritiqueRequest{Task: "t", Shots: shotsForCritic()}); !errors.Is(err, ports.ErrRunBudgetExceeded) {
		t.Fatalf("over budget = %v, want ErrRunBudgetExceeded", err)
	}
	if calls.Load() != 0 {
		t.Fatal("a request left the machine")
	}
	if _, err := critic.Critique(context.Background(), ports.VisualCritiqueRequest{Task: "t", Shots: shotsForCritic()}); err == nil || strings.Contains(err.Error(), "not json") {
		t.Fatalf("invalid answer = %v, want a generic error", err)
	}
	if _, err := NewVisualCritic(ProviderConfig{Provider: ports.ModelProviderDeepSeek, BaseURL: "https://x.invalid/v1", DefaultModel: "m"}); err == nil {
		t.Fatal("a non-Claude critic was built")
	}
}
