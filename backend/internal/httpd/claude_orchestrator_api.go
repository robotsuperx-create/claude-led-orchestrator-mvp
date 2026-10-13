package httpd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/claudeorchestrator"
)

const (
	claudeOrchestratorMaxBodyBytes = 16 * 1024
	claudeOrchestratorMaxTaskBytes = 8 * 1024
	claudeOrchestratorMaxRetries   = 3
	claudeOrchestratorMaxPathBytes = 4 * 1024
	// DefaultClaudeOrchestratorMaxActiveRuns bounds concurrent runs, each of
	// which spends provider credit and runs commands.
	DefaultClaudeOrchestratorMaxActiveRuns = 2
	// claudeOrchestratorMaxRetainedRuns bounds remembered run records; the
	// oldest terminal runs are forgotten first.
	claudeOrchestratorMaxRetainedRuns = 256
)

// ClaudeOrchestratorRunService is the narrow injectable service contract used by
// the local control API. It deliberately exposes no provider configuration or
// credentials.
type ClaudeOrchestratorRunService interface {
	Run(context.Context, ports.OrchestrationRequest) (ports.OrchestrationResult, error)
	RunState(runID string) (ports.RunState, bool)
}

// ClaudeOrchestratorAPI registers local-only endpoints for starting an
// explicitly authorized orchestration run and polling its state. Its zero value
// is default-deny because a run gate is required.
type ClaudeOrchestratorAPI struct {
	Service ClaudeOrchestratorRunService
	Gate    ports.ClaudeOrchestratorRunGate
	// MaxActiveRuns limits non-terminal runs; zero selects
	// DefaultClaudeOrchestratorMaxActiveRuns.
	MaxActiveRuns int
	// Info describes the configured orchestrator for the desktop UI. It holds
	// display names only: no paths beyond the repository's base name, no URLs,
	// and no credentials.
	Info ClaudeOrchestratorInfo
	// History, when set, keeps run records across daemon restarts. Call
	// Restore once before serving.
	History ports.ClaudeOrchestratorRunHistory
	// Publisher, when set, lets a person push a finished run's branch and
	// open a pull request for it. Nothing is ever pushed without that click.
	Publisher ClaudeOrchestratorRunPublisher

	persistMu sync.Mutex
	// persistFailures counts failed history writes; the in-memory record
	// stays authoritative, and the next write retries the whole history.
	persistFailures atomic.Int64
	mu              sync.RWMutex
	runs            map[string]ports.RunState
	details         map[string]claudeOrchestratorRunDetails
	order           []string
	cancel          map[string]context.CancelFunc
}

// ClaudeOrchestratorInfo is the display-safe configuration summary.
type ClaudeOrchestratorInfo struct {
	Enabled        bool                `json:"enabled"`
	Repository     string              `json:"repository"`
	PlannerModel   string              `json:"plannerModel"`
	WorkerProvider ports.ModelProvider `json:"workerProvider"`
	WorkerModel    string              `json:"workerModel"`
	// WorkerMode is "model" when an API model writes the code and "agents"
	// when AO agent sessions do; WorkerModel then names the agent harness.
	WorkerMode    string `json:"workerMode"`
	Sandboxed     bool   `json:"sandboxed"`
	MaxActiveRuns int    `json:"maxActiveRuns"`
	// PlannerProvider is "claude" or "local".
	PlannerProvider ports.ModelProvider `json:"plannerProvider,omitempty"`
	// BudgetTokens and BudgetUSD are each run's limits; zero means none.
	BudgetTokens int64   `json:"budgetTokens"`
	BudgetUSD    float64 `json:"budgetUsd,omitempty"`
	MaxReplans   int     `json:"maxReplans"`
	// CanPublish is true when runs can be pushed for review.
	CanPublish bool `json:"canPublish"`
}

// ClaudeOrchestratorRunPublisher pushes one run branch for review.
type ClaudeOrchestratorRunPublisher interface {
	Publish(context.Context, claudeorchestrator.PublishRequest) (claudeorchestrator.PublishResult, error)
}

// ClaudeOrchestratorUsage is a run's model usage as shown to a person.
type ClaudeOrchestratorUsage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	Calls        int64 `json:"calls"`
	// CostUSD is an estimate, present only when every call had a price.
	CostUSD      *float64 `json:"costUsd,omitempty"`
	BudgetTokens int64    `json:"budgetTokens,omitempty"`
	BudgetUSD    float64  `json:"budgetUsd,omitempty"`
	Exhausted    bool     `json:"exhausted,omitempty"`
}

