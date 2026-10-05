package controllers

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClaudeOrchestratorStartRequestJSONContract(t *testing.T) {
	got, err := json.Marshal(ClaudeOrchestratorStartRequest{
		RunID:         "run-123",
		Task:          "review the requested change",
		MaxRetries:    2,
		ExplicitOptIn: true,
	})
	if err != nil {
		t.Fatalf("Marshal start request: %v", err)
	}
	want := `{"runId":"run-123","task":"review the requested change","maxRetries":2,"explicitOptIn":true}`
	if string(got) != want {
		t.Fatalf("start request JSON = %s, want %s", got, want)
	}

	got, err = json.Marshal(ClaudeOrchestratorStartRequest{
		RunID:         "run-123",
		Task:          "task",
		ExplicitOptIn: false,
	})
	if err != nil {
		t.Fatalf("Marshal default request: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatalf("Unmarshal request JSON: %v", err)
	}
	if _, ok := fields["explicitOptIn"]; !ok {
		t.Fatal("explicitOptIn must remain present when false (default-deny contract)")
	}
	if _, ok := fields["maxRetries"]; ok {
		t.Fatal("maxRetries should be omitted when zero")
	}
}

func TestClaudeOrchestratorStartResponseJSONContractIsMinimal(t *testing.T) {
	got, err := json.Marshal(ClaudeOrchestratorStartResponse{
		RunID:    "run-123",
		State:    ports.RunStateHeld,
		Decision: ports.MergeOutcomeHold,
	})
	if err != nil {
		t.Fatalf("Marshal start response: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatalf("Unmarshal response JSON: %v", err)
	}
	want := []string{"decision", "runId", "state"}
	gotKeys := make([]string, 0, len(fields))
	for key := range fields {
		gotKeys = append(gotKeys, key)
	}
	if !reflect.DeepEqual(sortedStrings(gotKeys), want) {
		t.Fatalf("response fields = %v, want exactly %v", sortedStrings(gotKeys), want)
	}
	if string(fields["state"]) != `"held"` || string(fields["decision"]) != `"hold"` {
		t.Fatalf("response enum values = state %s, decision %s; want held/hold", fields["state"], fields["decision"])
	}
	for _, forbidden := range []string{"task", "output", "error", "details", "provider", "secret"} {
		if _, ok := fields[forbidden]; ok {
			t.Errorf("response unexpectedly exposes %q", forbidden)
		}
	}
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
