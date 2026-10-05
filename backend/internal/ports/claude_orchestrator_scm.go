package ports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ClaudeOrchestratorSCMWriteAction identifies one externally visible SCM write.
type ClaudeOrchestratorSCMWriteAction string

const (
	ClaudeOrchestratorSCMCreateBranch ClaudeOrchestratorSCMWriteAction = "create_branch"
	ClaudeOrchestratorSCMPush         ClaudeOrchestratorSCMWriteAction = "push"
	ClaudeOrchestratorSCMCreatePR     ClaudeOrchestratorSCMWriteAction = "create_pull_request"
)

// ClaudeOrchestratorSCMWriteApproval is an explicit, action-scoped approval for
// one SCM write request. Its zero value grants no authorization.
type ClaudeOrchestratorSCMWriteApproval struct {
	Action   ClaudeOrchestratorSCMWriteAction
	Approved bool
}

// ClaudeOrchestratorSCMWriteRejectionReason describes why a write was denied.
type ClaudeOrchestratorSCMWriteRejectionReason string

const (
	ClaudeOrchestratorSCMWritesDisabled         ClaudeOrchestratorSCMWriteRejectionReason = "writes_disabled"
	ClaudeOrchestratorSCMApprovalRequired       ClaudeOrchestratorSCMWriteRejectionReason = "explicit_approval_required"
	ClaudeOrchestratorSCMApprovalActionMismatch ClaudeOrchestratorSCMWriteRejectionReason = "approval_action_mismatch"
)

// ClaudeOrchestratorSCMWriteRejectedError reports a default-deny or approval
// failure at the external SCM write boundary.
type ClaudeOrchestratorSCMWriteRejectedError struct {
	Action ClaudeOrchestratorSCMWriteAction
	Reason ClaudeOrchestratorSCMWriteRejectionReason
}

func (e *ClaudeOrchestratorSCMWriteRejectedError) Error() string {
	return fmt.Sprintf("Claude orchestrator SCM write %q rejected: %s", e.Action, e.Reason)
}

// ClaudeOrchestratorSCMWritePolicy is a default-deny authorization check.
// Enabling writes is not sufficient: every write still needs a matching,
// explicit approval on that operation's request.
type ClaudeOrchestratorSCMWritePolicy struct {
	WritesEnabled bool
}

// Authorize rejects writes unless the policy is enabled and the request carries
// affirmative approval for exactly the requested action.
func (p ClaudeOrchestratorSCMWritePolicy) Authorize(approval ClaudeOrchestratorSCMWriteApproval, action ClaudeOrchestratorSCMWriteAction) error {
	if !p.WritesEnabled {
		return &ClaudeOrchestratorSCMWriteRejectedError{Action: action, Reason: ClaudeOrchestratorSCMWritesDisabled}
	}
	if !approval.Approved {
		return &ClaudeOrchestratorSCMWriteRejectedError{Action: action, Reason: ClaudeOrchestratorSCMApprovalRequired}
	}
	if approval.Action != action {
		return &ClaudeOrchestratorSCMWriteRejectedError{Action: action, Reason: ClaudeOrchestratorSCMApprovalActionMismatch}
	}
	return nil
}

// ClaudeOrchestratorSCMCreateBranchRequest describes a branch creation. The
// adapter must authorize Approval before making any external change.
type ClaudeOrchestratorSCMCreateBranchRequest struct {
	Repository SCMRepo
	BranchName string
	BaseBranch string
	BaseSHA    string
	Approval   ClaudeOrchestratorSCMWriteApproval
}

// ClaudeOrchestratorSCMBranchResult identifies the created branch head.
type ClaudeOrchestratorSCMBranchResult struct {
	BranchName string
	CommitSHA  string
}

// ClaudeOrchestratorSCMPushRequest describes publishing an existing commit to a
// branch. Approval is independent from any branch-creation approval.
type ClaudeOrchestratorSCMPushRequest struct {
	Repository SCMRepo
	BranchName string
	CommitSHA  string
	Approval   ClaudeOrchestratorSCMWriteApproval
}

// ClaudeOrchestratorSCMPushResult identifies the published branch head.
type ClaudeOrchestratorSCMPushResult struct {
	BranchName string
	CommitSHA  string
}

