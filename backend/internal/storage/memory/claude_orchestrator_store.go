// Package memory contains process-local storage adapters intended for tests
// and development. It does not provide durability across a process exit.
package memory

import (
	"context"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ClaudeOrchestratorStore = (*ClaudeOrchestratorMemoryStore)(nil)

// ClaudeOrchestratorMemoryState is the shared backing state for one in-memory
// store. Reusing it when constructing a new store simulates an adapter/service
// restart in tests; it is not durable across process restarts.
type ClaudeOrchestratorMemoryState struct {
	mu            sync.RWMutex
	runs          map[string]ports.OrchestratorRunRecord
	idempotencies map[string]ports.OrchestratorIdempotencyRecord
	events        map[string][]ports.OrchestratorEvent
	eventIDs      map[string]struct{}
	cancellations map[string]ports.OrchestratorCancelState
	fences        map[string]ports.OrchestratorFenceLease
	fenceEpochs   map[string]uint64
}

// NewClaudeOrchestratorMemoryState creates an empty backing state.
func NewClaudeOrchestratorMemoryState() *ClaudeOrchestratorMemoryState {
	return &ClaudeOrchestratorMemoryState{
		runs:          make(map[string]ports.OrchestratorRunRecord),
		idempotencies: make(map[string]ports.OrchestratorIdempotencyRecord),
		events:        make(map[string][]ports.OrchestratorEvent),
		eventIDs:      make(map[string]struct{}),
		cancellations: make(map[string]ports.OrchestratorCancelState),
		fences:        make(map[string]ports.OrchestratorFenceLease),
		fenceEpochs:   make(map[string]uint64),
	}
}

// ClaudeOrchestratorMemoryStore implements the orchestration storage ports.
type ClaudeOrchestratorMemoryStore struct {
	state *ClaudeOrchestratorMemoryState
}

// NewClaudeOrchestratorMemoryStore creates a store with a fresh, process-local
// state.
func NewClaudeOrchestratorMemoryStore() *ClaudeOrchestratorMemoryStore {
	return NewClaudeOrchestratorMemoryStoreWithState(NewClaudeOrchestratorMemoryState())
}

// NewClaudeOrchestratorMemoryStoreWithState opens another store handle over
// shared process-local state. It is useful for restart-simulation tests.
func NewClaudeOrchestratorMemoryStoreWithState(state *ClaudeOrchestratorMemoryState) *ClaudeOrchestratorMemoryStore {
	if state == nil {
		state = NewClaudeOrchestratorMemoryState()
	}
	return &ClaudeOrchestratorMemoryStore{state: state}
}

// CreateOrGetRun atomically creates a run and binds it to digested idempotency
// input, or returns the prior run for a matching retry.
func (s *ClaudeOrchestratorMemoryStore) CreateOrGetRun(
	ctx context.Context,
	run ports.OrchestratorRunRecord,
	idempotency ports.OrchestratorIdempotencyRecord,
) (ports.OrchestratorRunRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.OrchestratorRunRecord{}, false, err
	}
	if !validRun(run) || !validDigest(idempotency.KeyDigest) || !validDigest(idempotency.RequestDigest) ||
		!validIdentifier(idempotency.RunID) || idempotency.RunID != run.RunID || idempotency.CreatedAt.IsZero() {
		return ports.OrchestratorRunRecord{}, false, ports.ErrOrchestratorInvalidRecord
	}

	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if previous, ok := s.state.idempotencies[idempotency.KeyDigest]; ok {
		if previous.RequestDigest != idempotency.RequestDigest {
			return ports.OrchestratorRunRecord{}, false, ports.ErrOrchestratorIdempotencyConflict
		}
		existing, ok := s.state.runs[previous.RunID]
		if !ok {
			return ports.OrchestratorRunRecord{}, false, ports.ErrOrchestratorInvalidRecord
		}
		return existing, false, nil
	}
	if _, exists := s.state.runs[run.RunID]; exists {
		return ports.OrchestratorRunRecord{}, false, ports.ErrOrchestratorRunConflict
	}
	s.state.runs[run.RunID] = run
	s.state.idempotencies[idempotency.KeyDigest] = idempotency
	return run, true, nil
}

