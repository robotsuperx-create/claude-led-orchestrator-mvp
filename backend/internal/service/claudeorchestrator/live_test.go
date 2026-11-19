package claudeorchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// boardWorkspaces is a provisioner that reports fixed changes, so the board
// can be read without Git.
type boardWorkspaces struct {
	path    string
	changes []ports.LiveFileChange
}

func (b *boardWorkspaces) Prepare(_ context.Context, _ string) (ports.RunWorkspace, error) {
	return ports.RunWorkspace{Path: b.path, Branch: "ao/claude-orchestrator/run-live", BaseCommit: "abc"}, nil
}

func (b *boardWorkspaces) Finalize(_ context.Context, workspace ports.RunWorkspace, _ ports.OrchestrationResult) (ports.RunWorkspace, error) {
	return workspace, nil
}

func (b *boardWorkspaces) Changes(_ context.Context, _ ports.RunWorkspace) ([]ports.LiveFileChange, error) {
	return b.changes, nil
}

func TestLiveBoardFollowsEachStepAndTheRunsWriting(t *testing.T) {
	var service *Service
	plan := ports.ExecutionPlan{Summary: "parser", Subtasks: []ports.PlannedSubtask{
		{ID: "read", Title: "Read the parser", WorkerID: "w1"},
		{ID: "fix", Title: "Fix the quoting", WorkerID: "w2", DependsOn: []string{"read"}},
		{ID: "docs", Title: "Document it", WorkerID: "w3", DependsOn: []string{"fix"}},
	}}
	model := &modelFake{
		planFn: func(context.Context, ports.PlanRequest) (ports.ExecutionPlan, error) { return plan, nil },
		reviewFn: func(context.Context, ports.ReviewRequest) (ports.ReviewDecision, error) {
			return ports.ReviewDecision{Decision: ports.ReviewOutcomeApprove}, nil
		},
	}
	worker := &workerFake{executeFn: func(_ context.Context, request ports.WorkerRequest) (ports.WorkerExecution, error) {
		work, ok := service.LiveFor(context.Background(), "run-live")
		if !ok {
			t.Fatalf("no board while %s runs", request.Task.ID)
		}
		for _, step := range work.Steps {
			if step.ID == request.Task.ID && (step.State != ports.LiveStepRunning || step.Attempt != request.Attempt) {
				t.Errorf("step %s while it runs = %+v", step.ID, step)
			}
		}
		if request.Task.ID == "fix" {
			return ports.WorkerExecution{Status: ports.WorkerOutcomeFailed, Error: "quoting still wrong"}, nil
		}
		return ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted}, nil
	}}
	validator := &validatorFake{validateFn: func(context.Context, ports.ValidationRequest) (ports.ValidationReport, error) {
		return ports.ValidationReport{Passed: true}, nil
	}}
	root := t.TempDir()
	workspaces := &boardWorkspaces{path: root, changes: []ports.LiveFileChange{{Path: "parser.go", Added: 2, Lines: []string{"func parse() {", "}"}}}}
	service = New(Dependencies{
		Model: model, Worker: worker, Validator: validator, Memory: &memoryFake{}, Workspaces: workspaces,
		SitePages: func(worktree string) []string {
			if worktree != root {
				t.Errorf("pages looked for in %q", worktree)
			}
			return []string{"index.html"}
		},
	})

	if _, ok := service.LiveFor(context.Background(), "run-live"); ok {
		t.Fatal("a run that has not started has a board")
	}
	if _, err := service.Run(context.Background(), ports.OrchestrationRequest{RunID: "run-live", Task: "Fix the parser", MaxRetries: 1}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	work, ok := service.LiveFor(context.Background(), "run-live")
	if !ok || len(work.Steps) != 3 {
		t.Fatalf("board after the run = %+v, %v", work, ok)
	}
	want := map[string]ports.LiveStepState{"read": ports.LiveStepDone, "fix": ports.LiveStepFailed, "docs": ports.LiveStepBlocked}
	for i, step := range work.Steps {
		if step.Index != i || step.State != want[step.ID] {
			t.Errorf("step %d = %+v, want state %s", i, step, want[step.ID])
		}
	}
	if work.Steps[1].Attempt != 2 || work.Steps[1].Title != "Fix the quoting" {
		t.Errorf("the retried step = %+v, want its second attempt", work.Steps[1])
	}
	if !strings.Contains(work.Steps[1].Failure, "quoting still wrong") || work.Steps[0].Failure != "" {
		t.Errorf("failures on the board = %q / %q, want why the failed step failed and nothing on the done one", work.Steps[1].Failure, work.Steps[0].Failure)
	}
	if len(work.Files) != 1 || work.Files[0].Path != "parser.go" {
		t.Errorf("files = %+v", work.Files)
	}
	site, ok := service.SiteFor("run-live")
	if !ok || site.Root != root || strings.Join(site.Pages, ",") != "index.html" {
		t.Errorf("SiteFor = %+v, %v", site, ok)
	}
	if _, ok := service.SiteFor("run-other"); ok {
		t.Error("an unknown run has a site")
	}
}