// ClaudeOrchestratorSCMCreatePullRequestRequest describes opening a PR. A PR
// creation is a separate external write and requires its own approval.
type ClaudeOrchestratorSCMCreatePullRequestRequest struct {
	Repository SCMRepo
	HeadBranch string
	BaseBranch string
	Title      string
	Body       string
	Draft      bool
	Approval   ClaudeOrchestratorSCMWriteApproval
}

// ClaudeOrchestratorSCMCreatePullRequestResult identifies the created PR using
// the existing provider-neutral SCM reference contract.
type ClaudeOrchestratorSCMCreatePullRequestResult struct {
	PR SCMPRRef
}

// ClaudeOrchestratorSCMChecksRequest asks for check facts for one PR head.
type ClaudeOrchestratorSCMChecksRequest struct {
	PR      SCMPRRef
	HeadSHA string
}

// ClaudeOrchestratorSCMChecksResult returns normalized checks for the requested
// PR head. Check details reuse the existing provider-neutral observation type.
type ClaudeOrchestratorSCMChecksResult struct {
	HeadSHA string
	Checks  []SCMCheckObservation
}

// ClaudeOrchestratorSCM is the typed SCM boundary used by the orchestrator.
// Implementations must authorize each write request before performing it and
// must not make real SCM calls unless explicitly implemented by an adapter.
// Authentication is explicitly injected into an adapter; this contract does
// not read process environment variables or discover credentials automatically.
type ClaudeOrchestratorSCM interface {
	CreateBranch(ctx context.Context, request ClaudeOrchestratorSCMCreateBranchRequest) (ClaudeOrchestratorSCMBranchResult, error)
	Push(ctx context.Context, request ClaudeOrchestratorSCMPushRequest) (ClaudeOrchestratorSCMPushResult, error)
	CreatePullRequest(ctx context.Context, request ClaudeOrchestratorSCMCreatePullRequestRequest) (ClaudeOrchestratorSCMCreatePullRequestResult, error)
	GetChecks(ctx context.Context, request ClaudeOrchestratorSCMChecksRequest) (ClaudeOrchestratorSCMChecksResult, error)
}

// ClaudeOrchestratorSCMCredential holds a credential supplied explicitly by the
// composition root. Its formatting and serialization are always redacted; call
// Value only at the adapter boundary that needs the raw secret.
type ClaudeOrchestratorSCMCredential struct {
	value string
}

// NewClaudeOrchestratorSCMCredential wraps an explicitly supplied secret. It
// does not inspect environment variables or any other implicit credential store.
func NewClaudeOrchestratorSCMCredential(value string) ClaudeOrchestratorSCMCredential {
	return ClaudeOrchestratorSCMCredential{value: value}
}

// Value returns the secret for deliberate use by an injected SCM adapter.
func (c ClaudeOrchestratorSCMCredential) Value() string {
	return c.value
}

// String redacts credentials from ordinary string formatting.
func (ClaudeOrchestratorSCMCredential) String() string {
	return "[REDACTED]"
}

// GoString redacts credentials from Go-syntax formatting.
func (ClaudeOrchestratorSCMCredential) GoString() string {
	return "[REDACTED]"
}

// Format redacts credentials for every fmt formatting verb.
func (ClaudeOrchestratorSCMCredential) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[REDACTED]")
}

// MarshalJSON prevents accidental credential disclosure in JSON diagnostics.
func (ClaudeOrchestratorSCMCredential) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED]")
}

// MarshalText prevents accidental credential disclosure in text serialization.
func (ClaudeOrchestratorSCMCredential) MarshalText() ([]byte, error) {
	return []byte("[REDACTED]"), nil
}

// ErrClaudeOrchestratorSCMWriteRejected is the stable sentinel wrapped by typed
// write-rejection errors.
var ErrClaudeOrchestratorSCMWriteRejected = errors.New("Claude orchestrator SCM write rejected")

// Unwrap supports errors.Is checks without discarding the typed rejection.
func (e *ClaudeOrchestratorSCMWriteRejectedError) Unwrap() error {
	return ErrClaudeOrchestratorSCMWriteRejected
}
