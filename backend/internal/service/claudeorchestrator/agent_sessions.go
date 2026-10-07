package claudeorchestrator

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AgentSessionAPI is the subset of the AO session service that the agent
// dispatcher uses. The daemon passes its real session service.
type AgentSessionAPI interface {
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error)
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
}

// AgentReportReader lists the `ao report` records of a project.
type AgentReportReader interface {
	ListProject(ctx context.Context, projectID domain.ProjectID) ([]domain.ReportRecord, error)
}

// AgentProjectResolver returns the AO project registered for the orchestrator's
// repository. Agent sessions can only be spawned inside a registered project.
type AgentProjectResolver func(ctx context.Context) (domain.ProjectID, error)

// AgentSessionsConfig configures how subtasks are dispatched to AO agents.
type AgentSessionsConfig struct {
	// Harnesses maps the provider a plan names for a subtask to the AO agent
	// harness that implements it, for example claude → claude-code and
	// deepseek → deepseek-harness.
	Harnesses map[ports.ModelProvider]domain.AgentHarness
	// DefaultProvider is used when a subtask names no provider or one with no
	// configured harness.
	DefaultProvider ports.ModelProvider
	// PollInterval is how often session state is read. Defaults to 2s.
	PollInterval time.Duration
	// SettlePolls is how many consecutive polls an agent must sit at its
	// prompt, after working, before its turn counts as finished. Defaults to 2.
	SettlePolls int
	// CommitAuthorName and CommitAuthorEmail identify integration commits.
	CommitAuthorName  string
	CommitAuthorEmail string
	Now               func() time.Time
}

const (
	defaultAgentPollInterval = 2 * time.Second
	defaultAgentSettlePolls  = 2
	maxAgentBindings         = 256
	maxAgentNoteRunes        = 600
)

var unsafeBranchChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

type agentBackend struct {
	sessions AgentSessionAPI
	reports  AgentReportReader
	project  AgentProjectResolver
}

type agentBinding struct {
	sessionID domain.SessionID
	projectID domain.ProjectID
	branch    string
}

// AgentSessions implements ports.AgentImplementer with real AO agent
// sessions. Each subtask gets its own worker session on a branch cut from the
// run worktree's HEAD. When the agent finishes, its work is committed on that
// branch and fast-forwarded into the run worktree, where the orchestrator's
// fixed commands and validator then run. A retry messages the same agent with
// the failure reason. It never pushes, opens pull requests, or merges into any
// branch other than the run's own branch.
type AgentSessions struct {
	git          WorktreeCommandRunner
	backend      atomic.Pointer[agentBackend]
	harnesses    map[ports.ModelProvider]domain.AgentHarness
	fallback     ports.ModelProvider
	pollInterval time.Duration
	settlePolls  int
	authorName   string
	authorEmail  string
	now          func() time.Time

	mu       sync.Mutex
	bindings map[string]agentBinding
	order    []string
}

var _ ports.AgentImplementer = (*AgentSessions)(nil)

// NewAgentSessions validates configuration. The session backend is bound
// later with Bind, because the daemon builds its session service after the
// orchestrator.
func NewAgentSessions(git WorktreeCommandRunner, config AgentSessionsConfig) (*AgentSessions, error) {
	if git == nil {
		return nil, errors.New("agent sessions require a git command runner")
	}
	harnesses := make(map[ports.ModelProvider]domain.AgentHarness, len(config.Harnesses))
	for provider, harness := range config.Harnesses {
		harness = domain.AgentHarness(strings.TrimSpace(string(harness)))
		if harness == "" {
			continue
		}
		if !harness.IsKnown() {
			return nil, fmt.Errorf("agent harness %q for provider %q is not a known AO harness", harness, provider)
		}
		harnesses[provider] = harness
	}
	if len(harnesses) == 0 {
		return nil, errors.New("agent sessions require at least one provider harness")
	}
	fallback := config.DefaultProvider
	if _, ok := harnesses[fallback]; !ok {
		return nil, fmt.Errorf("default provider %q has no agent harness", fallback)
	}
	sessions := &AgentSessions{
		git:          git,
		harnesses:    harnesses,
		fallback:     fallback,
		pollInterval: config.PollInterval,
		settlePolls:  config.SettlePolls,
		authorName:   strings.TrimSpace(config.CommitAuthorName),
		authorEmail:  strings.TrimSpace(config.CommitAuthorEmail),
		now:          config.Now,
		bindings:     make(map[string]agentBinding),
	}
	if sessions.pollInterval <= 0 {
		sessions.pollInterval = defaultAgentPollInterval
	}
	if sessions.settlePolls <= 0 {
		sessions.settlePolls = defaultAgentSettlePolls
	}
	if sessions.authorName == "" {
		sessions.authorName = defaultCommitAuthorName
	}
	if sessions.authorEmail == "" {
		sessions.authorEmail = defaultCommitAuthorEmail
	}
	if strings.ContainsAny(sessions.authorName+sessions.authorEmail, "\x00\r\n") {
		return nil, errors.New("commit author must be a single line")
	}
	if sessions.now == nil {
		sessions.now = time.Now
	}
	return sessions, nil
}

