package ports

import (
	"context"
	"errors"
	"time"
)

// Errors returned by orchestrator run stores.
var (
	ErrOrchestratorRunNotFound         = errors.New("orchestrator run not found")
	ErrOrchestratorRunConflict         = errors.New("orchestrator run already exists")
	ErrOrchestratorIdempotencyConflict = errors.New("orchestrator idempotency key reused for a different request")
	ErrOrchestratorEventConflict       = errors.New("orchestrator event already exists")
	ErrOrchestratorInvalidRecord       = errors.New("invalid orchestrator persistence record")
)

// OrchestratorRunRecord contains only bounded operational metadata. TaskDigest
// is a SHA-256 digest; this record deliberately has no prompt, task text,
// provider response, API key, or arbitrary error/detail field.
type OrchestratorRunRecord struct {
	RunID      string
	State      RunState
	TaskDigest string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// OrchestratorEventKind is a closed set of audit facts that do not carry
// prompts, provider output, credentials, or free-form diagnostic messages.
type OrchestratorEventKind string

// Kinds of orchestrator run events.
const (
	OrchestratorEventRunCreated      OrchestratorEventKind = "run_created"
	OrchestratorEventStateChanged    OrchestratorEventKind = "state_changed"
	OrchestratorEventCancelRequested OrchestratorEventKind = "cancel_requested"
	OrchestratorEventFenceAcquired   OrchestratorEventKind = "fence_acquired"
)

// OrchestratorEvent is a sanitized append-only event. Sequence is assigned by
// the store; callers must submit it as zero. State may be empty for events that
// do not represent a lifecycle transition.
type OrchestratorEvent struct {
	EventID    string
	RunID      string
	Sequence   uint64
	Kind       OrchestratorEventKind
	State      RunState
	OccurredAt time.Time
}

// OrchestratorIdempotencyRecord stores digests only; raw idempotency keys and
// raw request/prompt content must never cross this storage boundary.
type OrchestratorIdempotencyRecord struct {
	KeyDigest     string
	RequestDigest string
	RunID         string
	CreatedAt     time.Time
}

// OrchestratorCancelReason is an allowlisted cancellation category, not a
// caller-provided message.
type OrchestratorCancelReason string

// Reasons an orchestrator run was canceled.
const (
	OrchestratorCancelUserRequest OrchestratorCancelReason = "user_request"
	OrchestratorCancelTimeout     OrchestratorCancelReason = "timeout"
	OrchestratorCancelShutdown    OrchestratorCancelReason = "shutdown"
)

// OrchestratorCancelState is monotonic: a run can transition from not requested
// to requested, but cancellation is not cleared by this service contract.
type OrchestratorCancelState struct {
	Requested   bool
	RequestedAt time.Time
	Reason      OrchestratorCancelReason
}

// OrchestratorFenceLease is an epoch-based ownership token. OwnerDigest is a
// digest rather than a raw worker/process identity; stale epochs are rejected.
type OrchestratorFenceLease struct {
	RunID       string
	OwnerDigest string
	Epoch       uint64
	ExpiresAt   time.Time
}

// OrchestratorRunStore reads and compare-and-swaps redacted run records.
type OrchestratorRunStore interface {
	GetRun(ctx context.Context, runID string) (OrchestratorRunRecord, bool, error)
	UpdateRun(ctx context.Context, record OrchestratorRunRecord, expectedState RunState) (bool, error)
}

// OrchestratorIdempotencyStore atomically records an idempotency digest and its
// run, returning the existing run for an identical retry and rejecting key
// reuse with a different request digest.
type OrchestratorIdempotencyStore interface {
	CreateOrGetRun(ctx context.Context, run OrchestratorRunRecord, idempotency OrchestratorIdempotencyRecord) (OrchestratorRunRecord, bool, error)
}

// OrchestratorEventStore appends and pages sanitized run events in sequence.
type OrchestratorEventStore interface {
	AppendEvent(ctx context.Context, event OrchestratorEvent) (OrchestratorEvent, error)
	ListEvents(ctx context.Context, runID string, afterSequence uint64, limit int) ([]OrchestratorEvent, error)
}

// OrchestratorCancellationStore persists a one-way cancel request separately
// from lifecycle state so that it can be checked at execution boundaries.
type OrchestratorCancellationStore interface {
	RequestCancel(ctx context.Context, runID string, at time.Time, reason OrchestratorCancelReason) (OrchestratorCancelState, bool, error)
	GetCancelState(ctx context.Context, runID string) (OrchestratorCancelState, error)
}

// OrchestratorFencingStore issues monotonically increasing per-run lease
// epochs. A new lease can be acquired only after the previous one expires.
type OrchestratorFencingStore interface {
	AcquireFence(ctx context.Context, runID, ownerDigest string, now, expiresAt time.Time) (OrchestratorFenceLease, bool, error)
	ValidateFence(ctx context.Context, lease OrchestratorFenceLease, now time.Time) (bool, error)
}

// ClaudeOrchestratorStore is the aggregate persistence boundary required by a
// durable orchestration service. The initial in-memory adapter is a test/dev
// implementation; SQLite persistence is intentionally a separate follow-up.
type ClaudeOrchestratorStore interface {
	OrchestratorRunStore
	OrchestratorIdempotencyStore
	OrchestratorEventStore
	OrchestratorCancellationStore
	OrchestratorFencingStore
}
