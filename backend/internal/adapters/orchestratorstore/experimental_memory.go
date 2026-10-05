// Package orchestratorstore contains an experimental in-memory implementation
// of the orchestrator lifecycle port. It is volatile and is not wired into the
// service or presented as durable storage.
package orchestratorstore

import (
	"context"
	"crypto/sha256"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const minIdempotencyKeyBytes = 16

// ExperimentalMemoryStore implements the safe metadata subset of
// ports.ProjectOrchestratorRunStore. It must not be used where restart durability,
// multi-process coordination, or approved CDC projection is required.
type ExperimentalMemoryStore struct {
	mu          sync.RWMutex
	runs        map[string]ports.ProjectOrchestratorRunSnapshot
	events      map[string][]ports.ProjectOrchestratorRunEvent
	idempotency map[[sha256.Size]byte]string
}

var _ ports.ProjectOrchestratorRunStore = (*ExperimentalMemoryStore)(nil)

// NewExperimentalMemoryStore constructs an empty volatile adapter.
func NewExperimentalMemoryStore() *ExperimentalMemoryStore {
	return &ExperimentalMemoryStore{
		runs:        make(map[string]ports.ProjectOrchestratorRunSnapshot),
		events:      make(map[string][]ports.ProjectOrchestratorRunEvent),
		idempotency: make(map[[sha256.Size]byte]string),
	}
}

// CreateOrGet hashes the token before retaining any state. A retry with the
// same token returns the original run and never creates another dispatchable
// record. The caller remains responsible for authorization of project scope.
func (s *ExperimentalMemoryStore) CreateOrGet(ctx context.Context, request ports.ProjectOrchestratorRunCreate) (ports.ProjectOrchestratorRunSnapshot, bool, error) {
	if err := contextError(ctx); err != nil {
		return ports.ProjectOrchestratorRunSnapshot{}, false, err
	}
	if s == nil || !validID(request.RunID) || !validID(request.ProjectID) || len(request.IdempotencyKey) < minIdempotencyKeyBytes || request.CreatedAt.IsZero() {
		return ports.ProjectOrchestratorRunSnapshot{}, false, ports.ErrProjectOrchestratorRunInvalid
	}
	digest := sha256.Sum256(request.IdempotencyKey)
	key := runKey(request.ProjectID, request.RunID)

	s.mu.Lock()
	defer s.mu.Unlock()
	if existingKey, exists := s.idempotency[digest]; exists {
		existing := s.runs[existingKey]
		if existing.ProjectID != request.ProjectID {
			return ports.ProjectOrchestratorRunSnapshot{}, false, ports.ErrProjectOrchestratorIdempotencyScopeConflict
		}
		return cloneRecord(existing), false, nil
	}
	if _, exists := s.runs[key]; exists {
		return ports.ProjectOrchestratorRunSnapshot{}, false, ports.ErrProjectOrchestratorRunConflict
	}
	record := ports.ProjectOrchestratorRunSnapshot{
		RunID:       request.RunID,
		ProjectID:   request.ProjectID,
		State:       ports.RunStatePending,
		SummaryCode: ports.ProjectOrchestratorSummaryNone,
		CreatedAt:   request.CreatedAt,
		UpdatedAt:   request.CreatedAt,
	}
	s.runs[key] = record
	s.idempotency[digest] = key
	s.appendEventLocked(key, record, ports.ProjectOrchestratorEventCreated, request.CreatedAt)
	return cloneRecord(record), true, nil
}

// Claim takes a pending run or reclaims an expired lease. Every claim advances
// the fencing token, so work from a previous lease cannot commit later.
func (s *ExperimentalMemoryStore) Claim(ctx context.Context, projectID, runID string, now time.Time, lease time.Duration) (ports.ProjectOrchestratorRunSnapshot, bool, error) {
	if err := contextError(ctx); err != nil {
		return ports.ProjectOrchestratorRunSnapshot{}, false, err
	}
	if s == nil || !validID(projectID) || !validID(runID) || now.IsZero() || lease <= 0 {
		return ports.ProjectOrchestratorRunSnapshot{}, false, ports.ErrProjectOrchestratorRunInvalid
	}
	key := runKey(projectID, runID)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.runs[key]
	if !exists {
		return ports.ProjectOrchestratorRunSnapshot{}, false, ports.ErrProjectOrchestratorRunNotFound
	}
	if record.CancelRequested || terminal(record.State) {
		return cloneRecord(record), false, nil
	}
	code := ports.ProjectOrchestratorEventClaimed
	if record.State == ports.RunStateExecuting {
		if record.LeaseExpiresAt == nil || now.Before(*record.LeaseExpiresAt) {
			return cloneRecord(record), false, nil
		}
		code = ports.ProjectOrchestratorEventReclaimed
	}
	if record.FencingToken == ^uint64(0) {
		return cloneRecord(record), false, ports.ErrProjectOrchestratorRunConflict
	}
	record.FencingToken++
	record.State = ports.RunStateExecuting
	record.UpdatedAt = now
	expires := now.Add(lease)
	record.LeaseExpiresAt = &expires
	s.runs[key] = record
	s.appendEventLocked(key, record, code, now)
	return cloneRecord(record), true, nil
}

// Transition records a terminal worker result only for the current fencing
// token. Only fixed summary codes are accepted; arbitrary text is rejected.
func (s *ExperimentalMemoryStore) Transition(ctx context.Context, projectID, runID string, fencingToken uint64, state ports.RunState, summary ports.ProjectOrchestratorSummaryCode, now time.Time) (ports.ProjectOrchestratorRunSnapshot, bool, error) {
	if err := contextError(ctx); err != nil {
		return ports.ProjectOrchestratorRunSnapshot{}, false, err
	}
	if s == nil || !validID(projectID) || !validID(runID) || now.IsZero() || !terminalTarget(state) || !validSummary(summary) {
		return ports.ProjectOrchestratorRunSnapshot{}, false, ports.ErrProjectOrchestratorRunInvalid
	}
	key := runKey(projectID, runID)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.runs[key]
	if !exists {
		return ports.ProjectOrchestratorRunSnapshot{}, false, ports.ErrProjectOrchestratorRunNotFound
	}
	if fencingToken != record.FencingToken {
		return cloneRecord(record), false, ports.ErrProjectOrchestratorRunStaleFence
	}
	if record.State == state && terminal(record.State) {
		return cloneRecord(record), false, nil
	}
	if record.State != ports.RunStateExecuting || terminal(record.State) || record.CancelRequested {
		return cloneRecord(record), false, nil
	}
	record.State = state
	record.SummaryCode = summary
	record.UpdatedAt = now
	record.LeaseExpiresAt = nil
	s.runs[key] = record
	s.appendEventLocked(key, record, eventCode(state), now)
	return cloneRecord(record), true, nil
}

// Cancel is an idempotent terminal state transition. Incrementing the fence
// invalidates any worker holding the prior token before returning.
func (s *ExperimentalMemoryStore) Cancel(ctx context.Context, projectID, runID string, now time.Time) (ports.ProjectOrchestratorRunSnapshot, bool, error) {
	if err := contextError(ctx); err != nil {
		return ports.ProjectOrchestratorRunSnapshot{}, false, err
	}
	if s == nil || !validID(projectID) || !validID(runID) || now.IsZero() {
		return ports.ProjectOrchestratorRunSnapshot{}, false, ports.ErrProjectOrchestratorRunInvalid
	}
	key := runKey(projectID, runID)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.runs[key]
	if !exists {
		return ports.ProjectOrchestratorRunSnapshot{}, false, ports.ErrProjectOrchestratorRunNotFound
	}
	if terminal(record.State) {
		return cloneRecord(record), false, nil
	}
	if record.FencingToken == ^uint64(0) {
		return cloneRecord(record), false, ports.ErrProjectOrchestratorRunConflict
	}
	record.FencingToken++
	record.State = ports.RunStateCanceled
	record.CancelRequested = true
	record.SummaryCode = ports.ProjectOrchestratorSummaryCanceled
	record.UpdatedAt = now
	record.LeaseExpiresAt = nil
	canceledAt := now
	record.CanceledAt = &canceledAt
	s.runs[key] = record
	s.appendEventLocked(key, record, ports.ProjectOrchestratorEventCanceled, now)
	return cloneRecord(record), true, nil
}

// Get returns a snapshot only within the requested project scope.
func (s *ExperimentalMemoryStore) Get(ctx context.Context, projectID, runID string) (ports.ProjectOrchestratorRunSnapshot, error) {
	if err := contextError(ctx); err != nil {
		return ports.ProjectOrchestratorRunSnapshot{}, err
	}
	if s == nil || !validID(projectID) || !validID(runID) {
		return ports.ProjectOrchestratorRunSnapshot{}, ports.ErrProjectOrchestratorRunInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, exists := s.runs[runKey(projectID, runID)]
	if !exists {
		return ports.ProjectOrchestratorRunSnapshot{}, ports.ErrProjectOrchestratorRunNotFound
	}
	return cloneRecord(record), nil
}

// Events returns a defensive copy of the private, metadata-only event history.
func (s *ExperimentalMemoryStore) Events(ctx context.Context, projectID, runID string) ([]ports.ProjectOrchestratorRunEvent, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if s == nil || !validID(projectID) || !validID(runID) {
		return nil, ports.ErrProjectOrchestratorRunInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := runKey(projectID, runID)
	if _, exists := s.runs[key]; !exists {
		return nil, ports.ErrProjectOrchestratorRunNotFound
	}
	return append([]ports.ProjectOrchestratorRunEvent(nil), s.events[key]...), nil
}

func (s *ExperimentalMemoryStore) appendEventLocked(key string, record ports.ProjectOrchestratorRunSnapshot, code ports.ProjectOrchestratorEventCode, now time.Time) {
	sequence := uint64(len(s.events[key]) + 1)
	s.events[key] = append(s.events[key], ports.ProjectOrchestratorRunEvent{
		RunID:        record.RunID,
		ProjectID:    record.ProjectID,
		Sequence:     sequence,
		Code:         code,
		State:        record.State,
		FencingToken: record.FencingToken,
		SummaryCode:  record.SummaryCode,
		CreatedAt:    now,
	})
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return ports.ErrProjectOrchestratorRunInvalid
	}
	return ctx.Err()
}

func validID(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00')
}

func validSummary(code ports.ProjectOrchestratorSummaryCode) bool {
	switch code {
	case ports.ProjectOrchestratorSummaryNone, ports.ProjectOrchestratorSummarySucceeded, ports.ProjectOrchestratorSummaryHeld, ports.ProjectOrchestratorSummaryFailed, ports.ProjectOrchestratorSummaryCanceled:
		return true
	default:
		return false
	}
}

func terminalTarget(state ports.RunState) bool {
	return state == ports.RunStateCompleted || state == ports.RunStateHeld || state == ports.RunStateFailed
}

func terminal(state ports.RunState) bool {
	return terminalTarget(state) || state == ports.RunStateCanceled
}

func eventCode(state ports.RunState) ports.ProjectOrchestratorEventCode {
	switch state {
	case ports.RunStateCompleted:
		return ports.ProjectOrchestratorEventCompleted
	case ports.RunStateHeld:
		return ports.ProjectOrchestratorEventHeld
	default:
		return ports.ProjectOrchestratorEventFailed
	}
}

func runKey(projectID, runID string) string {
	return projectID + "\x00" + runID
}

func cloneRecord(record ports.ProjectOrchestratorRunSnapshot) ports.ProjectOrchestratorRunSnapshot {
	if record.CanceledAt != nil {
		value := *record.CanceledAt
		record.CanceledAt = &value
	}
	if record.LeaseExpiresAt != nil {
		value := *record.LeaseExpiresAt
		record.LeaseExpiresAt = &value
	}
	return record
}
