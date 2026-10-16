package ports

import (
	"context"
	"errors"
	"time"
)

// Errors returned by project orchestrator run stores.
var (
	ErrProjectOrchestratorRunInvalid               = errors.New("invalid orchestrator run metadata")
	ErrProjectOrchestratorRunNotFound              = errors.New("orchestrator run not found")
	ErrProjectOrchestratorRunConflict              = errors.New("orchestrator run conflicts with existing metadata")
	ErrProjectOrchestratorRunStaleFence            = errors.New("orchestrator run fencing token is stale")
	ErrProjectOrchestratorIdempotencyScopeConflict = errors.New("idempotency token is already bound to another project")
)

// ProjectOrchestratorSummaryCode is a closed, non-text summary classification. It is
// intentionally not a place to persist summaries, prompts, errors, or outputs.
type ProjectOrchestratorSummaryCode string

// Summary codes recorded for a project orchestrator run.
const (
	ProjectOrchestratorSummaryNone      ProjectOrchestratorSummaryCode = "none"
	ProjectOrchestratorSummarySucceeded ProjectOrchestratorSummaryCode = "succeeded"
	ProjectOrchestratorSummaryHeld      ProjectOrchestratorSummaryCode = "held"
	ProjectOrchestratorSummaryFailed    ProjectOrchestratorSummaryCode = "failed"
	ProjectOrchestratorSummaryCanceled  ProjectOrchestratorSummaryCode = "canceled"
)

// ProjectOrchestratorEventCode is the allowlisted lifecycle vocabulary for private
// store history. These values are metadata, not user-provided descriptions.
type ProjectOrchestratorEventCode string

// Event codes recorded for a project orchestrator run.
const (
	ProjectOrchestratorEventCreated   ProjectOrchestratorEventCode = "created"
	ProjectOrchestratorEventClaimed   ProjectOrchestratorEventCode = "claimed"
	ProjectOrchestratorEventReclaimed ProjectOrchestratorEventCode = "reclaimed"
	ProjectOrchestratorEventCompleted ProjectOrchestratorEventCode = "completed"
	ProjectOrchestratorEventHeld      ProjectOrchestratorEventCode = "held"
	ProjectOrchestratorEventFailed    ProjectOrchestratorEventCode = "failed"
	ProjectOrchestratorEventCanceled  ProjectOrchestratorEventCode = "canceled"
)

// ProjectOrchestratorRunCreate contains only opaque identity and an idempotency token.
// Implementations must hash IdempotencyKey immediately and never retain it.
// The token must contain at least 128 random bits. There are intentionally no
// task, prompt, provider, credential, or worker-output fields in this contract.
type ProjectOrchestratorRunCreate struct {
	RunID          string
	ProjectID      string
	IdempotencyKey []byte
	CreatedAt      time.Time
}

// ProjectOrchestratorRunSnapshot is a safe lifecycle snapshot. SummaryCode is a closed
// enum; no arbitrary or redacted free text is persisted by this interface.
type ProjectOrchestratorRunSnapshot struct {
	RunID           string
	ProjectID       string
	State           RunState
	FencingToken    uint64
	CancelRequested bool
	CanceledAt      *time.Time
	LeaseExpiresAt  *time.Time
	SummaryCode     ProjectOrchestratorSummaryCode
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ProjectOrchestratorRunEvent is an append-only, minimal lifecycle projection. It
// contains no summary text, request data, credentials, or provider payload.
type ProjectOrchestratorRunEvent struct {
	RunID        string
	ProjectID    string
	Sequence     uint64
	Code         ProjectOrchestratorEventCode
	State        RunState
	FencingToken uint64
	SummaryCode  ProjectOrchestratorSummaryCode
	CreatedAt    time.Time
}

// ProjectOrchestratorRunStore is the persistence boundary for orchestrator lifecycle
// metadata. CreateOrGet must be atomic and hash the high-entropy token; Claim
// must increment fencing tokens on initial claim and lease reclaim; transitions
// and cancellation must atomically update the snapshot and append one event.
// Implementations must scope all reads/writes to projectID. The experimental
// in-memory adapter is volatile and must not be mistaken for durable storage.
type ProjectOrchestratorRunStore interface {
	CreateOrGet(ctx context.Context, request ProjectOrchestratorRunCreate) (record ProjectOrchestratorRunSnapshot, created bool, err error)
	Claim(ctx context.Context, projectID, runID string, now time.Time, lease time.Duration) (record ProjectOrchestratorRunSnapshot, claimed bool, err error)
	Transition(ctx context.Context, projectID, runID string, fencingToken uint64, state RunState, summary ProjectOrchestratorSummaryCode, now time.Time) (record ProjectOrchestratorRunSnapshot, changed bool, err error)
	Cancel(ctx context.Context, projectID, runID string, now time.Time) (record ProjectOrchestratorRunSnapshot, changed bool, err error)
	Get(ctx context.Context, projectID, runID string) (record ProjectOrchestratorRunSnapshot, err error)
	Events(ctx context.Context, projectID, runID string) (events []ProjectOrchestratorRunEvent, err error)
}