// Bind connects the AO session, report, and project services.
func (a *AgentSessions) Bind(sessions AgentSessionAPI, reports AgentReportReader, project AgentProjectResolver) error {
	if a == nil {
		return errors.New("agent sessions are not configured")
	}
	if sessions == nil || reports == nil || project == nil {
		return errors.New("agent sessions require session, report, and project services")
	}
	a.backend.Store(&agentBackend{sessions: sessions, reports: reports, project: project})
	return nil
}

// HarnessFor returns the AO harness that implements work for provider.
func (a *AgentSessions) HarnessFor(provider ports.ModelProvider) domain.AgentHarness {
	if harness, ok := a.harnesses[provider]; ok {
		return harness
	}
	return a.harnesses[a.fallback]
}

func agentAttemptFailure(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ports.ErrAgentAttemptFailed, fmt.Sprintf(format, args...))
}

// Implement dispatches one attempt and integrates the agent's work.
func (a *AgentSessions) Implement(ctx context.Context, request ports.AgentImplementRequest) (string, error) {
	if a == nil {
		return "", errors.New("agent sessions are not configured")
	}
	backend := a.backend.Load()
	if backend == nil {
		return "", errors.New("agent sessions are not connected to the AO session service yet")
	}
	if request.WorktreePath == "" || strings.TrimSpace(request.Task.ID) == "" {
		return "", errors.New("agent request needs a run worktree and a subtask id")
	}
	runHead, err := a.gitOutput(ctx, request.WorktreePath, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	key := request.WorktreePath + "\x00" + request.Task.ID
	harness := a.HarnessFor(request.Task.Provider)
	since := a.now()

	binding, reuse := a.lookup(key)
	if reuse && request.Attempt > 1 {
		if err := a.sendRetry(ctx, backend, binding, request); err != nil {
			return "", err
		}
	} else {
		binding, err = a.spawn(ctx, backend, request, harness, runHead)
		if err != nil {
			return "", err
		}
		a.store(key, binding)
	}

	note, err := a.await(ctx, backend, binding, since)
	if err != nil {
		if ctx.Err() != nil {
			a.killQuietly(backend, binding.sessionID)
		}
		return "", err
	}
	session, err := backend.sessions.Get(ctx, binding.sessionID)
	if err != nil {
		return "", fmt.Errorf("read agent session: %w", err)
	}
	sessionPath, err := a.verifySessionWorkspace(ctx, request.WorktreePath, session, binding.branch)
	if err != nil {
		return "", err
	}
	if err := a.commitAgentWork(ctx, sessionPath, request.Task); err != nil {
		return "", err
	}
	if err := a.integrate(ctx, request.WorktreePath, runHead, binding.branch); err != nil {
		return "", err
	}
	summary := fmt.Sprintf("%s agent session %s finished subtask %q", harness, binding.sessionID, request.Task.ID)
	if note != "" {
		summary += ": " + note
	}
	return boundedRedacted(summary), nil
}

func (a *AgentSessions) spawn(ctx context.Context, backend *agentBackend, request ports.AgentImplementRequest, harness domain.AgentHarness, runHead string) (agentBinding, error) {
	projectID, err := backend.project(ctx)
	if err != nil {
		return agentBinding{}, fmt.Errorf("resolve the AO project for agent sessions: %w", err)
	}
	runBranch, err := a.gitOutput(ctx, request.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return agentBinding{}, err
	}
	if runBranch == "" || runBranch == "HEAD" {
		return agentBinding{}, errors.New("the run worktree is not on a branch")
	}
	branch, err := a.createAgentBranch(ctx, request.WorktreePath, runBranch, request.Task.ID, runHead)
	if err != nil {
		return agentBinding{}, err
	}
	session, _, _, err := backend.sessions.Spawn(ctx, ports.SpawnConfig{
		ProjectID:   projectID,
		Kind:        domain.KindWorker,
		Harness:     harness,
		Branch:      branch,
		Prompt:      agentTaskPrompt(request),
		DisplayName: agentDisplayName(request.Task),
	})
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = a.gitOutput(cleanupCtx, request.WorktreePath, "branch", "-D", branch)
		return agentBinding{}, fmt.Errorf("spawn %s agent session: %s", harness, boundedRedacted(err.Error()))
	}
	return agentBinding{sessionID: session.ID, projectID: projectID, branch: branch}, nil
}

