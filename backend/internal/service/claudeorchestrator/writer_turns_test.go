package claudeorchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// turnsAuthor answers each reply of an attempt with the next scripted reply.
type turnsAuthor struct {
	requests []ports.CodeAuthorRequest
	replies  []func(ports.CodeAuthorRequest) (ports.EditProposal, error)
}

func (a *turnsAuthor) SelectFiles(context.Context, ports.CodeAuthorRequest) (ports.FileSelection, error) {
	return ports.FileSelection{Paths: []string{"index.html"}}, nil
}

func (a *turnsAuthor) ProposeEdits(_ context.Context, request ports.CodeAuthorRequest) (ports.EditProposal, error) {
	a.requests = append(a.requests, request)
	return a.replies[len(a.requests)-1](request)
}

func TestAWriterFinishesALargeSubtaskOverSeveralShortReplies(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "index.html"), "<main></main>\n")
	runner := &lsFilesRunner{files: []string{"index.html", "css/site.css", "js/site.js"}}
	author := &turnsAuthor{replies: []func(ports.CodeAuthorRequest) (ports.EditProposal, error){
		func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{Summary: "styles", More: true, Edits: []ports.FileEdit{{Path: "css/site.css", Content: "body { margin: 0; }\n"}}}, nil
		},
		func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{}, ports.ErrReplyCutOff
		},
		func(request ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{Summary: "page and script", Edits: []ports.FileEdit{
				{Path: "index.html", Replace: []ports.TextReplacement{{Old: "<main></main>", New: "<main>hi</main>"}}},
				{Path: "js/site.js", Content: "\"use strict\";\n"},
			}}, nil
		},
	}}
	runtime, _ := newAuthoringRuntime(t, worktree, runner, author)
	execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil || execution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("Execute = %+v, %v", execution, err)
	}
	if len(author.requests) != 3 {
		t.Fatalf("replies asked = %d, want 3", len(author.requests))
	}
	for i, request := range author.requests {
		if request.Turn != i+1 {
			t.Errorf("reply %d had turn %d", i+1, request.Turn)
		}
	}
	third := author.requests[2]
	if len(third.Progress) != 1 || third.Progress[0] != "styles" || !strings.Contains(third.PreviousFailure, "cut off") {
		t.Errorf("third reply was asked with progress %q and failure %q", third.Progress, third.PreviousFailure)
	}
	var sawStyles bool
	for _, file := range third.Files {
		sawStyles = sawStyles || (file.Path == "css/site.css" && strings.Contains(file.Content, "margin"))
	}
	if !sawStyles {
		t.Errorf("the later reply did not see the file an earlier one wrote: %+v", third.Files)
	}
	if got := mustRead(t, filepath.Join(worktree, "index.html")); got != "<main>hi</main>\n" {
		t.Errorf("index.html = %q", got)
	}
	if !strings.Contains(execution.Summary, "wrote css/site.css, index.html, js/site.js") {
		t.Errorf("summary = %q", execution.Summary)
	}
}

func TestAWriterThatKeepsGettingCutOffFailsTheAttemptWithTheReason(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "index.html"), "<main></main>\n")
	runner := &lsFilesRunner{files: []string{"index.html"}}
	cutOff := func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
		return ports.EditProposal{}, ports.ErrReplyCutOff
	}
	author := &turnsAuthor{}
	for range maxAuthorTurns {
		author.replies = append(author.replies, cutOff)
	}
	runtime, _ := newAuthoringRuntime(t, worktree, runner, author)
	execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if execution.Status != ports.WorkerOutcomeFailed || !strings.Contains(execution.Error, "cut off") {
		t.Fatalf("execution = %+v, want a failed attempt that says the replies were cut off", execution)
	}
	if len(author.requests) != maxAuthorTurns {
		t.Errorf("replies asked = %d, want %d", len(author.requests), maxAuthorTurns)
	}
}