func displayUsage(usage *ports.RunUsage) *ClaudeOrchestratorUsage {
	// A run that made no metered call has nothing to show.
	if usage == nil || (usage.Calls == 0 && !usage.Exhausted) {
		return nil
	}
	shown := &ClaudeOrchestratorUsage{
		InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, Calls: usage.Calls,
		BudgetTokens: usage.BudgetTokens, BudgetUSD: float64(usage.BudgetMicroUSD) / 1e6, Exhausted: usage.Exhausted,
	}
	if usage.Priced {
		cost := float64(usage.CostMicroUSD) / 1e6
		shown.CostUSD = &cost
	}
	return shown
}

// claudeOrchestratorRunDetails holds the non-sensitive record of a run.
type claudeOrchestratorRunDetails struct {
	Title          string
	Branch         string
	Commit         string
	Recommendation ports.MergeOutcome
	Interrupted    bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Usage          *ports.RunUsage
	Replans        int
	PullRequestURL string
	Publishing     bool
}

// claudeOrchestratorMaxTitleRunes bounds the task excerpt kept for history.
const claudeOrchestratorMaxTitleRunes = 160

// ClaudeOrchestratorRunSummary is one row of run history.
type ClaudeOrchestratorRunSummary struct {
	ClaudeOrchestratorRunResponse
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ClaudeOrchestratorRunList is the run history, newest first.
type ClaudeOrchestratorRunList struct {
	Runs []ClaudeOrchestratorRunSummary `json:"runs"`
}

// Register mounts the isolated local control routes. Callers opt into daemon
// wiring by registering this API; it is intentionally not mounted by default.
func (api *ClaudeOrchestratorAPI) Register(r chi.Router) {
	r.Get("/internal/claude-orchestrator", api.info)
	r.Get("/internal/claude-orchestrator/runs", api.list)
	r.Post("/internal/claude-orchestrator/runs", api.start)
	r.Get("/internal/claude-orchestrator/runs/{runId}", api.status)
	r.Post("/internal/claude-orchestrator/runs/{runId}/cancel", api.cancelRun)
	r.Get("/internal/claude-orchestrator/runs/{runId}/plan", api.plan)
	r.Post("/internal/claude-orchestrator/runs/{runId}/approve", api.decidePlan(true))
	r.Post("/internal/claude-orchestrator/runs/{runId}/reject", api.decidePlan(false))
	r.Post("/internal/claude-orchestrator/runs/{runId}/publish", api.publish)
}

type claudeOrchestratorRunRequest struct {
	Task          string `json:"task"`
	MaxRetries    int    `json:"maxRetries,omitempty"`
	ExplicitOptIn bool   `json:"explicitOptIn"`
	// WorktreePath optionally selects an existing worktree of the configured
	// project. The worker and validator verify it before running anything.
	WorktreePath string `json:"worktreePath,omitempty"`
	// ApprovePlan pauses the run after planning until it is approved.
	ApprovePlan bool `json:"approvePlan,omitempty"`
}

// ClaudeOrchestratorRunResponse is intentionally limited to opaque run
// identity, lifecycle state, and where the result lives: the run branch, its
// commit, and the merge recommendation. It does not serialize prompts, model
// output, worker output, errors, credentials, or provider configuration.
type ClaudeOrchestratorRunResponse struct {
	RunID          string             `json:"runId"`
	State          ports.RunState     `json:"state"`
	Branch         string             `json:"branch,omitempty"`
	Commit         string             `json:"commit,omitempty"`
	Recommendation ports.MergeOutcome `json:"recommendation,omitempty"`
	// Interrupted means the run was still active when the daemon stopped.
	Interrupted bool `json:"interrupted,omitempty"`
	// Usage is the run's model usage and budget.
	Usage *ClaudeOrchestratorUsage `json:"usage,omitempty"`
	// Replans counts how many times Claude revised the plan.
	Replans int `json:"replans,omitempty"`
	// PullRequestURL is set once the run was published for review;
	// CompareURL when a pull request has to be opened by hand.
	PullRequestURL string `json:"pullRequestUrl,omitempty"`
	CompareURL     string `json:"compareUrl,omitempty"`
}

func (api *ClaudeOrchestratorAPI) start(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}

	var body claudeOrchestratorRunRequest
	if err := decodeClaudeOrchestratorBody(w, r, &body); err != nil {
		status, code, message := http.StatusBadRequest, "INVALID_REQUEST", "Request body must be valid JSON"
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			status, code, message = http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "Request body exceeds the allowed size"
		}
		writeClaudeOrchestratorAPIError(w, r, status, code, message)
		return
	}
	if !body.ExplicitOptIn {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "EXPLICIT_OPT_IN_REQUIRED", "Explicit opt-in is required for every run")
		return
	}
	if api.Gate == nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "FEATURE_DISABLED", "Claude orchestrator runs are disabled")
		return
	}
	if err := api.Gate.CheckRun(ports.ClaudeOrchestratorRunRequest{ExplicitOptIn: body.ExplicitOptIn}); err != nil {
		// Gate errors are deliberately not returned: they may contain internal
		// policy details and are not needed to make the authorization decision.
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "RUN_NOT_AUTHORIZED", "Claude orchestrator run is not authorized")
		return
	}
	if api.Service == nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusServiceUnavailable, "ORCHESTRATOR_UNAVAILABLE", "Claude orchestrator service is unavailable")
		return
	}
	if strings.TrimSpace(body.Task) == "" || len(body.Task) > claudeOrchestratorMaxTaskBytes || strings.ContainsRune(body.Task, '\x00') {
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadRequest, "INVALID_TASK", "task must be non-empty and within the allowed size")
		return
	}
	if body.MaxRetries < 0 || body.MaxRetries > claudeOrchestratorMaxRetries {
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadRequest, "INVALID_MAX_RETRIES", "maxRetries is outside the allowed range")
		return
	}
	body.WorktreePath = strings.TrimSpace(body.WorktreePath)
	if body.WorktreePath != "" && (len(body.WorktreePath) > claudeOrchestratorMaxPathBytes ||
		strings.ContainsRune(body.WorktreePath, '\x00') || !filepath.IsAbs(body.WorktreePath)) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadRequest, "INVALID_WORKTREE_PATH", "worktreePath must be an absolute path within the allowed size")
		return
	}

	runID, err := newClaudeOrchestratorRunID()
	if err != nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusInternalServerError, "RUN_START_FAILED", "Unable to start Claude orchestrator run")
		return
	}
	if !api.reserveRun(runID, runTitle(body.Task)) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusTooManyRequests, "TOO_MANY_ACTIVE_RUNS", "Too many Claude orchestrator runs are active; wait for one to finish")
		return
	}
	api.persist()
	service := api.Service
	request := ports.OrchestrationRequest{
		RunID: runID, Task: body.Task, MaxRetries: body.MaxRetries, WorktreePath: body.WorktreePath,
		RequirePlanApproval: body.ApprovePlan,
	}
	ctx, cancel := context.WithCancel(context.Background())
	api.registerCancel(runID, cancel)
	go api.execute(ctx, service, request, cancel)

	envelope.WriteJSON(w, http.StatusAccepted, ClaudeOrchestratorRunResponse{RunID: runID, State: ports.RunStatePending})
}

