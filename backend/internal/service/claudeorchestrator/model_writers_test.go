package claudeorchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// writerAuthor answers each subtask with edits chosen by the test.
type writerAuthor struct {
	edits func(request ports.CodeAuthorRequest) ([]ports.FileEdit, error)
}

func (a writerAuthor) SelectFiles(context.Context, ports.CodeAuthorRequest) (ports.FileSelection, error) {
	return ports.FileSelection{Paths: []string{"README.md"}}, nil
}

func (a writerAuthor) ProposeEdits(_ context.Context, request ports.CodeAuthorRequest) (ports.EditProposal, error) {
	edits, err := a.edits(request)
	return ports.EditProposal{Summary: "edited for " + request.Title, Edits: edits}, err
}

func newTestModelWriters(t *testing.T, edits func(ports.CodeAuthorRequest) ([]ports.FileEdit, error)) *ModelWriters {
	t.Helper()
	writers, err := NewModelWriters(ExecCommandRunner{}, writerAuthor{edits: edits})
	if err != nil {
		t.Fatalf("NewModelWriters: %v", err)
	}
	return writers
}

func writerRequest(worktree, id string) ports.AgentImplementRequest {
	return ports.AgentImplementRequest{
		WorktreePath: worktree,
		Task:         ports.PlannedSubtask{ID: id, Title: id, Instructions: "write " + id, Provider: ports.ModelProviderClaude},
		Attempt:      1,
	}
}

// writerGit runs git the way the writers do, with the machine's own Git
// configuration (core.autocrlf on Windows, for example), and returns its
// trimmed output.
func writerGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	output, err := ExecCommandRunner{}.RunInDirectory(context.Background(), dir, append([]string{"git"}, args...))
	if err != nil || output.ExitCode != 0 {
		t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, output.Stderr)
	}
	return strings.TrimSpace(output.Stdout)
}

// readText reads a checked-out file with its line endings normalized.
func readText(t *testing.T, path string) string {
	t.Helper()
	return strings.ReplaceAll(mustRead(t, path), "\r\n", "\n")
}

// assertNoWriterLeftovers checks that every attempt removed its worktree and
// that no writer branch is left behind.
func assertNoWriterLeftovers(t *testing.T, worktree string) {
	t.Helper()
	list := writerGit(t, worktree, "worktree", "list", "--porcelain")
	if count := strings.Count(list, "worktree "); count != 2 {
		t.Fatalf("worktrees = %d, want only the project and the run:\n%s", count, list)
	}
	if branches := writerGit(t, worktree, "branch", "--list", "ao/claude-orchestrator/run-1--*"); branches != "" {
		t.Fatalf("writer branches were left behind: %q", branches)
	}
	matches, err := filepath.Glob(worktree + ".writer-*")
	if err != nil || len(matches) != 0 {
		t.Fatalf("writer directories were left behind: %v, %v", matches, err)
	}
}

func TestModelWritersWriteIndependentSubtasksAtTheSameTime(t *testing.T) {
	_, worktree := newAgentRun(t)
	writers := newTestModelWriters(t, func(request ports.CodeAuthorRequest) ([]ports.FileEdit, error) {
		return []ports.FileEdit{{Path: request.Title + ".txt", Content: "work of " + request.Title + "\n"}}, nil
	})
	before := runGit(t, worktree, "rev-parse", "HEAD")

	type outcome struct {
		work ports.AgentWork
		err  error
	}
	results := make(chan outcome, 2)
	for _, id := range []string{"left", "right"} {
		go func(id string) {
			work, err := writers.Implement(context.Background(), writerRequest(worktree, id))
			results <- outcome{work, err}
		}(id)
	}
	var works []ports.AgentWork
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatalf("Implement: %v", got.err)
		}
		if got.work.Base != before || got.work.Branch == "" {
			t.Fatalf("work = %+v, want a branch cut from the run head %s", got.work, before)
		}
		works = append(works, got.work)
	}
	for _, work := range works {
		if err := writers.Integrate(context.Background(), worktree, work); err != nil {
			t.Fatalf("Integrate %s: %v", work.Branch, err)
		}
	}
	for _, name := range []string{"left.txt", "right.txt"} {
		if got := readText(t, filepath.Join(worktree, name)); !strings.HasPrefix(got, "work of ") {
			t.Fatalf("%s = %q", name, got)
		}
	}
	if status := writerGit(t, worktree, "status", "--porcelain"); status != "" {
		t.Fatalf("run worktree is dirty: %q", status)
	}
	assertNoWriterLeftovers(t, worktree)
}

