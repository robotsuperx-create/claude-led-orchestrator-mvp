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
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/modelgateway"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	worktreesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/worktree"
)

// fakeProvider is an OpenAI-compatible chat endpoint whose answer is chosen
// from the system prompt, standing in for Claude or DeepSeek over real HTTP.
type fakeProvider struct {
	t      *testing.T
	mu     sync.Mutex
	inputs []string // user messages, in call order
	answer func(system, user string, call int) string
}

func (p *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Messages) != 2 {
		p.t.Errorf("unexpected provider request: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.inputs = append(p.inputs, request.Messages[1].Content)
	call := len(p.inputs)
	p.mu.Unlock()
	content, _ := json.Marshal(p.answer(request.Messages[0].Content, request.Messages[1].Content, call))
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":`+string(content)+`}}]}`)
}

func (p *fakeProvider) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.inputs...)
}

func e2eGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

// TestClaudeOrchestratorEndToEndWritesCodeOnRunBranch drives one run through
// the production composition: config -> daemon wiring -> HTTP API -> service
// -> Claude plan -> per-run worktree -> DeepSeek-authored edits -> fixed
// commands -> validator -> retry with the failure -> Claude review -> commit.
// Only the two model providers are fakes, served over real HTTP.
func TestClaudeOrchestratorEndToEndWritesCodeOnRunBranch(t *testing.T) {
	for _, tool := range []string{"git", "grep", "test"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "README.md"), []byte("# greeter\nWrite hello world to greet.txt.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2eGit(t, project, "init", "--quiet")
	e2eGit(t, project, "add", "--all")
	e2eGit(t, project, "-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "--quiet", "-m", "initial")
	mainHead := e2eGit(t, project, "rev-parse", "HEAD")

	claude := &fakeProvider{t: t, answer: func(system, _ string, _ int) string {
		if strings.Contains(system, "execution plan") {
			// The model also tries to choose the worktree; it must be ignored.
			return `{"summary":"Add greeting file","subtasks":[{"id":"greet","title":"Create greet.txt",` +
				`"instructions":"Create greet.txt containing exactly: hello world","worker_id":"coder","provider":"deepseek",` +
				`"metadata":{"worktree_path":"/etc"}}]}`
		}
		return `{"decision":"approve","summary":"greet.txt is correct","issues":[]}`
	}}
	deepSeek := &fakeProvider{t: t, answer: func(system, user string, _ int) string {
		if strings.Contains(system, "choose the files") {
			return "```json\n{\"paths\":[\"README.md\"]}\n```"
		}
		// First attempt is wrong; the retry sees the validator's failure.
		if !strings.Contains(user, "previous_failure") {
			return `{"summary":"first try","edits":[{"path":"greet.txt","content":"goodbye\n"}]}`
		}
		return `{"summary":"fix greeting","edits":[{"path":"greet.txt","content":"hello world\n"}]}`
	}}
	claudeServer := httptest.NewServer(claude)
	t.Cleanup(claudeServer.Close)
	deepSeekServer := httptest.NewServer(deepSeek)
	t.Cleanup(deepSeekServer.Close)

	cfg := config.Config{
		DataDir: filepath.Join(t.TempDir(), "ao"),
		ClaudeOrchestrator: config.ClaudeOrchestratorConfig{
			FeatureEnabled:   true,
			ClaudeProvider:   modelgateway.ProviderConfig{Provider: ports.ModelProviderClaude, BaseURL: claudeServer.URL, DefaultModel: "claude-test"},
			DeepSeekProvider: modelgateway.ProviderConfig{Provider: ports.ModelProviderDeepSeek, BaseURL: deepSeekServer.URL, DefaultModel: "deepseek-test"},
			Worker: config.ClaudeOrchestratorWorkerConfig{
				ProjectRoot:     project,
				DefaultProvider: ports.ModelProviderDeepSeek,
				Commands:        []config.ClaudeOrchestratorCommandConfig{{Name: "exists", Argv: []string{"test", "-f", "greet.txt"}}},
				Timeout:         30 * time.Second,
			},
			Validator: config.ClaudeOrchestratorValidatorConfig{
				Commands: []config.ClaudeOrchestratorCommandConfig{{Name: "content", Argv: []string{"grep", "-qx", "hello world", "greet.txt"}}},
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
	router := httpd.NewRouterWithControl(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{ClaudeOrchestrator: wiring.service}, httpd.ControlDeps{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	response, err := http.Post(server.URL+"/internal/claude-orchestrator/runs", "application/json",
		bytes.NewBufferString(`{"task":"Add a greeting file","explicitOptIn":true,"maxRetries":1}`))
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
		if final.State == ports.RunStateCompleted || final.State == ports.RunStateHeld ||
			final.State == ports.RunStateFailed || final.State == ports.RunStateCanceled {
			if final.Commit != "" || final.State != ports.RunStateCompleted {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %+v", final)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if final.State != ports.RunStateCompleted || final.Recommendation != ports.MergeOutcomeMerge {
		t.Fatalf("final status = %+v, want completed with a merge recommendation", final)
	}
	if final.Branch != "ao/claude-orchestrator/"+started.RunID || final.Commit == "" {
		t.Fatalf("final status = %+v, want the run branch and commit", final)
	}
	if got := e2eGit(t, project, "show", final.Commit+":greet.txt"); got != "hello world" {
		t.Fatalf("committed greet.txt = %q, want the retried content", got)
	}
	if parent := e2eGit(t, project, "rev-parse", final.Commit+"^"); parent != mainHead {
		t.Fatalf("run commit parent = %s, want the project HEAD %s", parent, mainHead)
	}
	// The user's checkout is untouched; the worktree lives under the data dir.
	if head := e2eGit(t, project, "rev-parse", "HEAD"); head != mainHead {
		t.Fatal("the run moved the main checkout")
	}
	if _, err := os.Stat(filepath.Join(project, "greet.txt")); !os.IsNotExist(err) {
		t.Fatal("the run wrote into the main checkout")
	}
	worktreeList := e2eGit(t, project, "worktree", "list", "--porcelain")
	if !strings.Contains(worktreeList, claudeOrchestratorManagedRoot(cfg)) {
		t.Fatalf("run worktree is not under the AO data dir:\n%s", worktreeList)
	}

	// Two authoring rounds (select + propose each), and the retry carried the
	// validator's failure; the file the model asked for was shown to it.
	deepSeekCalls := deepSeek.calls()
	if len(deepSeekCalls) != 4 {
		t.Fatalf("DeepSeek calls = %d, want 4 (two attempts x select/propose)", len(deepSeekCalls))
	}
	if !strings.Contains(deepSeekCalls[1], "Write hello world to greet.txt") {
		t.Fatalf("README content was not shown to the author: %s", deepSeekCalls[1])
	}
	if !strings.Contains(deepSeekCalls[3], `previous_failure`) || !strings.Contains(deepSeekCalls[3], "content") {
		t.Fatalf("retry did not carry the validation failure: %s", deepSeekCalls[3])
	}
	// Claude planned and reviewed; it never saw a local path.
	for _, input := range claude.calls() {
		if strings.Contains(input, cfg.DataDir) || strings.Contains(input, project) {
			t.Fatalf("a local path was sent to Claude: %s", input)
		}
	}
	for _, input := range deepSeekCalls {
		if strings.Contains(input, cfg.DataDir) || strings.Contains(input, project) {
			t.Fatalf("a local path was sent to DeepSeek: %s", input)
		}
	}
}