func (api *ClaudeOrchestratorAPI) cancelRun(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	runID := chi.URLParam(r, "runId")
	state, ok := api.getRunState(runID)
	if runID == "" || !ok {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "RUN_NOT_FOUND", "Run was not found")
		return
	}
	if state == ports.RunStateCanceled {
		envelope.WriteJSON(w, http.StatusOK, ClaudeOrchestratorRunResponse{RunID: runID, State: state})
		return
	}
	if terminalClaudeOrchestratorRunState(state) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusConflict, "RUN_ALREADY_TERMINAL", "Run is already terminal")
		return
	}
	if api.Service != nil {
		if service, supportsCancel := api.Service.(ports.OrchestrationRunCanceler); supportsCancel && !service.Cancel(runID) {
			if current, found := api.Service.RunState(runID); found && validClaudeOrchestratorRunState(current) {
				api.setRunState(runID, current)
				state = current
			}
			if state == ports.RunStateCanceled {
				envelope.WriteJSON(w, http.StatusOK, ClaudeOrchestratorRunResponse{RunID: runID, State: state})
				return
			}
			if terminalClaudeOrchestratorRunState(state) {
				writeClaudeOrchestratorAPIError(w, r, http.StatusConflict, "RUN_ALREADY_TERMINAL", "Run is already terminal")
				return
			}
		}
	}
	api.cancelLocal(runID)
	api.persist()
	envelope.WriteJSON(w, http.StatusOK, ClaudeOrchestratorRunResponse{RunID: runID, State: ports.RunStateCanceled})
}

