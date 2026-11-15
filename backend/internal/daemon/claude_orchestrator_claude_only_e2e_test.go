package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	worktreesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/worktree"
)

// TestClaudeOrchestratorOneClaudeKeyRunsTheWholeCrew drives a run with only a
// Claude provider configured: Claude plans (naming other writers, which must
// be ignored), writes two independent subtasks at the same time, and reviews.
// The provider is busy once and the run carries on. Only Claude is faked,
// over real HTTP; Git, worktrees, commands and the API are real.
func TestClaudeOrchestratorOneClaudeKeyRunsTheWholeCrew(t *testing.T) {
	for _, tool := range []string{"git", "test"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, project, "init", "--quiet")
	e2eGit(t, project, "add", "--all")
	e2eGit(t, project, "-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "--quiet", "-m", "initial")
	mainHead := e2eGit(t, project, "rev-parse", "HEAD")

	// Both proposals must be in flight together: each waits for the other.
	var proposing atomic.Int32
	bothProposing := make(chan struct{})
	var bothOnce sync.Once
	var busyOnce atomic.Bool
	claude := &fakeProvider{t: t, answer: func(system, user string, _ int) string {
		switch {
		case strings.Contains(system, "execution plan"):
			return `{"summary":"Two files","subtasks":[` +
				`{"id":"alpha","title":"Write alpha.txt","instructions":"Create alpha.txt","worker_id":"w","provider":"deepseek"},` +
				`{"id":"beta","title":"Write beta.txt","instructions":"Create beta.txt","worker_id":"w","provider":"local"}]}`
		case strings.Contains(system, "choose the files"):
			return `{"paths":["README.md"]}`
		case strings.Contains(system, "Review the supplied"):
			return `{"decision":"approve","summary":"both files exist","issues":[]}`
		}
		if proposing.Add(1) == 2 {
			bothOnce.Do(func() { close(bothProposing) })
		}
		select {
		case <-bothProposing:
		case <-time.After(10 * time.Second):
			t.Error("the two independent subtasks were not written at the same time")
		}
		name := "alpha.txt"
		if strings.Contains(user, "beta.txt") {
			name = "beta.txt"
		}
		return `{"summary":"wrote ` + name + `","edits":[{"path":"` + name + `","content":"` + name + `\n"}]}`
	}}
	busyFirst := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if busyOnce.CompareAndSwap(false, true) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		claude.ServeHTTP(w, r)
	})
	claudeServer := httptest.NewServer(busyFirst)
	t.Cleanup(claudeServer.Close)

	cfg := config.Config{
		DataDir: filepath.Join(t.TempDir(), "ao"),
		ClaudeOrchestrator: config.ClaudeOrchestratorConfig{
			FeatureEnabled: true,
			ClaudeOnly:     true,
			ClaudeProvider: modelgateway.ProviderConfig{Provider: ports.ModelProviderClaude, BaseURL: claudeServer.URL, DefaultModel: "claude-test"},
			Worker: config.ClaudeOrchestratorWorkerConfig{
				ProjectRoot: project,
				// Ignored: the one Claude key writes everything.
				DefaultProvider:     ports.ModelProviderDeepSeek,
				MaxParallelSubtasks: 2,
				Commands:            []config.ClaudeOrchestratorCommandConfig{{Name: "readme", Argv: []string{"test", "-f", "README.md"}}},
				Timeout:             30 * time.Second,
			},
			Validator: config.ClaudeOrchestratorValidatorConfig{
				Commands: []config.ClaudeOrchestratorCommandConfig{{Name: "readme", Argv: []string{"test", "-f", "README.md"}}},
				Timeout:  30 * time.Second,
			},
		},
	}
	wiring, err := newClaudeOrchestratorWiring(cfg, claudeOrchestratorBuildDeps{
		Worktrees: worktreesvc.NewManagerWithManagedRoot(nil, claudeOrchestratorManagedRoot(cfg)),
	})
	if err != nil {
		t.Fatalf("newClaudeOrchestratorWiring: %v", err)
	}
	if providers := wiring.providers.Providers(); len(providers) != 1 || providers[0] != ports.ModelProviderClaude {
		t.Fatalf("providers = %v, want Claude alone", providers)
	}
	router := httpd.NewRouterWithControl(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{ClaudeOrchestrator: wiring.service}, httpd.ControlDeps{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	response, err := http.Post(server.URL+"/internal/claude-orchestrator/runs", "application/json",
		bytes.NewBufferString(`{"task":"Add two files","explicitOptIn":true,"maxRetries":0}`))
	if err != nil {
		t.Fatal(err)
	}
	var started httpd.ClaudeOrchestratorRunResponse
	if err := json.NewDecoder(response.Body).Decode(&started); err != nil || response.StatusCode != http.StatusAccepted {
		t.Fatalf("start = %d, %+v, %v", response.StatusCode, started, err)
	}
	_ = response.Body.Close()

	var final httpd.ClaudeOrchestratorRunResponse
	deadline := time.Now().Add(60 * time.Second)
	for {
		poll, err := http.Get(server.URL + "/internal/claude-orchestrator/runs/" + started.RunID)
		if err != nil {
			t.Fatal(err)
		}
		final = httpd.ClaudeOrchestratorRunResponse{}
		_ = json.NewDecoder(poll.Body).Decode(&final)
		_ = poll.Body.Close()
		if final.State == ports.RunStateHeld || final.State == ports.RunStateFailed || final.State == ports.RunStateCanceled ||
			(final.State == ports.RunStateCompleted && final.Commit != "") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %+v", final)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if final.State != ports.RunStateCompleted || final.Recommendation != ports.MergeOutcomeMerge {
		t.Fatalf("final status = %+v, want completed with a merge recommendation", final)
	}
	for _, name := range []string{"alpha.txt", "beta.txt"} {
		if got := e2eGit(t, project, "show", final.Commit+":"+name); got != name {
			t.Fatalf("committed %s = %q", name, got)
		}
	}
	if head := e2eGit(t, project, "rev-parse", "HEAD"); head != mainHead {
		t.Fatal("the run moved the main checkout")
	}
	// Every writer's worktree and branch is gone; only the run's remain.
	if list := e2eGit(t, project, "worktree", "list", "--porcelain"); strings.Count(list, "worktree ") != 2 {
		t.Fatalf("worktrees left behind:\n%s", list)
	}
	if branches := e2eGit(t, project, "branch", "--list", "ao/claude-orchestrator/*--*"); branches != "" {
		t.Fatalf("writer branches left behind: %q", branches)
	}
	if !busyOnce.Load() {
		t.Fatal("the busy answer was never sent")
	}
}
