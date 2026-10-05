package orchestratorstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCreateOrGetIsAtomicAndRetainsOnlyTokenDigest(t *testing.T) {
	store := NewExperimentalMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	key := []byte("0123456789abcdef")
	const retries = 32

	var wg sync.WaitGroup
	var mu sync.Mutex
	createdCount := 0
	ids := make(map[string]struct{})
	errs := make(chan error, retries)
	for i := 0; i < retries; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			record, created, err := store.CreateOrGet(ctx, ports.ProjectOrchestratorRunCreate{
				RunID:          fmt.Sprintf("run-%02d", i),
				ProjectID:      "project-a",
				IdempotencyKey: key,
				CreatedAt:      now,
			})
			if err != nil {
				errs <- err
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if created {
				createdCount++
			}
			ids[record.RunID] = struct{}{}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if createdCount != 1 || len(ids) != 1 {
		t.Fatalf("created=%d distinct run IDs=%d, want exactly one", createdCount, len(ids))
	}
	if len(store.idempotency) != 1 {
		t.Fatalf("idempotency entries=%d, want one digest", len(store.idempotency))
	}
	digest := sha256.Sum256(key)
	if _, ok := store.idempotency[digest]; !ok {
		t.Fatal("store did not index by SHA-256 digest")
	}
	if _, ok := store.idempotency[[sha256.Size]byte{}]; ok {
		t.Fatal("store contains unexpected raw-token-shaped value")
	}

	firstID := ""
	for id := range ids {
		firstID = id
	}
	replay, created, err := store.CreateOrGet(ctx, ports.ProjectOrchestratorRunCreate{
		RunID:          "a-different-proposed-run",
		ProjectID:      "project-a",
		IdempotencyKey: key,
		CreatedAt:      now.Add(time.Second),
	})
	if err != nil || created || replay.RunID != firstID {
		t.Fatalf("replay=(%+v, created=%v, err=%v), want original run", replay, created, err)
	}
	if replay.ProjectID != "project-a" || replay.State != ports.RunStatePending {
		t.Fatalf("replay metadata = %+v", replay)
	}
}

func TestIdempotencyTokenCannotCrossProjectScope(t *testing.T) {
	store := NewExperimentalMemoryStore()
	now := time.Now().UTC()
	key := []byte("0123456789abcdef")
	_, _, err := store.CreateOrGet(context.Background(), ports.ProjectOrchestratorRunCreate{
		RunID: "run-a", ProjectID: "project-a", IdempotencyKey: key, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, created, err := store.CreateOrGet(context.Background(), ports.ProjectOrchestratorRunCreate{
		RunID: "run-b", ProjectID: "project-b", IdempotencyKey: key, CreatedAt: now,
	})
	if !errors.Is(err, ports.ErrProjectOrchestratorIdempotencyScopeConflict) || created {
		t.Fatalf("cross-project reuse = (created=%v, err=%v), want scope conflict and no new run", created, err)
	}
	if _, err := store.Get(context.Background(), "project-b", "run-b"); !errors.Is(err, ports.ErrProjectOrchestratorRunNotFound) {
		t.Fatalf("cross-project retry created a run: %v", err)
	}
}

func TestProjectScopeLeaseReclaimAndFencing(t *testing.T) {
	store := NewExperimentalMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	_, _, err := store.CreateOrGet(ctx, ports.ProjectOrchestratorRunCreate{
		RunID: "run-1", ProjectID: "project-a", IdempotencyKey: []byte("0123456789abcdef"), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, claimed, err := store.Claim(ctx, "project-a", "run-1", now, time.Minute)
	if err != nil || !claimed || first.FencingToken != 1 {
		t.Fatalf("first claim=(%+v,%v,%v), want fence 1", first, claimed, err)
	}
	_, claimed, err = store.Claim(ctx, "project-a", "run-1", now.Add(30*time.Second), time.Minute)
	if err != nil || claimed {
		t.Fatalf("claim before expiry=(%v,%v), want unclaimed", claimed, err)
	}
	second, claimed, err := store.Claim(ctx, "project-a", "run-1", now.Add(time.Minute), time.Minute)
	if err != nil || !claimed || second.FencingToken != 2 {
		t.Fatalf("reclaim=(%+v,%v,%v), want fence 2", second, claimed, err)
	}
	if _, _, err := store.Transition(ctx, "project-a", "run-1", first.FencingToken, ports.RunStateCompleted, ports.ProjectOrchestratorSummarySucceeded, now.Add(time.Minute)); !errors.Is(err, ports.ErrProjectOrchestratorRunStaleFence) {
		t.Fatalf("old worker transition err=%v, want stale fence", err)
	}
	if _, err := store.Get(ctx, "project-b", "run-1"); !errors.Is(err, ports.ErrProjectOrchestratorRunNotFound) {
		t.Fatalf("cross-project get err=%v, want not found", err)
	}
	completed, changed, err := store.Transition(ctx, "project-a", "run-1", second.FencingToken, ports.RunStateCompleted, ports.ProjectOrchestratorSummarySucceeded, now.Add(time.Minute))
	if err != nil || !changed || completed.State != ports.RunStateCompleted {
		t.Fatalf("current worker transition=(%+v,%v,%v)", completed, changed, err)
	}
	events, err := store.Events(ctx, "project-a", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events=%d, want created/claimed/reclaimed/completed", len(events))
	}
	for i, event := range events {
		if event.Sequence != uint64(i+1) {
			t.Fatalf("event %d sequence=%d", i, event.Sequence)
		}
	}
	if events[2].Code != ports.ProjectOrchestratorEventReclaimed || events[3].FencingToken != 2 {
		t.Fatalf("reclaim/terminal event metadata incorrect: %+v", events)
	}
}

func TestCancellationInvalidatesFenceAndIsIdempotent(t *testing.T) {
	store := NewExperimentalMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	_, _, err := store.CreateOrGet(ctx, ports.ProjectOrchestratorRunCreate{
		RunID: "run-cancel", ProjectID: "project-a", IdempotencyKey: []byte("0123456789abcdef"), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.Claim(ctx, "project-a", "run-cancel", now, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim=(%v,%v)", ok, err)
	}
	canceled, changed, err := store.Cancel(ctx, "project-a", "run-cancel", now.Add(time.Second))
	if err != nil || !changed || canceled.State != ports.RunStateCanceled || !canceled.CancelRequested || canceled.FencingToken != claimed.FencingToken+1 || canceled.CanceledAt == nil {
		t.Fatalf("cancel=(%+v,%v,%v)", canceled, changed, err)
	}
	repeated, changed, err := store.Cancel(ctx, "project-a", "run-cancel", now.Add(2*time.Second))
	if err != nil || changed || repeated.FencingToken != canceled.FencingToken {
		t.Fatalf("repeated cancel=(%+v,%v,%v), want idempotent no-op", repeated, changed, err)
	}
	if _, _, err := store.Transition(ctx, "project-a", "run-cancel", claimed.FencingToken, ports.RunStateCompleted, ports.ProjectOrchestratorSummarySucceeded, now.Add(3*time.Second)); !errors.Is(err, ports.ErrProjectOrchestratorRunStaleFence) {
		t.Fatalf("late worker transition err=%v, want stale fence", err)
	}
	events, err := store.Events(ctx, "project-a", "run-cancel")
	if err != nil || len(events) != 3 || events[2].Code != ports.ProjectOrchestratorEventCanceled {
		t.Fatalf("events=(%+v,%v), want exactly one cancellation event", events, err)
	}
}

func TestOnlyAllowlistedSummaryCodesCanBeStored(t *testing.T) {
	store := NewExperimentalMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	_, _, err := store.CreateOrGet(ctx, ports.ProjectOrchestratorRunCreate{
		RunID: "run-safe", ProjectID: "project-a", IdempotencyKey: []byte("0123456789abcdef"), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, _, err := store.Claim(ctx, "project-a", "run-safe", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	secretLikeText := "Bearer sk-example task prompt must never persist"
	if _, _, err := store.Transition(ctx, "project-a", "run-safe", claimed.FencingToken, ports.RunStateFailed, ports.ProjectOrchestratorSummaryCode(secretLikeText), now.Add(time.Second)); !errors.Is(err, ports.ErrProjectOrchestratorRunInvalid) {
		t.Fatalf("arbitrary summary error=%v, want rejected", err)
	}
	record, err := store.Get(ctx, "project-a", "run-safe")
	if err != nil || record.State != ports.RunStateExecuting || record.SummaryCode != ports.ProjectOrchestratorSummaryNone {
		t.Fatalf("unsafe summary altered run: (%+v,%v)", record, err)
	}
	events, err := store.Events(ctx, "project-a", "run-safe")
	if err != nil || len(events) != 2 {
		t.Fatalf("unsafe summary emitted event: (%+v,%v)", events, err)
	}
}