func (api *ClaudeOrchestratorAPI) status(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	runID := chi.URLParam(r, "runId")
	if runID == "" {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "RUN_NOT_FOUND", "Run was not found")
		return
	}
	state, ok := api.getRunState(runID)
	if !ok {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "RUN_NOT_FOUND", "Run was not found")
		return
	}
	if api.Service != nil && !terminalClaudeOrchestratorRunState(state) {
		api.refreshRun(runID)
		state, _ = api.getRunState(runID)
	}
	envelope.WriteJSON(w, http.StatusOK, api.response(runID, state))
}

func (api *ClaudeOrchestratorAPI) response(runID string, state ports.RunState) ClaudeOrchestratorRunResponse {
	api.mu.RLock()
	details := api.details[runID]
	api.mu.RUnlock()
	return details.response(runID, state)
}

func (details claudeOrchestratorRunDetails) response(runID string, state ports.RunState) ClaudeOrchestratorRunResponse {
	return ClaudeOrchestratorRunResponse{
		RunID: runID, State: state,
		Branch: details.Branch, Commit: details.Commit, Recommendation: details.Recommendation,
		Interrupted: details.Interrupted, Usage: displayUsage(details.Usage), Replans: details.Replans,
		PullRequestURL: details.PullRequestURL,
	}
}

