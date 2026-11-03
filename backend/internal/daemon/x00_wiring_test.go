package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/x00"
)

func TestX00TalksToClaudeThroughTheConfiguredProvider(t *testing.T) {
	var got struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"1","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"{\"say\":\"At your command.\",\"mood\":\"pleased\",\"actions\":[]}"}}]}`)
	}))
	defer server.Close()

	cfg := config.CommanderConfig{Enabled: true, Provider: modelgateway.ProviderConfig{
		Provider: ports.ModelProviderClaude, BaseURL: server.URL + "/v1", APIKey: "test-key", DefaultModel: "claude-haiku-5-5", MaxTokens: 256,
	}}
	service := newX00(cfg, slog.New(slog.DiscardHandler))
	if service == nil {
		t.Fatal("X-00 was not built")
	}
	reply, err := service.Reply(context.Background(), x00.Request{Turns: []x00.Turn{{Role: x00.RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Say != "At your command." || reply.Mood != x00.MoodPleased {
		t.Fatalf("reply = %+v", reply)
	}
	if got.Model != "claude-haiku-5-5" || len(got.Messages) != 2 || got.Messages[0].Role != "system" || got.Messages[1].Content != "hello" {
		t.Fatalf("request = %+v", got)
	}
	if !strings.Contains(auth, "test-key") {
		t.Fatal("the key was not sent to the provider")
	}
}

func TestX00StaysSilentWithoutAConnection(t *testing.T) {
	if newX00(config.CommanderConfig{Reason: config.CommanderReasonNoKey}, slog.New(slog.DiscardHandler)) != nil {
		t.Fatal("X-00 should be off without a key")
	}
}

func TestX00VoiceFindsLocalEnginesWithoutARestart(t *testing.T) {
	speech := t.TempDir()
	voice := newX00Voice(config.X00VoiceConfig{SpeechDir: speech})
	if status := voice.Status(); status.Listen != "" || status.Speak != "" {
		t.Fatalf("status before install = %+v", status)
	}
	exe := func(name string) string {
		if runtime.GOOS == "windows" {
			return name + ".exe"
		}
		return name
	}
	for _, path := range []string{
		filepath.Join(speech, "whisper", exe("whisper-cli")),
		filepath.Join(speech, "whisper", "models", "ggml-base-q5_1.bin"),
		filepath.Join(speech, "piper", exe("piper")),
		filepath.Join(speech, "piper", "voices", "en_US-ryan-high.onnx"),
		filepath.Join(speech, "piper", "voices", "en_US-ryan-high.onnx.json"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if status := voice.Status(); status.Listen != "local" || status.Speak != "local" {
		t.Fatalf("status after install = %+v", status)
	}
	cloud := newX00Voice(config.X00VoiceConfig{SpeechDir: speech, Cloud: &config.X00CloudVoiceConfig{
		BaseURL: "https://api.openai.com/v1", APIKey: "k", TranscribeModel: "gpt-4o-mini-transcribe", SpeechModel: "gpt-4o-mini-tts", Voice: "onyx",
	}})
	if status := cloud.Status(); status.Listen != "cloud" || status.Speak != "cloud" {
		t.Fatalf("status with a cloud key = %+v", status)
	}
}
