package claudeorchestrator

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// lsFilesRunner answers the worker's fixed `git ls-files` call and records
// every other command, which stands in for the operator's allowlist.
type lsFilesRunner struct {
	files    []string
	commands []workerRuntimeCommandCall
	// onCommand runs for allowlisted commands, letting a test observe the
	// worktree at the moment commands execute.
	onCommand func(directory string)
}

func (r *lsFilesRunner) Run(context.Context, []string) (CommandOutput, error) {
	return CommandOutput{}, errors.New("unexpected Run without a directory")
}

func (r *lsFilesRunner) RunInDirectory(_ context.Context, directory string, argv []string) (CommandOutput, error) {
	if len(argv) > 1 && argv[0] == "git" && argv[1] == "ls-files" {
		return CommandOutput{Stdout: strings.Join(r.files, "\x00") + "\x00"}, nil
	}
	r.commands = append(r.commands, workerRuntimeCommandCall{directory: directory, argv: append([]string(nil), argv...)})
	if r.onCommand != nil {
		r.onCommand(directory)
	}
	return CommandOutput{}, nil
}

type authorFake struct {
	selection     ports.FileSelection
	proposal      ports.EditProposal
	selectErr     error
	proposeErr    error
	selectRequest ports.CodeAuthorRequest
	editRequest   ports.CodeAuthorRequest
	// reviewRequest is the writer's look at its own finished work.
	reviewRequest *ports.CodeAuthorRequest
}

func (f *authorFake) SelectFiles(_ context.Context, request ports.CodeAuthorRequest) (ports.FileSelection, error) {
	f.selectRequest = request
	return f.selection, f.selectErr
}

func (f *authorFake) ProposeEdits(_ context.Context, request ports.CodeAuthorRequest) (ports.EditProposal, error) {
	if request.Review {
		f.reviewRequest = &request
		return ports.EditProposal{Summary: "nothing to fix"}, nil
	}
	f.editRequest = request
	return f.proposal, f.proposeErr
}

func newAuthoringRuntime(t *testing.T, worktree string, runner *lsFilesRunner, author ports.CodeAuthor) (*WorkerRuntime, *workerRuntimeValidatorFake) {
	t.Helper()
	validator := &workerRuntimeValidatorFake{report: ports.ValidationReport{Passed: true}}
	runtime, err := NewWorkerRuntime(&workerRuntimeWorktreeFake{}, validator, runner, WorkerRuntimeConfig{
		ProjectRoot: filepath.Dir(worktree),
		Commands:    []ValidationCommand{{Name: "test", Argv: []string{"go", "test", "./..."}}},
		Timeout:     time.Second,
		Author:      author,
	})
	if err != nil {
		t.Fatalf("NewWorkerRuntime() error = %v", err)
	}
	return runtime, validator
}

func authoringRequest(worktree string) ports.WorkerRequest {
	return ports.WorkerRequest{
		Task: ports.PlannedSubtask{
			ID: "t1", Title: "Greet", Instructions: "add a greeting", Provider: ports.ModelProviderDeepSeek,
			Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: worktree},
		},
		Attempt:         2,
		PreviousFailure: "tests failed: missing greeting",
	}
}

func TestWorkerAuthorsCodeBeforeRunningCommands(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "main.go"), "package main\n")
	mustWrite(t, filepath.Join(worktree, ".env"), "SECRET=1")
	var contentWhenCommandsRan string
	runner := &lsFilesRunner{
		files: []string{"main.go", ".env", "README.md", "assets/logo.PNG", "fonts/brand.woff2"},
		onCommand: func(directory string) {
			contentWhenCommandsRan = mustRead(t, filepath.Join(directory, "greet.go"))
		},
	}
	author := &authorFake{
		selection: ports.FileSelection{Paths: []string{"main.go", ".env"}},
		proposal: ports.EditProposal{Summary: "add greeting", Edits: []ports.FileEdit{
			{Path: "greet.go", Content: "package main\n\nconst greeting = \"hi\"\n"},
		}},
	}
	runtime, _ := newAuthoringRuntime(t, worktree, runner, author)

	execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if execution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("execution = %+v, want completed", execution)
	}
	if !strings.Contains(contentWhenCommandsRan, "greeting") {
		t.Fatalf("commands ran before the edit was applied; saw %q", contentWhenCommandsRan)
	}
	if !strings.Contains(execution.Summary, "add greeting") || !strings.Contains(execution.Summary, "wrote greet.go") {
		t.Fatalf("summary = %q, want model summary and changed files", execution.Summary)
	}
	// The model is told about the retry and sees no protected files.
	request := author.selectRequest
	if request.Provider != ports.ModelProviderDeepSeek || request.Attempt != 2 || request.PreviousFailure != "tests failed: missing greeting" {
		t.Fatalf("author request = %+v, want provider, attempt and previous failure", request)
	}
	if strings.Join(request.RepositoryFiles, ",") != "main.go,README.md" {
		t.Fatalf("repository files = %v, want protected .env and binary assets omitted", request.RepositoryFiles)
	}
	if len(author.editRequest.Files) != 1 || author.editRequest.Files[0].Path != "main.go" {
		t.Fatalf("files shown to the model = %+v, want only main.go", author.editRequest.Files)
	}
}