// reserveRun registers a new pending run unless the active-run limit is
// reached, then forgets the oldest terminal runs beyond the retention bound.
func (api *ClaudeOrchestratorAPI) reserveRun(runID, title string) bool {
	limit := api.MaxActiveRuns
	if limit <= 0 {
		limit = DefaultClaudeOrchestratorMaxActiveRuns
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.runs == nil {
		api.runs = make(map[string]ports.RunState)
	}
	active := 0
	for _, state := range api.runs {
		if !terminalClaudeOrchestratorRunState(state) {
			active++
		}
	}
	if active >= limit {
		return false
	}
	api.runs[runID] = ports.RunStatePending
	if api.details == nil {
		api.details = make(map[string]claudeOrchestratorRunDetails)
	}
	now := time.Now().UTC()
	api.details[runID] = claudeOrchestratorRunDetails{Title: title, CreatedAt: now, UpdatedAt: now}
	api.order = append(api.order, runID)
	if len(api.order) > claudeOrchestratorMaxRetainedRuns {
		kept := api.order[:0]
		excess := len(api.order) - claudeOrchestratorMaxRetainedRuns
		for _, id := range api.order {
			if excess > 0 && terminalClaudeOrchestratorRunState(api.runs[id]) {
				delete(api.runs, id)
				delete(api.details, id)
				excess--
				continue
			}
			kept = append(kept, id)
		}
		api.order = kept
	}
	return true
}

func (api *ClaudeOrchestratorAPI) recordDetails(runID string, result ports.OrchestrationResult) {
	api.mu.Lock()
	defer api.mu.Unlock()
	if _, known := api.runs[runID]; !known {
		return
	}
	if api.details == nil {
		api.details = make(map[string]claudeOrchestratorRunDetails)
	}
	details := api.details[runID]
	details.Branch = result.Workspace.Branch
	details.Commit = result.Workspace.Commit
	details.Recommendation = result.MergeDecision.Decision
	usage := result.Usage
	details.Usage = &usage
	details.Replans = result.Replans
	details.UpdatedAt = time.Now().UTC()
	api.details[runID] = details
}

// runTitle keeps a short, secret-redacted excerpt of the task for history.
func runTitle(task string) string {
	title := strings.Join(strings.Fields(claudeorchestrator.RedactSecrets(task)), " ")
	if runes := []rune(title); len(runes) > claudeOrchestratorMaxTitleRunes {
		title = strings.TrimSpace(string(runes[:claudeOrchestratorMaxTitleRunes])) + "…"
	}
	return title
}

func (api *ClaudeOrchestratorAPI) info(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	info := api.Info
	info.Enabled = api.Gate != nil && api.Service != nil
	info.CanPublish = api.Publisher != nil
	if info.MaxActiveRuns <= 0 {
		info.MaxActiveRuns = api.MaxActiveRuns
		if info.MaxActiveRuns <= 0 {
			info.MaxActiveRuns = DefaultClaudeOrchestratorMaxActiveRuns
		}
	}
	envelope.WriteJSON(w, http.StatusOK, info)
}

func (api *ClaudeOrchestratorAPI) list(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	api.refreshFromService()
	api.mu.RLock()
	runs := make([]ClaudeOrchestratorRunSummary, 0, len(api.order))
	for i := len(api.order) - 1; i >= 0; i-- {
		runID := api.order[i]
		state, ok := api.runs[runID]
		if !ok {
			continue
		}
		details := api.details[runID]
		runs = append(runs, ClaudeOrchestratorRunSummary{
			ClaudeOrchestratorRunResponse: details.response(runID, state),
			Title:                         details.Title, CreatedAt: details.CreatedAt, UpdatedAt: details.UpdatedAt,
		})
	}
	api.mu.RUnlock()
	envelope.WriteJSON(w, http.StatusOK, ClaudeOrchestratorRunList{Runs: runs})
}

// refreshFromService pulls the live stage of every active run so history
// shows planning/executing/... rather than the state recorded at start.
func (api *ClaudeOrchestratorAPI) refreshFromService() {
	if api.Service == nil {
		return
	}
	api.mu.RLock()
	active := make([]string, 0)
	for runID, state := range api.runs {
		if !terminalClaudeOrchestratorRunState(state) {
			active = append(active, runID)
		}
	}
	api.mu.RUnlock()
	for _, runID := range active {
		api.refreshRun(runID)
	}
}

// refreshRun pulls one active run's live stage and usage from the service.
func (api *ClaudeOrchestratorAPI) refreshRun(runID string) {
	if current, found := api.Service.RunState(runID); found && validClaudeOrchestratorRunState(current) {
		api.setRunState(runID, current)
	}
	reporter, ok := api.Service.(ports.OrchestrationUsageReporter)
	if !ok {
		return
	}
	usage, found := reporter.UsageFor(runID)
	if !found {
		return
	}
	api.mu.Lock()
	if details, known := api.details[runID]; known {
		details.Usage = &usage
		api.details[runID] = details
	}
	api.mu.Unlock()
}

func (api *ClaudeOrchestratorAPI) execute(ctx context.Context, service ClaudeOrchestratorRunService, request ports.OrchestrationRequest, cancel context.CancelFunc) {
	state := ports.RunStateFailed
	defer func() {
		defer cancel()
		defer api.removeCancel(request.RunID)
		if recover() != nil {
			api.setRunState(request.RunID, ports.RunStateFailed)
			return
		}
		api.setRunState(request.RunID, state)
	}()

	// This context is detached from the HTTP request and remains cancelable
	// through the run's local control endpoint.
	result, err := service.Run(ctx, request)
	api.recordDetails(request.RunID, result)
	api.persist()
	if err == nil && validClaudeOrchestratorRunState(result.State) {
		state = result.State
		return
	}
	if current, ok := service.RunState(request.RunID); ok && validClaudeOrchestratorRunState(current) {
		state = current
	}
}

func (api *ClaudeOrchestratorAPI) setRunState(runID string, state ports.RunState) {
	if api.updateRunState(runID, state) {
		api.persist()
	}
}

func (api *ClaudeOrchestratorAPI) updateRunState(runID string, state ports.RunState) bool {
	if !validClaudeOrchestratorRunState(state) {
		state = ports.RunStateFailed
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.runs == nil {
		api.runs = make(map[string]ports.RunState)
	}
	current, ok := api.runs[runID]
	if ok && terminalClaudeOrchestratorRunState(current) {
		return false
	}
	api.runs[runID] = state
	if details, known := api.details[runID]; known && current != state {
		details.UpdatedAt = time.Now().UTC()
		api.details[runID] = details
	}
	return current != state
}

// Restore loads persisted history. A run that was active when the daemon
// stopped can no longer progress, so it is recorded as failed and marked
// interrupted; its branch and any commits stay in Git.
func (api *ClaudeOrchestratorAPI) Restore() error {
	if api.History == nil {
		return nil
	}
	records, err := api.History.Load()
	api.mu.Lock()
	if api.runs == nil {
		api.runs = make(map[string]ports.RunState)
	}
	if api.details == nil {
		api.details = make(map[string]claudeOrchestratorRunDetails)
	}
	if len(records) > claudeOrchestratorMaxRetainedRuns {
		records = records[len(records)-claudeOrchestratorMaxRetainedRuns:]
	}
	now := time.Now().UTC()
	for _, record := range records {
		if !validClaudeOrchestratorRunID(record.RunID) || !validClaudeOrchestratorRunState(record.State) {
			continue
		}
		if _, exists := api.runs[record.RunID]; exists {
			continue
		}
		state, interrupted, updated := record.State, record.Interrupted, record.UpdatedAt
		if !terminalClaudeOrchestratorRunState(state) {
			state, interrupted, updated = ports.RunStateFailed, true, now
		}
		api.runs[record.RunID] = state
		api.details[record.RunID] = claudeOrchestratorRunDetails{
			Title: runTitle(record.Title), Branch: record.Branch, Commit: record.Commit,
			Recommendation: record.Recommendation, Interrupted: interrupted,
			CreatedAt: record.CreatedAt, UpdatedAt: updated,
			Usage: record.Usage, Replans: record.Replans, PullRequestURL: safeReviewURL(record.PullRequestURL),
		}
		api.order = append(api.order, record.RunID)
	}
	api.mu.Unlock()
	api.persist()
	return err
}

// persist writes the full bounded history. Snapshots are taken while holding
// persistMu, so concurrent writers can never store an older view last.
func (api *ClaudeOrchestratorAPI) persist() {
	if api.History == nil {
		return
	}
	api.persistMu.Lock()
	defer api.persistMu.Unlock()
	api.mu.RLock()
	records := make([]ports.ClaudeOrchestratorRunRecord, 0, len(api.order))
	for _, runID := range api.order {
		state, ok := api.runs[runID]
		if !ok {
			continue
		}
		details := api.details[runID]
		records = append(records, ports.ClaudeOrchestratorRunRecord{
			RunID: runID, State: state, Title: details.Title,
			Branch: details.Branch, Commit: details.Commit, Recommendation: details.Recommendation,
			Interrupted: details.Interrupted, CreatedAt: details.CreatedAt, UpdatedAt: details.UpdatedAt,
			Usage: details.Usage, Replans: details.Replans, PullRequestURL: details.PullRequestURL,
		})
	}
	api.mu.RUnlock()
	if err := api.History.Save(records); err != nil {
		api.persistFailures.Add(1)
	}
}

func validClaudeOrchestratorRunID(runID string) bool {
	if !strings.HasPrefix(runID, "claude-run-") || len(runID) != len("claude-run-")+32 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(runID, "claude-run-"))
	return err == nil
}

func (api *ClaudeOrchestratorAPI) registerCancel(runID string, cancel context.CancelFunc) {
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.cancel == nil {
		api.cancel = make(map[string]context.CancelFunc)
	}
	api.cancel[runID] = cancel
}

func (api *ClaudeOrchestratorAPI) removeCancel(runID string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	delete(api.cancel, runID)
}

func (api *ClaudeOrchestratorAPI) cancelLocal(runID string) {
	api.mu.Lock()
	state, exists := api.runs[runID]
	if !exists || terminalClaudeOrchestratorRunState(state) {
		api.mu.Unlock()
		return
	}
	api.runs[runID] = ports.RunStateCanceled
	cancel := api.cancel[runID]
	api.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (api *ClaudeOrchestratorAPI) getRunState(runID string) (ports.RunState, bool) {
	api.mu.RLock()
	defer api.mu.RUnlock()
	state, ok := api.runs[runID]
	return state, ok
}

func decodeClaudeOrchestratorBody(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, claudeOrchestratorMaxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body contains multiple JSON values")
		}
		return err
	}
	return nil
}