// createAgentBranch cuts a fresh branch for the agent from the run's HEAD.
// It is a sibling of the run branch, not a child, because Git cannot hold
// both refs/heads/a and refs/heads/a/b.
func (a *AgentSessions) createAgentBranch(ctx context.Context, worktree, runBranch, subtaskID, head string) (string, error) {
	slug := strings.Trim(unsafeBranchChars.ReplaceAllString(subtaskID, "-"), "-.")
	if runes := []rune(slug); len(runes) > 40 {
		slug = string(runes[:40])
	}
	if slug == "" {
		slug = "task"
	}
	base := runBranch + "--" + slug
	for suffix := 1; suffix <= 20; suffix++ {
		branch := base
		if suffix > 1 {
			branch = fmt.Sprintf("%s-%d", base, suffix)
		}
		if _, err := a.gitOutput(ctx, worktree, "check-ref-format", "--branch", branch); err != nil {
			return "", fmt.Errorf("agent branch name is invalid: %w", err)
		}
		if _, err := a.gitOutput(ctx, worktree, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			continue
		}
		if _, err := a.gitOutput(ctx, worktree, "branch", branch, head); err != nil {
			return "", err
		}
		return branch, nil
	}
	return "", fmt.Errorf("no unused agent branch name for %q", base)
}

func (a *AgentSessions) sendRetry(ctx context.Context, backend *agentBackend, binding agentBinding, request ports.AgentImplementRequest) error {
	session, err := backend.sessions.Get(ctx, binding.sessionID)
	if err != nil {
		return fmt.Errorf("read agent session: %w", err)
	}
	if session.IsTerminated || session.Activity.State == domain.ActivityExited {
		return agentAttemptFailure("agent session %s has ended and cannot retry", binding.sessionID)
	}
	if session.Activity.State == domain.ActivityBlocked {
		// Never type into a session that is waiting on an approval dialog.
		return agentAttemptFailure("agent session %s is waiting for an approval in AO", binding.sessionID)
	}
	if err := backend.sessions.Send(ctx, binding.sessionID, agentRetryPrompt(request), nil); err != nil {
		return fmt.Errorf("send the retry to agent session %s: %s", binding.sessionID, boundedRedacted(err.Error()))
	}
	return nil
}