func TestWorkerReportsAuthorFailuresAsRetryableAttempts(t *testing.T) {
	for name, author := range map[string]*authorFake{
		"select error":  {selectErr: errors.New("provider timeout")},
		"propose error": {proposeErr: errors.New("invalid JSON")},
		"unsafe edit":   {proposal: ports.EditProposal{Edits: []ports.FileEdit{{Path: "../escape.go", Content: "x"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			worktree := t.TempDir()
			runner := &lsFilesRunner{files: []string{"main.go"}}
			runtime, validator := newAuthoringRuntime(t, worktree, runner, author)
			execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
			if err != nil {
				t.Fatalf("Execute() error = %v; an author failure must be a failed attempt, not an error", err)
			}
			if execution.Status != ports.WorkerOutcomeFailed || execution.Error == "" {
				t.Fatalf("execution = %+v, want failed with a reason the model can act on", execution)
			}
			if len(runner.commands) != 0 || validator.calls != 0 {
				t.Fatal("commands or validation ran after the author failed")
			}
		})
	}
}

// The model no longer sees the repository listing while it writes, so a
// rewrite or delete of an existing file it was not given is refused with a
// reason; new files, files it read, and replacements are fine.
func TestWorkerRefusesToRewriteFilesTheModelDidNotRead(t *testing.T) {
	for name, tc := range map[string]struct {
		edit ports.FileEdit
		ok   bool
	}{
		"rewrite an unread file": {edit: ports.FileEdit{Path: "README.md", Content: "# replaced\n"}},
		"delete an unread file":  {edit: ports.FileEdit{Path: "README.md", Delete: true}},
		"rewrite a file it read": {edit: ports.FileEdit{Path: "main.go", Content: "package app\n"}, ok: true},
		"create a new file":      {edit: ports.FileEdit{Path: "greet.go", Content: "package main\n"}, ok: true},
		"replace in an unread file": {
			edit: ports.FileEdit{Path: "README.md", Replace: []ports.TextReplacement{{Old: "# demo", New: "# Demo"}}}, ok: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			worktree := t.TempDir()
			mustWrite(t, filepath.Join(worktree, "main.go"), "package main\n")
			mustWrite(t, filepath.Join(worktree, "README.md"), "# demo\n")
			runner := &lsFilesRunner{files: []string{"main.go", "README.md"}}
			author := &authorFake{
				selection: ports.FileSelection{Paths: []string{"main.go"}},
				proposal:  ports.EditProposal{Edits: []ports.FileEdit{tc.edit}},
			}
			runtime, _ := newAuthoringRuntime(t, worktree, runner, author)
			execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if tc.ok {
				if execution.Status != ports.WorkerOutcomeCompleted {
					t.Fatalf("execution = %+v, want completed", execution)
				}
				return
			}
			if execution.Status != ports.WorkerOutcomeFailed || !strings.Contains(execution.Error, "README.md") ||
				!strings.Contains(execution.Error, "were not given") {
				t.Fatalf("execution = %+v, want a failed attempt naming README.md", execution)
			}
			if got := mustRead(t, filepath.Join(worktree, "README.md")); got != "# demo\n" {
				t.Fatalf("README.md = %q, want it untouched", got)
			}
		})
	}
}

// The writer receives the full guidance of its subtask's skills.
func TestWorkerSendsTheSubtasksSkillsToTheWriter(t *testing.T) {
	worktree := t.TempDir()
	runner := &lsFilesRunner{files: []string{"index.html"}}
	author := &authorFake{proposal: ports.EditProposal{Edits: []ports.FileEdit{{Path: "about.html", Content: "<h1>About</h1>\n"}}}}
	runtime, _ := newAuthoringRuntime(t, worktree, runner, author)
	request := authoringRequest(worktree)
	request.Task.Skills = []string{"design-taste", "unknown"}
	if _, err := runtime.Execute(context.Background(), request); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	skills := author.editRequest.Skills
	if len(skills) != 1 || skills[0].Name != "design-taste" || !strings.Contains(skills[0].Guidance, "accent") {
		t.Fatalf("writer skills = %+v, want the design-taste guidance", skills)
	}
}