func TestParseLiveDiffCountsLinesAndKeepsTheLastAdded(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/parser.go b/parser.go",
		"index 1111111..2222222 100644",
		"--- a/parser.go",
		"+++ b/parser.go",
		"@@ -3 +3,3 @@",
		"-\treturn nil",
		"+\tif quoted {",
		"+\t\treturn field",
		"+++ counter",
		"diff --git a/docs/new.md b/docs/new.md",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/docs/new.md",
		"@@ -0,0 +1 @@",
		"+# Parser",
		"diff --git a/logo.png b/logo.png",
		"Binary files a/logo.png and b/logo.png differ",
	}, "\n")
	files := parseLiveDiff(diff)
	if len(files) != 3 {
		t.Fatalf("files = %+v", files)
	}
	if files[0].Path != "parser.go" || files[0].Added != 3 || files[0].Removed != 1 || files[0].Lines[0] != "\tif quoted {" || files[0].Lines[2] != "++ counter" {
		t.Errorf("parser.go = %+v (indentation kept, a line that starts with ++ is code)", files[0])
	}
	if files[1].Path != "docs/new.md" || !files[1].New || files[1].Added != 1 {
		t.Errorf("docs/new.md = %+v", files[1])
	}
	if files[2].Path != "logo.png" || len(files[2].Lines) != 0 {
		t.Errorf("logo.png = %+v", files[2])
	}
}

func TestRunWorkspacesChangesReadsTheWorktreeWithoutTouchingIt(t *testing.T) {
	project := newGitProject(t)
	workspaces, _ := newTestRunWorkspaces(t, project)
	ctx := context.Background()
	workspace, err := workspaces.Prepare(ctx, "run-live")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	mustWrite(t, filepath.Join(workspace.Path, "README.md"), "# demo\nmore\n")
	mustWrite(t, filepath.Join(workspace.Path, "app.js"), "const key = 'sk-ant-api03-"+strings.Repeat("x", 40)+"';\nconsole.log(key);\n")

	files, err := workspaces.Changes(ctx, workspace)
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	byPath := map[string]ports.LiveFileChange{}
	for _, file := range files {
		byPath[file.Path] = file
	}
	if readme := byPath["README.md"]; readme.Added != 1 || readme.Lines[0] != "more" {
		t.Errorf("README.md = %+v", readme)
	}
	app := byPath["app.js"]
	if !app.New || app.Added != 2 {
		t.Fatalf("app.js = %+v", app)
	}
	if strings.Contains(strings.Join(app.Lines, "\n"), "sk-ant-api03") {
		t.Errorf("a secret reached the code stream: %q", app.Lines)
	}
	if status := runGit(t, workspace.Path, "status", "--porcelain"); !strings.Contains(status, "?? app.js") {
		t.Errorf("Changes staged the new file: %q", status)
	}
}