// await waits until the agent reports done, asks for help, ends, or sits at
// its prompt after working. An agent blocked on an approval keeps waiting for
// a person; the attempt timeout bounds the wait.
func (a *AgentSessions) await(ctx context.Context, backend *agentBackend, binding agentBinding, since time.Time) (string, error) {
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()
	seenWorking := false
	settled := 0
	for {
		session, err := backend.sessions.Get(ctx, binding.sessionID)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("read agent session: %w", err)
		}
		report, found, err := a.latestReport(ctx, backend, binding, since)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", err
		}
		if found {
			note := agentNote(report.Note)
			switch report.State {
			case domain.ReportDone:
				return note, nil
			case domain.ReportNeedsInput, domain.ReportStuck:
				return "", agentAttemptFailure("the agent reported %s: %s", report.State, note)
			}
		}
		if session.IsTerminated || session.Activity.State == domain.ActivityExited {
			return "", agentAttemptFailure("agent session %s ended before reporting done", binding.sessionID)
		}
		switch session.Activity.State {
		case domain.ActivityActive:
			seenWorking = true
			settled = 0
		case domain.ActivityWaitingInput, domain.ActivityIdle:
			if seenWorking || session.Activity.LastActivityAt.After(since) {
				settled++
				if settled >= a.settlePolls {
					return "", nil
				}
			}
		default:
			settled = 0
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *AgentSessions) latestReport(ctx context.Context, backend *agentBackend, binding agentBinding, since time.Time) (domain.ReportRecord, bool, error) {
	reports, err := backend.reports.ListProject(ctx, binding.projectID)
	if err != nil {
		return domain.ReportRecord{}, false, fmt.Errorf("read agent reports: %w", err)
	}
	var latest domain.ReportRecord
	found := false
	for _, report := range reports {
		if report.SessionID != binding.sessionID || report.CreatedAt.Before(since) {
			continue
		}
		if report.State != domain.ReportDone && report.State != domain.ReportNeedsInput && report.State != domain.ReportStuck {
			continue
		}
		latest, found = report, true
	}
	return latest, found, nil
}