func newClaudeOrchestratorRunID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "claude-run-" + hex.EncodeToString(value[:]), nil
}

func validClaudeOrchestratorRunState(state ports.RunState) bool {
	switch state {
	case ports.RunStatePending, ports.RunStatePlanning, ports.RunStateExecuting,
		ports.RunStateValidating, ports.RunStateReviewing, ports.RunStateAwaitingApproval, ports.RunStateCompleted,
		ports.RunStateHeld, ports.RunStateFailed, ports.RunStateCanceled:
		return true
	default:
		return false
	}
}

func terminalClaudeOrchestratorRunState(state ports.RunState) bool {
	switch state {
	case ports.RunStateCompleted, ports.RunStateHeld, ports.RunStateFailed, ports.RunStateCanceled:
		return true
	default:
		return false
	}
}

func writeClaudeOrchestratorAPIError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	envelope.WriteAPIError(w, r, status, "request_error", code, message, nil)
}

// Display bounds for a plan shown to a person. The plan is Claude's own
// output; it is secret-redacted and bounded, and carries no paths or metadata.
const (
	claudeOrchestratorMaxPlanSubtasks     = 50
	claudeOrchestratorMaxPlanSummaryRunes = 1000
	claudeOrchestratorMaxPlanTitleRunes   = 200
	claudeOrchestratorMaxPlanInstrRunes   = 4000
	claudeOrchestratorMaxPlanIdentRunes   = 80
	claudeOrchestratorMaxPlanDependencies = 20
)

