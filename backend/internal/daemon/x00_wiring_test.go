package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