// verifySessionWorkspace confirms the session's worktree is a checkout of the
// agent branch in the same repository as the run worktree.
func (a *AgentSessions) verifySessionWorkspace(ctx context.Context, runWorktree string, session domain.Session, branch string) (string, error) {
	path := strings.TrimSpace(session.Metadata.WorkspacePath)
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("agent session %s has no workspace", session.ID)
	}
	if session.Metadata.Branch != "" && session.Metadata.Branch != branch {
		return "", fmt.Errorf("agent session %s is on branch %q, expected %q", session.ID, session.Metadata.Branch, branch)
	}
	current, err := a.gitOutput(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if current != branch {
		return "", agentAttemptFailure("the agent switched its worktree to branch %q; it must stay on %q", current, branch)
	}
	sessionCommon, err := a.gitOutput(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	runCommon, err := a.gitOutput(ctx, runWorktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !samePath(sessionCommon, runCommon) {
		return "", fmt.Errorf("agent session %s works in a different repository", session.ID)
	}
	return path, nil
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if resolved, err := filepath.EvalSymlinks(left); err == nil {
		left = resolved
	}
	if resolved, err := filepath.EvalSymlinks(right); err == nil {
		right = resolved
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func (a *AgentSessions) commitAgentWork(ctx context.Context, sessionPath string, task ports.PlannedSubtask) error {
	status, err := a.gitOutput(ctx, sessionPath, "status", "--porcelain")
	if err != nil {
		return err
	}
	if status == "" {
		return nil
	}
	if _, err := a.gitOutput(ctx, sessionPath, "add", "--all"); err != nil {
		return err
	}
	subject := strings.Join(strings.Fields(task.Title), " ")
	if runes := []rune(subject); len(runes) > 72 {
		subject = strings.TrimSpace(string(runes[:72]))
	}
	if subject == "" {
		subject = "Agent work for subtask " + task.ID
	}
	_, err = a.gitOutput(ctx, sessionPath,
		"-c", "user.name="+a.authorName, "-c", "user.email="+a.authorEmail,
		"commit", "--no-verify", "--quiet", "-m", RedactSecrets(subject))
	return err
}

// integrate fast-forwards the run worktree to the agent branch after checking
// that the agent built on the run's HEAD and touched no protected paths.
func (a *AgentSessions) integrate(ctx context.Context, worktree, runHead, branch string) error {
	tip, err := a.gitOutput(ctx, worktree, "rev-parse", "refs/heads/"+branch)
	if err != nil {
		return err
	}
	if tip == runHead {
		return agentAttemptFailure("the agent made no changes")
	}
	if _, err := a.gitOutput(ctx, worktree, "merge-base", "--is-ancestor", runHead, tip); err != nil {
		return agentAttemptFailure("the agent rewrote history; its branch no longer builds on the run")
	}
	changed, err := a.gitOutput(ctx, worktree, "diff", "--name-only", "-z", runHead, tip)
	if err != nil {
		return err
	}
	for _, name := range strings.Split(changed, "\x00") {
		if name == "" {
			continue
		}
		clean, err := cleanRepoPath(name)
		if err != nil || protectedRepoPath(clean) {
			return agentAttemptFailure("the agent changed a protected path %q", boundedRedacted(name))
		}
	}
	if _, err := a.gitOutput(ctx, worktree, "merge", "--ff-only", "--quiet", tip); err != nil {
		return fmt.Errorf("fast-forward the run worktree: %w", err)
	}
	return nil
}

func (a *AgentSessions) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	output, err := a.git.RunInDirectory(ctx, dir, append([]string{"git"}, args...))
	if err != nil {
		return "", fmt.Errorf("git %s: %s", args[0], boundedRedacted(err.Error()))
	}
	if output.ExitCode != 0 {
		detail := strings.TrimSpace(output.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(output.Stdout)
		}
		return "", fmt.Errorf("git %s exited with code %d: %s", args[0], output.ExitCode, boundedRedacted(detail))
	}
	return strings.TrimSpace(output.Stdout), nil
}

func (a *AgentSessions) killQuietly(backend *agentBackend, id domain.SessionID) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = backend.sessions.Kill(ctx, id)
}

func (a *AgentSessions) lookup(key string) (agentBinding, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	binding, ok := a.bindings[key]
	return binding, ok
}

func (a *AgentSessions) store(key string, binding agentBinding) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, exists := a.bindings[key]; !exists {
		a.order = append(a.order, key)
	}
	a.bindings[key] = binding
	for len(a.order) > maxAgentBindings {
		delete(a.bindings, a.order[0])
		a.order = a.order[1:]
	}
}

func agentNote(note string) string {
	note = strings.Join(strings.Fields(note), " ")
	if runes := []rune(note); len(runes) > maxAgentNoteRunes {
		note = string(runes[:maxAgentNoteRunes]) + "…"
	}
	return boundedRedacted(note)
}

func agentDisplayName(task ports.PlannedSubtask) string {
	title := strings.Join(strings.Fields(task.Title), " ")
	if title == "" {
		title = task.ID
	}
	if runes := []rune(title); len(runes) > 60 {
		title = string(runes[:60]) + "…"
	}
	return "Orchestrator: " + title
}

func agentTaskPrompt(request ports.AgentImplementRequest) string {
	var prompt strings.Builder
	prompt.WriteString("You are one worker in a Claude-led orchestrator run. Claude planned the run and will review your work.\n\n")
	fmt.Fprintf(&prompt, "Subtask: %s\n\n%s\n\n", strings.TrimSpace(request.Task.Title), strings.TrimSpace(request.Task.Instructions))
	prompt.WriteString("Rules:\n")
	prompt.WriteString("- Work only in this worktree and stay on its current branch. Do not rewrite history.\n")
	prompt.WriteString("- Do not push, open pull requests, or merge. The orchestrator integrates your work.\n")
	prompt.WriteString("- Do not edit .git, .env files, keys, or other secrets.\n")
	prompt.WriteString("- Run the project's tests that cover your change before you finish.\n")
	prompt.WriteString("- When you are done, run `ao report --done --note \"<one-line summary>\"`. If you cannot finish, run `ao report --stuck --note \"<why>\"`.\n")
	if failure := strings.TrimSpace(request.PreviousFailure); failure != "" {
		prompt.WriteString("\nA previous attempt failed:\n")
		prompt.WriteString(failure)
		prompt.WriteString("\n")
	}
	return RedactSecrets(prompt.String())
}

func agentRetryPrompt(request ports.AgentImplementRequest) string {
	failure := strings.TrimSpace(request.PreviousFailure)
	if failure == "" {
		failure = "the orchestrator's checks did not pass"
	}
	return RedactSecrets(fmt.Sprintf(
		"Your previous attempt at %q did not pass the orchestrator's checks (attempt %d):\n%s\n\nFix the problem in this worktree, run the tests, then run `ao report --done --note \"<one-line summary>\"`.",
		strings.TrimSpace(request.Task.Title), request.Attempt-1, failure))
}