func TestModelWritersConflictsFailTheAttemptAndLeaveTheRunAlone(t *testing.T) {
	_, worktree := newAgentRun(t)
	writers := newTestModelWriters(t, func(request ports.CodeAuthorRequest) ([]ports.FileEdit, error) {
		return []ports.FileEdit{{Path: "README.md", Content: "rewritten by " + request.Title + "\n"}}, nil
	})
	first, err := writers.Implement(context.Background(), writerRequest(worktree, "one"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := writers.Implement(context.Background(), writerRequest(worktree, "two"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writers.Integrate(context.Background(), worktree, first); err != nil {
		t.Fatalf("first Integrate: %v", err)
	}
	head := runGit(t, worktree, "rev-parse", "HEAD")
	err = writers.Integrate(context.Background(), worktree, second)
	if !errors.Is(err, ports.ErrAgentAttemptFailed) || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("second Integrate = %v, want a retryable conflict naming README.md", err)
	}
	if after := runGit(t, worktree, "rev-parse", "HEAD"); after != head {
		t.Fatal("a conflicting merge moved the run")
	}
	if got := readText(t, filepath.Join(worktree, "README.md")); got != "rewritten by one\n" {
		t.Fatalf("README.md = %q, want the first subtask's work", got)
	}
	assertNoWriterLeftovers(t, worktree)

	// The retry is cut from the run's latest commit, so it builds on the first.
	retry, err := writers.Implement(context.Background(), writerRequest(worktree, "two"))
	if err != nil || retry.Base != head {
		t.Fatalf("retry = %+v, %v; want a branch from %s", retry, err, head)
	}
	if err := writers.Integrate(context.Background(), worktree, retry); err != nil {
		t.Fatalf("retry Integrate: %v", err)
	}
	assertNoWriterLeftovers(t, worktree)
}

func TestModelWritersWithNothingToChangeMergeNothing(t *testing.T) {
	_, worktree := newAgentRun(t)
	writers := newTestModelWriters(t, func(ports.CodeAuthorRequest) ([]ports.FileEdit, error) {
		// The same content as the commit: applied, but no change.
		return []ports.FileEdit{{Path: "README.md", Content: "# demo\n"}}, nil
	})
	head := runGit(t, worktree, "rev-parse", "HEAD")
	work, err := writers.Implement(context.Background(), writerRequest(worktree, "noop"))
	if err != nil || work.Branch != "" {
		t.Fatalf("Implement = %+v, %v; want work with nothing to merge", work, err)
	}
	if err := writers.Integrate(context.Background(), worktree, work); err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	if after := runGit(t, worktree, "rev-parse", "HEAD"); after != head {
		t.Fatal("an empty change moved the run")
	}
	assertNoWriterLeftovers(t, worktree)
}

func TestModelWritersTurnModelFailuresIntoRetryableAttempts(t *testing.T) {
	_, worktree := newAgentRun(t)
	for name, edits := range map[string]func(ports.CodeAuthorRequest) ([]ports.FileEdit, error){
		"the model fails": func(ports.CodeAuthorRequest) ([]ports.FileEdit, error) {
			return nil, errors.New("model unavailable")
		},
		"a protected path": func(ports.CodeAuthorRequest) ([]ports.FileEdit, error) {
			return []ports.FileEdit{{Path: ".env", Content: "TOKEN=x\n"}}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			writers := newTestModelWriters(t, edits)
			_, err := writers.Implement(context.Background(), writerRequest(worktree, "bad"))
			if !errors.Is(err, ports.ErrAgentAttemptFailed) {
				t.Fatalf("Implement = %v, want a retryable failed attempt", err)
			}
			assertNoWriterLeftovers(t, worktree)
		})
	}
}

func TestModelWritersKeepTheRunsCheckOutputBeforeMerging(t *testing.T) {
	_, worktree := newAgentRun(t)
	writers := newTestModelWriters(t, func(ports.CodeAuthorRequest) ([]ports.FileEdit, error) {
		return []ports.FileEdit{{Path: "feature.txt", Content: "feature\n"}}, nil
	})
	work, err := writers.Implement(context.Background(), writerRequest(worktree, "feature"))
	if err != nil {
		t.Fatal(err)
	}
	// An earlier subtask's checks left a file behind in the run worktree.
	mustWrite(t, filepath.Join(worktree, "generated.txt"), "generated\n")
	if err := writers.Integrate(context.Background(), worktree, work); err != nil {
		t.Fatalf("Integrate: %v", err)
	}
	for _, name := range []string{"generated.txt", "feature.txt"} {
		if _, err := os.Stat(filepath.Join(worktree, name)); err != nil {
			t.Fatalf("%s is missing: %v", name, err)
		}
	}
	if status := writerGit(t, worktree, "status", "--porcelain"); status != "" {
		t.Fatalf("run worktree is dirty: %q", status)
	}
}

func TestWorkerRuntimeRunsChecksOnModelWritersWork(t *testing.T) {
	_, worktree := newAgentRun(t)
	writers := newTestModelWriters(t, func(ports.CodeAuthorRequest) ([]ports.FileEdit, error) {
		return []ports.FileEdit{{Path: "greet.txt", Content: "hello\n"}}, nil
	})
	var sawGreeting bool
	runner := &lsFilesRunner{onCommand: func(directory string) {
		_, err := os.Stat(filepath.Join(directory, "greet.txt"))
		sawGreeting = err == nil
	}}
	validator := &workerRuntimeValidatorFake{report: ports.ValidationReport{Passed: true}}
	runtime, err := NewWorkerRuntime(&workerRuntimeWorktreeFake{}, validator, runner, WorkerRuntimeConfig{
		ProjectRoot: filepath.Dir(worktree),
		Commands:    []ValidationCommand{{Name: "test", Argv: []string{"go", "test", "./..."}}},
		Timeout:     30 * time.Second,
		Agents:      writers,
	})
	if err != nil {
		t.Fatalf("NewWorkerRuntime: %v", err)
	}
	request := ports.WorkerRequest{
		Task: ports.PlannedSubtask{
			ID: "greet", Title: "greet", Instructions: "add a greeting", Provider: ports.ModelProviderClaude,
			Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: worktree},
		},
		Attempt: 1,
	}
	execution, err := runtime.Execute(context.Background(), request)
	if err != nil || execution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("Execute = %+v, %v", execution, err)
	}
	if !sawGreeting {
		t.Fatal("the checks ran before the writer's work was merged into the run")
	}
}

func TestNewModelWritersRequiresGitAndAnAuthor(t *testing.T) {
	if _, err := NewModelWriters(nil, writerAuthor{}); err == nil {
		t.Fatal("accepted a nil git runner")
	}
	if _, err := NewModelWriters(ExecCommandRunner{}, nil); err == nil {
		t.Fatal("accepted a nil author")
	}
}
