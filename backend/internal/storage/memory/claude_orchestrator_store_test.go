package memory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCreateOrGetRunIsIdempotentAndRejectsMismatchedReuse(t *testing.T) {
	ctx := context.Background()
	store := NewClaudeOrchestratorMemoryStore()
	run := testRun()
	idem := testIdempotency()

	created, inserted, err := store.CreateOrGetRun(ctx, run, idem)
	if err != nil || !inserted || created != run {
		t.Fatalf("first admission = (%+v, %v, %v), want created run", created, inserted, err)
	}

	retry := run
	retry.State = ports.RunStateFailed
	retry.UpdatedAt = run.UpdatedAt.Add(time.Minute)
	got, inserted, err := store.CreateOrGetRun(ctx, retry, idem)
	if err != nil || inserted || got != run {
		t.Fatalf("idempotent retry = (%+v, %v, %v), want original run", got, inserted, err)
	}

	mismatched := idem
	mismatched.RequestDigest = strings.Repeat("d", 64)
	if _, _, err := store.CreateOrGetRun(ctx, run, mismatched); !errors.Is(err, ports.ErrOrchestratorIdempotencyConflict) {
		t.Fatalf("mismatched idempotency reuse error = %v, want conflict", err)
	}
}

func TestRunUpdatesUseExpectedStateAndEventsAreSequenced(t *testing.T) {
	ctx := context.Background()
	store := NewClaudeOrchestratorMemoryStore()
	createTestRun(t, store)

	updated := testRun()
	updated.State = ports.RunStatePlanning
	updated.UpdatedAt = updated.UpdatedAt.Add(time.Minute)
	if ok, err := store.UpdateRun(ctx, updated, ports.RunStatePending); err != nil || !ok {
		t.Fatalf("valid state transition = (%v, %v), want applied", ok, err)
	}
	if ok, err := store.UpdateRun(ctx, updated, ports.RunStatePending); err != nil || ok {
		t.Fatalf("stale state transition = (%v, %v), want not applied", ok, err)
	}

	first, err := store.AppendEvent(ctx, ports.OrchestratorEvent{
		EventID: "event-1", RunID: "run-1", Kind: ports.OrchestratorEventRunCreated,
		State: ports.RunStatePending, OccurredAt: testTime,
	})
	if err != nil || first.Sequence != 1 {
		t.Fatalf("first event = (%+v, %v), want sequence 1", first, err)
	}
	second, err := store.AppendEvent(ctx, ports.OrchestratorEvent{
		EventID: "event-2", RunID: "run-1", Kind: ports.OrchestratorEventStateChanged,
		State: ports.RunStatePlanning, OccurredAt: testTime.Add(time.Minute),
	})
	if err != nil || second.Sequence != 2 {
		t.Fatalf("second event = (%+v, %v), want sequence 2", second, err)
	}

	events, err := store.ListEvents(ctx, "run-1", 1, 10)
	if err != nil || len(events) != 1 || events[0] != second {
		t.Fatalf("events after sequence 1 = (%+v, %v), want second event", events, err)
	}
}