// GetRun returns the redacted run record, if present.
func (s *ClaudeOrchestratorMemoryStore) GetRun(ctx context.Context, runID string) (ports.OrchestratorRunRecord, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.OrchestratorRunRecord{}, false, err
	}
	if !validIdentifier(runID) {
		return ports.OrchestratorRunRecord{}, false, ports.ErrOrchestratorInvalidRecord
	}
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	run, ok := s.state.runs[runID]
	return run, ok, nil
}

// UpdateRun applies a state transition only if the current state matches the
// caller's expected state. The run identity, digest, and creation time are
// immutable after idempotent admission.
func (s *ClaudeOrchestratorMemoryStore) UpdateRun(ctx context.Context, record ports.OrchestratorRunRecord, expectedState ports.RunState) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !validRun(record) || !validRunState(expectedState) {
		return false, ports.ErrOrchestratorInvalidRecord
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	current, ok := s.state.runs[record.RunID]
	if !ok {
		return false, ports.ErrOrchestratorRunNotFound
	}
	if current.State != expectedState {
		return false, nil
	}
	if current.TaskDigest != record.TaskDigest || !current.CreatedAt.Equal(record.CreatedAt) {
		return false, ports.ErrOrchestratorInvalidRecord
	}
	s.state.runs[record.RunID] = record
	return true, nil
}

