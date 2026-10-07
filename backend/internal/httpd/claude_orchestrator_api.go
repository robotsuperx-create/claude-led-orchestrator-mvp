package httpd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
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

	mu      sync.RWMutex
	runs    map[string]ports.RunState
	details map[string]claudeOrchestratorRunDetails
	order   []string
	cancel  map[string]context.CancelFunc
}

// ClaudeOrchestratorInfo is the display-safe configuration summary.
type ClaudeOrchestratorInfo struct {
	Enabled        bool                `json:"enabled"`
	Repository     string              `json:"repository"`
	PlannerModel   string              `json:"plannerModel"`
	WorkerProvider ports.ModelProvider `json:"workerProvider"`
	WorkerModel    string              `json:"workerModel"`
	Sandboxed      bool                `json:"sandboxed"`
	MaxActiveRuns  int                 `json:"maxActiveRuns"`
}

// claudeOrchestratorRunDetails holds the non-sensitive record of a run.
type claudeOrchestratorRunDetails struct {
	Title          string
	Branch         string
	Commit         string
	Recommendation ports.MergeOutcome
	CreatedAt      time.Time
	UpdatedAt      time.Time
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
}

type claudeOrchestratorRunRequest struct {
	Task          string `json:"task"`
	MaxRetries    int    `json:"maxRetries,omitempty"`
	ExplicitOptIn bool   `json:"explicitOptIn"`
	// WorktreePath optionally selects an existing worktree of the configured
	// project. The worker and validator verify it before running anything.
	WorktreePath string `json:"worktreePath,omitempty"`
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
	service := api.Service
	request := ports.OrchestrationRequest{RunID: runID, Task: body.Task, MaxRetries: body.MaxRetries, WorktreePath: body.WorktreePath}
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
	if api.Service != nil {
		if current, found := api.Service.RunState(runID); found && validClaudeOrchestratorRunState(current) {
			api.setRunState(runID, current)
			state, _ = api.getRunState(runID)
		}
	}
	envelope.WriteJSON(w, http.StatusOK, api.response(runID, state))
}

func (api *ClaudeOrchestratorAPI) response(runID string, state ports.RunState) ClaudeOrchestratorRunResponse {
	api.mu.RLock()
	details := api.details[runID]
	api.mu.RUnlock()
	return ClaudeOrchestratorRunResponse{
		RunID: runID, State: state,
		Branch: details.Branch, Commit: details.Commit, Recommendation: details.Recommendation,
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
			ClaudeOrchestratorRunResponse: ClaudeOrchestratorRunResponse{
				RunID: runID, State: state,
				Branch: details.Branch, Commit: details.Commit, Recommendation: details.Recommendation,
			},
			Title: details.Title, CreatedAt: details.CreatedAt, UpdatedAt: details.UpdatedAt,
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
		if current, found := api.Service.RunState(runID); found && validClaudeOrchestratorRunState(current) {
			api.setRunState(runID, current)
		}
	}
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
	if err == nil && validClaudeOrchestratorRunState(result.State) {
		state = result.State
		return
	}
	if current, ok := service.RunState(request.RunID); ok && validClaudeOrchestratorRunState(current) {
		state = current
	}
}

func (api *ClaudeOrchestratorAPI) setRunState(runID string, state ports.RunState) {
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
		return
	}
	api.runs[runID] = state
	if details, known := api.details[runID]; known && current != state {
		details.UpdatedAt = time.Now().UTC()
		api.details[runID] = details
	}
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
		ports.RunStateValidating, ports.RunStateReviewing, ports.RunStateCompleted,
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