func TestCancelAndFenceStateSurviveStoreRestartSimulation(t *testing.T) {
	ctx := context.Background()
	state := NewClaudeOrchestratorMemoryState()
	beforeRestart := NewClaudeOrchestratorMemoryStoreWithState(state)
	createTestRun(t, beforeRestart)

	cancelled, applied, err := beforeRestart.RequestCancel(ctx, "run-1", testTime.Add(time.Minute), ports.OrchestratorCancelUserRequest)
	if err != nil || !applied || !cancelled.Requested {
		t.Fatalf("cancel request = (%+v, %v, %v), want newly persisted", cancelled, applied, err)
	}
	lease, acquired, err := beforeRestart.AcquireFence(ctx, "run-1", strings.Repeat("e", 64), testTime, testTime.Add(5*time.Minute))
	if err != nil || !acquired || lease.Epoch != 1 {
		t.Fatalf("fence acquire = (%+v, %v, %v), want epoch 1", lease, acquired, err)
	}
	if _, err := beforeRestart.AppendEvent(ctx, ports.OrchestratorEvent{
		EventID: "event-restart", RunID: "run-1", Kind: ports.OrchestratorEventCancelRequested,
		OccurredAt: testTime.Add(time.Minute),
	}); err != nil {
		t.Fatalf("append restart event: %v", err)
	}

	// A new adapter/service object shares only the explicitly injected in-memory
	// backing state; this simulates restart boundaries without claiming disk durability.
	afterRestart := NewClaudeOrchestratorMemoryStoreWithState(state)
	gotRun, found, err := afterRestart.GetRun(ctx, "run-1")
	if err != nil || !found || gotRun != testRun() {
		t.Fatalf("run after restart simulation = (%+v, %v, %v)", gotRun, found, err)
	}
	gotCancel, err := afterRestart.GetCancelState(ctx, "run-1")
	if err != nil || gotCancel != cancelled {
		t.Fatalf("cancel state after restart simulation = (%+v, %v), want %+v", gotCancel, err, cancelled)
	}
	valid, err := afterRestart.ValidateFence(ctx, lease, testTime.Add(2*time.Minute))
	if err != nil || !valid {
		t.Fatalf("fence after restart simulation = (%v, %v), want valid", valid, err)
	}
	events, err := afterRestart.ListEvents(ctx, "run-1", 0, 5)
	if err != nil || len(events) != 1 || events[0].EventID != "event-restart" || events[0].Sequence != 1 {
		t.Fatalf("events after restart simulation = (%+v, %v)", events, err)
	}
	if _, inserted, err := afterRestart.CreateOrGetRun(ctx, testRun(), testIdempotency()); err != nil || inserted {
		t.Fatalf("idempotency after restart simulation = (inserted %v, err %v), want existing run", inserted, err)
	}

	next, acquired, err := afterRestart.AcquireFence(ctx, "run-1", strings.Repeat("f", 64), testTime.Add(6*time.Minute), testTime.Add(8*time.Minute))
	if err != nil || !acquired || next.Epoch != 2 {
		t.Fatalf("expired lease reacquire = (%+v, %v, %v), want epoch 2", next, acquired, err)
	}
	valid, err = afterRestart.ValidateFence(ctx, lease, testTime.Add(6*time.Minute))
	if err != nil || valid {
		t.Fatalf("stale fence validation = (%v, %v), want invalid", valid, err)
	}
}

func TestPersistenceContractsRejectRawPromptAndIdempotencyValues(t *testing.T) {
	store := NewClaudeOrchestratorMemoryStore()
	ctx := context.Background()
	unsafeRun := testRun()
	unsafeRun.TaskDigest = "full prompt: keep the API key private"
	if _, _, err := store.CreateOrGetRun(ctx, unsafeRun, testIdempotency()); !errors.Is(err, ports.ErrOrchestratorInvalidRecord) {
		t.Fatalf("raw task prompt error = %v, want invalid record", err)
	}
	unsafeIdem := testIdempotency()
	unsafeIdem.KeyDigest = "sk-test-api-key"
	if _, _, err := store.CreateOrGetRun(ctx, testRun(), unsafeIdem); !errors.Is(err, ports.ErrOrchestratorInvalidRecord) {
		t.Fatalf("raw idempotency key error = %v, want invalid record", err)
	}

	encoded, err := json.Marshal(testRun())
	if err != nil {
		t.Fatalf("marshal redacted record: %v", err)
	}
	if strings.Contains(string(encoded), "prompt") || strings.Contains(string(encoded), "apiKey") || strings.Contains(string(encoded), "sk-test") {
		t.Fatalf("redacted run record contains sensitive field/value: %s", encoded)
	}
}

var testTime = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

func testRun() ports.OrchestratorRunRecord {
	return ports.OrchestratorRunRecord{
		RunID: "run-1", State: ports.RunStatePending, TaskDigest: strings.Repeat("a", 64),
		CreatedAt: testTime, UpdatedAt: testTime,
	}
}

func testIdempotency() ports.OrchestratorIdempotencyRecord {
	return ports.OrchestratorIdempotencyRecord{
		KeyDigest: strings.Repeat("b", 64), RequestDigest: strings.Repeat("c", 64),
		RunID: "run-1", CreatedAt: testTime,
	}
}

func createTestRun(t *testing.T, store *ClaudeOrchestratorMemoryStore) {
	t.Helper()
	if _, created, err := store.CreateOrGetRun(context.Background(), testRun(), testIdempotency()); err != nil || !created {
		t.Fatalf("seed run = (created %v, err %v)", created, err)
	}
}