// ClaudeOrchestratorPlan is the plan a person reviews before approving a run.
type ClaudeOrchestratorPlan struct {
	RunID    string                          `json:"runId"`
	Summary  string                          `json:"summary"`
	Subtasks []ClaudeOrchestratorPlanSubtask `json:"subtasks"`
}

// ClaudeOrchestratorPlanSubtask is one planned step.
type ClaudeOrchestratorPlanSubtask struct {
	ID           string              `json:"id"`
	Title        string              `json:"title"`
	Instructions string              `json:"instructions"`
	Provider     ports.ModelProvider `json:"provider,omitempty"`
	DependsOn    []string            `json:"dependsOn,omitempty"`
}

func (api *ClaudeOrchestratorAPI) planApprover() (ports.OrchestrationPlanApprover, bool) {
	if api.Service == nil {
		return nil, false
	}
	approver, ok := api.Service.(ports.OrchestrationPlanApprover)
	return approver, ok
}

func (api *ClaudeOrchestratorAPI) plan(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	runID := chi.URLParam(r, "runId")
	approver, ok := api.planApprover()
	if _, known := api.getRunState(runID); !known || !ok {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "RUN_NOT_FOUND", "Run was not found")
		return
	}
	plan, found := approver.PlanFor(runID)
	if !found {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "PLAN_NOT_READY", "The run has no plan yet")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, displayPlan(runID, plan))
}

func displayPlan(runID string, plan ports.ExecutionPlan) ClaudeOrchestratorPlan {
	shown := ClaudeOrchestratorPlan{RunID: runID, Summary: boundedPlanText(plan.Summary, claudeOrchestratorMaxPlanSummaryRunes)}
	subtasks := plan.Subtasks
	if len(subtasks) > claudeOrchestratorMaxPlanSubtasks {
		subtasks = subtasks[:claudeOrchestratorMaxPlanSubtasks]
	}
	shown.Subtasks = make([]ClaudeOrchestratorPlanSubtask, 0, len(subtasks))
	for _, task := range subtasks {
		step := ClaudeOrchestratorPlanSubtask{
			ID:           boundedPlanText(task.ID, claudeOrchestratorMaxPlanIdentRunes),
			Title:        boundedPlanText(task.Title, claudeOrchestratorMaxPlanTitleRunes),
			Instructions: boundedPlanText(task.Instructions, claudeOrchestratorMaxPlanInstrRunes),
			Provider:     ports.ModelProvider(boundedPlanText(string(task.Provider), claudeOrchestratorMaxPlanIdentRunes)),
		}
		for i, dependency := range task.DependsOn {
			if i == claudeOrchestratorMaxPlanDependencies {
				break
			}
			step.DependsOn = append(step.DependsOn, boundedPlanText(dependency, claudeOrchestratorMaxPlanIdentRunes))
		}
		shown.Subtasks = append(shown.Subtasks, step)
	}
	return shown
}

func boundedPlanText(value string, maxRunes int) string {
	value = strings.TrimSpace(claudeorchestrator.RedactSecrets(strings.ToValidUTF8(value, "")))
	if runes := []rune(value); len(runes) > maxRunes {
		value = strings.TrimSpace(string(runes[:maxRunes])) + "…"
	}
	return value
}