// AppendEvent assigns the next per-run sequence and appends a sanitized event.
func (s *ClaudeOrchestratorMemoryStore) AppendEvent(ctx context.Context, event ports.OrchestratorEvent) (ports.OrchestratorEvent, error) {
	if err := ctx.Err(); err != nil {
		return ports.OrchestratorEvent{}, err
	}
	if !validEvent(event) || event.Sequence != 0 {
		return ports.OrchestratorEvent{}, ports.ErrOrchestratorInvalidRecord
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if _, ok := s.state.runs[event.RunID]; !ok {
		return ports.OrchestratorEvent{}, ports.ErrOrchestratorRunNotFound
	}
	if _, exists := s.state.eventIDs[event.EventID]; exists {
		return ports.OrchestratorEvent{}, ports.ErrOrchestratorEventConflict
	}
	event.Sequence = uint64(len(s.state.events[event.RunID]) + 1)
	s.state.events[event.RunID] = append(s.state.events[event.RunID], event)
	s.state.eventIDs[event.EventID] = struct{}{}
	return event, nil
}

// ListEvents returns ordered events strictly after afterSequence.
func (s *ClaudeOrchestratorMemoryStore) ListEvents(ctx context.Context, runID string, afterSequence uint64, limit int) ([]ports.OrchestratorEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validIdentifier(runID) || limit <= 0 {
		return nil, ports.ErrOrchestratorInvalidRecord
	}
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	if _, ok := s.state.runs[runID]; !ok {
		return nil, ports.ErrOrchestratorRunNotFound
	}
	events := s.state.events[runID]
	start := int(afterSequence)
	if uint64(start) != afterSequence {
		return []ports.OrchestratorEvent{}, nil
	}
	if start >= len(events) {
		return []ports.OrchestratorEvent{}, nil
	}
	end := start + limit
	if end > len(events) {
		end = len(events)
	}
	return append([]ports.OrchestratorEvent(nil), events[start:end]...), nil
}

// RequestCancel persists the first allowed cancellation request. Subsequent
// requests are idempotent and cannot replace the original timestamp or reason.
func (s *ClaudeOrchestratorMemoryStore) RequestCancel(ctx context.Context, runID string, at time.Time, reason ports.OrchestratorCancelReason) (ports.OrchestratorCancelState, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.OrchestratorCancelState{}, false, err
	}
	if !validIdentifier(runID) || at.IsZero() || !validCancelReason(reason) {
		return ports.OrchestratorCancelState{}, false, ports.ErrOrchestratorInvalidRecord
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if _, ok := s.state.runs[runID]; !ok {
		return ports.OrchestratorCancelState{}, false, ports.ErrOrchestratorRunNotFound
	}
	if existing, ok := s.state.cancellations[runID]; ok {
		return existing, false, nil
	}
	state := ports.OrchestratorCancelState{Requested: true, RequestedAt: at, Reason: reason}
	s.state.cancellations[runID] = state
	return state, true, nil
}

// GetCancelState returns the stored request or its zero (not requested) state.
func (s *ClaudeOrchestratorMemoryStore) GetCancelState(ctx context.Context, runID string) (ports.OrchestratorCancelState, error) {
	if err := ctx.Err(); err != nil {
		return ports.OrchestratorCancelState{}, err
	}
	if !validIdentifier(runID) {
		return ports.OrchestratorCancelState{}, ports.ErrOrchestratorInvalidRecord
	}
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	if _, ok := s.state.runs[runID]; !ok {
		return ports.OrchestratorCancelState{}, ports.ErrOrchestratorRunNotFound
	}
	return s.state.cancellations[runID], nil
}

// AcquireFence returns a new epoch when the previous lease is absent or expired.
// A live lease is never silently stolen, including by the same owner digest.
func (s *ClaudeOrchestratorMemoryStore) AcquireFence(ctx context.Context, runID, ownerDigest string, now, expiresAt time.Time) (ports.OrchestratorFenceLease, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.OrchestratorFenceLease{}, false, err
	}
	if !validIdentifier(runID) || !validDigest(ownerDigest) || now.IsZero() || !expiresAt.After(now) {
		return ports.OrchestratorFenceLease{}, false, ports.ErrOrchestratorInvalidRecord
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if _, ok := s.state.runs[runID]; !ok {
		return ports.OrchestratorFenceLease{}, false, ports.ErrOrchestratorRunNotFound
	}
	if current, ok := s.state.fences[runID]; ok && current.ExpiresAt.After(now) {
		return current, false, nil
	}
	epoch := s.state.fenceEpochs[runID] + 1
	lease := ports.OrchestratorFenceLease{RunID: runID, OwnerDigest: ownerDigest, Epoch: epoch, ExpiresAt: expiresAt}
	s.state.fenceEpochs[runID] = epoch
	s.state.fences[runID] = lease
	return lease, true, nil
}

// ValidateFence rejects expired, superseded, or mismatched fencing tokens.
func (s *ClaudeOrchestratorMemoryStore) ValidateFence(ctx context.Context, lease ports.OrchestratorFenceLease, now time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !validIdentifier(lease.RunID) || !validDigest(lease.OwnerDigest) || lease.Epoch == 0 || lease.ExpiresAt.IsZero() || now.IsZero() {
		return false, ports.ErrOrchestratorInvalidRecord
	}
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	if _, ok := s.state.runs[lease.RunID]; !ok {
		return false, ports.ErrOrchestratorRunNotFound
	}
	current, ok := s.state.fences[lease.RunID]
	return ok && current == lease && now.Before(lease.ExpiresAt), nil
}

func validRun(record ports.OrchestratorRunRecord) bool {
	return validIdentifier(record.RunID) && validRunState(record.State) && validDigest(record.TaskDigest) &&
		!record.CreatedAt.IsZero() && !record.UpdatedAt.IsZero() && !record.UpdatedAt.Before(record.CreatedAt)
}

func validRunState(state ports.RunState) bool {
	switch state {
	case ports.RunStatePending, ports.RunStatePlanning, ports.RunStateExecuting, ports.RunStateValidating,
		ports.RunStateReviewing, ports.RunStateCompleted, ports.RunStateHeld, ports.RunStateFailed:
		return true
	default:
		return false
	}
}

func validEvent(event ports.OrchestratorEvent) bool {
	if !validIdentifier(event.EventID) || !validIdentifier(event.RunID) || event.OccurredAt.IsZero() {
		return false
	}
	switch event.Kind {
	case ports.OrchestratorEventRunCreated, ports.OrchestratorEventCancelRequested, ports.OrchestratorEventFenceAcquired:
		return event.State == "" || validRunState(event.State)
	case ports.OrchestratorEventStateChanged:
		return validRunState(event.State)
	default:
		return false
	}
}

func validCancelReason(reason ports.OrchestratorCancelReason) bool {
	switch reason {
	case ports.OrchestratorCancelUserRequest, ports.OrchestratorCancelTimeout, ports.OrchestratorCancelShutdown:
		return true
	default:
		return false
	}
}

func validDigest(digest string) bool {
	if len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
