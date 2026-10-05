package ports

// ClaudeOrchestratorFeatureFlag reports whether the orchestrator feature is
// enabled. Implementations should keep their zero/default configuration disabled.
type ClaudeOrchestratorFeatureFlag interface {
	Enabled() bool
}

// ClaudeOrchestratorRunRequest carries the per-run authorization supplied by
// the caller. A run requires explicit opt-in even when the feature is enabled.
type ClaudeOrchestratorRunRequest struct {
	ExplicitOptIn bool
}

// ClaudeOrchestratorRunGate rejects runs that lack feature enablement or
// per-run explicit opt-in.
type ClaudeOrchestratorRunGate interface {
	CheckRun(ClaudeOrchestratorRunRequest) error
}

// ClaudeOrchestratorRunRejectionReason is a stable, typed explanation for a
// gate rejection.
type ClaudeOrchestratorRunRejectionReason string

const (
	ClaudeOrchestratorRunFeatureDisabled ClaudeOrchestratorRunRejectionReason = "feature_disabled"
	ClaudeOrchestratorRunOptInRequired   ClaudeOrchestratorRunRejectionReason = "explicit_opt_in_required"
)

// ClaudeOrchestratorRunRejectedError reports why a run was denied.
type ClaudeOrchestratorRunRejectedError struct {
	Reason ClaudeOrchestratorRunRejectionReason
}

func (e *ClaudeOrchestratorRunRejectedError) Error() string {
	return "Claude orchestrator run rejected: " + string(e.Reason)
}

// ClaudeOrchestratorRunPolicy is a small default-deny implementation of the
// feature flag and run gate. Its zero value is disabled; an enabled feature
// still requires explicit opt-in on every run.
type ClaudeOrchestratorRunPolicy struct {
	FeatureEnabled bool
}

// Enabled reports the feature setting. The zero value is inactive.
func (p ClaudeOrchestratorRunPolicy) Enabled() bool {
	return p.FeatureEnabled
}

// CheckRun applies the feature flag before the caller's explicit opt-in.
func (p ClaudeOrchestratorRunPolicy) CheckRun(request ClaudeOrchestratorRunRequest) error {
	if !p.Enabled() {
		return &ClaudeOrchestratorRunRejectedError{Reason: ClaudeOrchestratorRunFeatureDisabled}
	}
	if !request.ExplicitOptIn {
		return &ClaudeOrchestratorRunRejectedError{Reason: ClaudeOrchestratorRunOptInRequired}
	}
	return nil
}
