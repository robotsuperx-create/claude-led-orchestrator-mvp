package controllers

import "github.com/aoagents/agent-orchestrator/backend/internal/ports"

// ClaudeOrchestratorStartRequest is the proposed public JSON contract for one
// synchronous, explicitly opted-in orchestration run. It is not the schema of
// the internal async control routes in httpd/claude_orchestrator_api.go; see
// docs/claude-orchestrator-http-contract.md for the route and contract boundary.
type ClaudeOrchestratorStartRequest struct {
	RunID         string `json:"runId"`
	Task          string `json:"task"`
	MaxRetries    int    `json:"maxRetries,omitempty"`
	ExplicitOptIn bool   `json:"explicitOptIn"`
}

// ClaudeOrchestratorStartResponse deliberately exposes only the run identity,
// terminal state, and advisory merge decision. It does not serialize task text,
// worker output, provider details, or error internals.
type ClaudeOrchestratorStartResponse struct {
	RunID    string             `json:"runId"`
	State    ports.RunState     `json:"state"`
	Decision ports.MergeOutcome `json:"decision"`
}