func (api *ClaudeOrchestratorAPI) decidePlan(approved bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !localControlRequest(r) {
			writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
			return
		}
		runID := chi.URLParam(r, "runId")
		approver, ok := api.planApprover()
		if _, known := api.getRunState(runID); !known || !ok {
			writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "RUN_NOT_FOUND", "Run was not found")
			return
		}
		decide := approver.Reject
		if approved {
			decide = approver.Approve
		}
		decided := decide(runID)
		if !decided {
			writeClaudeOrchestratorAPIError(w, r, http.StatusConflict, "RUN_NOT_AWAITING_APPROVAL", "Run is not waiting for plan approval")
			return
		}
		state := ports.RunStatePlanning
		if !approved {
			state = ports.RunStateCanceled
			api.cancelLocal(runID)
		} else if current, found := api.Service.RunState(runID); found && validClaudeOrchestratorRunState(current) {
			state = current
		}
		api.setRunState(runID, state)
		api.persist()
		envelope.WriteJSON(w, http.StatusOK, api.response(runID, state))
	}
}

// claudeOrchestratorPublishTimeout bounds one push plus pull-request creation.
const claudeOrchestratorPublishTimeout = 2 * time.Minute

// publish pushes a finished run's own branch and opens a pull request for it.
// It runs only when a person asks; the orchestrator never pushes or merges on
// its own.
func (api *ClaudeOrchestratorAPI) publish(w http.ResponseWriter, r *http.Request) {
	if !localControlRequest(r) {
		writeClaudeOrchestratorAPIError(w, r, http.StatusForbidden, "LOCAL_CONTROL_REQUIRED", "This endpoint is available only to local callers")
		return
	}
	runID := chi.URLParam(r, "runId")
	if api.Publisher == nil {
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "PUBLISH_UNAVAILABLE", "Publishing runs is not available")
		return
	}
	api.mu.Lock()
	state, known := api.runs[runID]
	details := api.details[runID]
	switch {
	case !known:
		api.mu.Unlock()
		writeClaudeOrchestratorAPIError(w, r, http.StatusNotFound, "RUN_NOT_FOUND", "Run was not found")
		return
	case details.PullRequestURL != "":
		api.mu.Unlock()
		envelope.WriteJSON(w, http.StatusOK, details.response(runID, state))
		return
	case state != ports.RunStateCompleted && state != ports.RunStateHeld:
		api.mu.Unlock()
		writeClaudeOrchestratorAPIError(w, r, http.StatusConflict, "RUN_NOT_PUBLISHABLE", "Only a completed or held run can be published")
		return
	case details.Branch == "" || details.Commit == "":
		api.mu.Unlock()
		writeClaudeOrchestratorAPIError(w, r, http.StatusConflict, "RUN_HAS_NO_CHANGES", "The run has no committed changes to publish")
		return
	case details.Publishing:
		api.mu.Unlock()
		writeClaudeOrchestratorAPIError(w, r, http.StatusConflict, "PUBLISH_IN_PROGRESS", "This run is already being published")
		return
	}
	details.Publishing = true
	api.details[runID] = details
	api.mu.Unlock()
	defer func() {
		api.mu.Lock()
		if current, ok := api.details[runID]; ok {
			current.Publishing = false
			api.details[runID] = current
		}
		api.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), claudeOrchestratorPublishTimeout)
	defer cancel()
	body := "Opened from X-ROBOT-AI Mission control. Review the changes before merging; nothing was merged automatically.\n\n" +
		"Run: " + runID + "\nRecommendation: " + string(details.Recommendation) + "\nCommit: " + details.Commit + "\n"
	result, err := api.Publisher.Publish(ctx, claudeorchestrator.PublishRequest{
		RunID:  strings.TrimPrefix(details.Branch, claudeorchestrator.DefaultRunBranchPrefix),
		Branch: details.Branch,
		Title:  details.Title,
		Body:   body,
	})
	if err != nil {
		// Push errors can carry remote names or URLs; the person gets a fixed
		// message and the branch stays local.
		code, message := "PUBLISH_FAILED", "The branch could not be pushed. Check that the repository has a remote you can push to."
		if errors.Is(err, claudeorchestrator.ErrPublishNoRemote) {
			code, message = "NO_REMOTE", "The repository has no remote to publish to"
		}
		writeClaudeOrchestratorAPIError(w, r, http.StatusBadGateway, code, message)
		return
	}
	api.mu.Lock()
	current := api.details[runID]
	current.PullRequestURL = safeReviewURL(result.PullRequestURL)
	current.UpdatedAt = time.Now().UTC()
	api.details[runID] = current
	api.mu.Unlock()
	api.persist()
	response := current.response(runID, state)
	response.CompareURL = safeReviewURL(result.CompareURL)
	envelope.WriteJSON(w, http.StatusOK, response)
}

// safeReviewURL keeps only plain https links without credentials.
func safeReviewURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}
