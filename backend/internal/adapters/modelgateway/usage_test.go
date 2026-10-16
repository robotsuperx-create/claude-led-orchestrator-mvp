package modelgateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type recordingMeter struct {
	refuse   bool
	provider ports.ModelProvider
	input    int
	output   int
}

func (m *recordingMeter) Reserve(ports.ModelProvider) error {
	if m.refuse {
		return ports.ErrRunBudgetExceeded
	}
	return nil
}

func (m *recordingMeter) Record(provider ports.ModelProvider, input, output int) {
	m.provider, m.input, m.output = provider, m.input+input, m.output+output
}

func TestModelCallsReportUsageToTheRunMeter(t *testing.T) {
	var requests atomic.Int32
	var lastBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, _ := io.ReadAll(r.Body)
		lastBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"{\"summary\":\"s\",\"subtasks\":[{\"id\":\"a\",\"worker_id\":\"w\"}]}"}}],"usage":{"prompt_tokens":321,"completion_tokens":45,"total_tokens":366}}`)
	}))
	t.Cleanup(server.Close)
	adapter := newTestAdapter(t, ProviderConfig{Provider: ports.ModelProviderLocal, BaseURL: server.URL, DefaultModel: "qwen2.5-coder"})

	meter := &recordingMeter{}
	ctx := ports.WithUsageMeter(context.Background(), meter)
	if _, err := adapter.Plan(ctx, ports.PlanRequest{Task: "t", PreviousAttempt: "- b: FAILED: tests"}); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if meter.provider != ports.ModelProviderLocal || meter.input != 321 || meter.output != 45 {
		t.Fatalf("meter = %+v, want the provider-reported counts", meter)
	}
	if !strings.Contains(lastBody, "previous_attempt") {
		t.Fatalf("revision request did not carry the previous attempt: %s", lastBody)
	}

	// Once the budget is spent, no request leaves the machine.
	refused := ports.WithUsageMeter(context.Background(), &recordingMeter{refuse: true})
	if _, err := adapter.Plan(refused, ports.PlanRequest{Task: "t"}); !errors.Is(err, ports.ErrRunBudgetExceeded) {
		t.Fatalf("Plan with a spent budget = %v, want ErrRunBudgetExceeded", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want the refused call never sent", requests.Load())
	}
}
