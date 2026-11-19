package claudeorchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// turnsAuthor answers each reply of an attempt with the next scripted reply.
type turnsAuthor struct {
	requests []ports.CodeAuthorRequest
	replies  []func(ports.CodeAuthorRequest) (ports.EditProposal, error)
	// reviews are the writer's looks at its finished work; unless a test
	// scripts them, it finds nothing to fix.
	reviews []ports.CodeAuthorRequest
	review  func(ports.CodeAuthorRequest) (ports.EditProposal, error)
}

func (a *turnsAuthor) SelectFiles(context.Context, ports.CodeAuthorRequest) (ports.FileSelection, error) {
	return ports.FileSelection{Paths: []string{"index.html"}}, nil
}

func (a *turnsAuthor) ProposeEdits(_ context.Context, request ports.CodeAuthorRequest) (ports.EditProposal, error) {
	if request.Review {
		a.reviews = append(a.reviews, request)
		if a.review != nil {
			return a.review(request)
		}
		return ports.EditProposal{Summary: "nothing to fix"}, nil
	}
	a.requests = append(a.requests, request)
	if len(a.requests) > len(a.replies) {
		return ports.EditProposal{Summary: "done"}, nil
	}
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
	// Replies that keep running past the limit with nothing complete in
	// them end the attempt after maxCutOffsInARow, not after every turn.
	runtime, _ := newAuthoringRuntime(t, worktree, runner, author)
	execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if execution.Status != ports.WorkerOutcomeFailed || !strings.Contains(execution.Error, "cut off") {
		t.Fatalf("execution = %+v, want a failed attempt that says the replies were cut off", execution)
	}
	if len(author.requests) != maxCutOffsInARow {
		t.Errorf("replies asked = %d, want %d", len(author.requests), maxCutOffsInARow)
	}
}

// A writer that was not given a file it must change asks for it, and is
// called again with it rather than failing the attempt.
func TestAWriterThatAsksToReadAFileIsShownItAndWrites(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "index.html"), "<main></main>\n")
	mustWrite(t, filepath.Join(worktree, "css", "styles.css"), ":root { --red: #E41E2B; }\n")
	runner := &lsFilesRunner{files: []string{"index.html", "css/styles.css"}}
	author := &turnsAuthor{replies: []func(ports.CodeAuthorRequest) (ports.EditProposal, error){
		func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{Summary: "need the tokens", Read: []string{"css/styles.css"}}, nil
		},
		func(request ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{Summary: "used the red token", Edits: []ports.FileEdit{
				{Path: "css/styles.css", Replace: []ports.TextReplacement{{Old: "--red: #E41E2B;", New: "--red: #E41E2B; --ink: #1A1A1A;"}}},
			}}, nil
		},
	}}
	runtime, _ := newAuthoringRuntime(t, worktree, runner, author)
	execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil || execution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("Execute = %+v, %v", execution, err)
	}
	if len(author.requests) != 2 {
		t.Fatalf("replies asked = %d, want 2", len(author.requests))
	}
	var shown bool
	for _, file := range author.requests[1].Files {
		shown = shown || file.Path == "css/styles.css"
	}
	if !shown {
		t.Fatalf("the second reply was not shown the file asked for: %+v", author.requests[1].Files)
	}
	if got := mustRead(t, filepath.Join(worktree, "css", "styles.css")); !strings.Contains(got, "--ink: #1A1A1A;") {
		t.Fatalf("css/styles.css = %q", got)
	}
}

// A design-tier subtask asks its writer as design work, on every reply.
func TestADesignTierSubtaskIsWrittenAsDesignWork(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "index.html"), "<main></main>\n")
	runner := &lsFilesRunner{files: []string{"index.html"}}
	author := &turnsAuthor{replies: []func(ports.CodeAuthorRequest) (ports.EditProposal, error){
		func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{Summary: "hero", Edits: []ports.FileEdit{{Path: "index.html", Replace: []ports.TextReplacement{{Old: "<main></main>", New: "<main>hero</main>"}}}}}, nil
		},
	}}
	runtime, _ := newAuthoringRuntime(t, worktree, runner, author)
	request := authoringRequest(worktree)
	request.Task.Tier = ports.SubtaskTierDesign
	if _, err := runtime.Execute(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(author.requests) != 1 || author.requests[0].Tier != ports.SubtaskTierDesign {
		t.Fatalf("requests = %+v, want the design tier", author.requests)
	}
}

// A reply whose edits are rejected costs nothing already written: the writer
// is told why and sends them again in the same attempt.
func TestARejectedReplyIsSentBackWithoutLosingEarlierWork(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "index.html"), "<main></main>\n")
	runner := &lsFilesRunner{files: []string{"index.html", "css/site.css"}}
	author := &turnsAuthor{replies: []func(ports.CodeAuthorRequest) (ports.EditProposal, error){
		func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{Summary: "styles", More: true, Edits: []ports.FileEdit{{Path: "css/site.css", Content: "body { margin: 0; }\n"}}}, nil
		},
		func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			// The old text is misremembered: nothing in this reply applies.
			return ports.EditProposal{Summary: "page", Edits: []ports.FileEdit{{Path: "index.html", Replace: []ports.TextReplacement{{Old: "<main> </main>", New: "<main>hi</main>"}}}}}, nil
		},
		func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{Summary: "page", Edits: []ports.FileEdit{{Path: "index.html", Replace: []ports.TextReplacement{{Old: "<main></main>", New: "<main>hi</main>"}}}}}, nil
		},
	}}
	runtime, _ := newAuthoringRuntime(t, worktree, runner, author)
	execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil || execution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("Execute = %+v, %v", execution, err)
	}
	if third := author.requests[2]; !strings.Contains(third.PreviousFailure, "rejected") {
		t.Fatalf("the corrected reply was asked with %q, want the rejection", third.PreviousFailure)
	}
	if got := mustRead(t, filepath.Join(worktree, "css", "site.css")); got != "body { margin: 0; }\n" {
		t.Fatalf("earlier work was lost: css/site.css = %q", got)
	}
	if got := mustRead(t, filepath.Join(worktree, "index.html")); got != "<main>hi</main>\n" {
		t.Fatalf("index.html = %q", got)
	}
}

// Once the writer says it is done, it looks at its own work once, with every
// file it wrote in front of it, and its fixes are applied.
func TestAWriterReviewsItsOwnWorkOnceBeforeHandingItOver(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "index.html"), "<main></main>\n")
	runner := &lsFilesRunner{files: []string{"index.html", "css/a.css", "js/a.js"}}
	author := &turnsAuthor{
		replies: []func(ports.CodeAuthorRequest) (ports.EditProposal, error){
			func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
				return ports.EditProposal{Summary: "styles", More: true, Edits: []ports.FileEdit{{Path: "css/a.css", Content: "a{}\n"}}}, nil
			},
			func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
				return ports.EditProposal{Summary: "script", Edits: []ports.FileEdit{{Path: "js/a.js", Content: "\"use strict\";\n"}}}, nil
			},
		},
		review: func(request ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{Summary: "focus style was missing", Edits: []ports.FileEdit{{Path: "css/a.css", Replace: []ports.TextReplacement{{Old: "a{}", New: "a{} a:focus-visible{outline:2px solid}"}}}}}, nil
		},
	}
	runtime, _ := newAuthoringRuntime(t, worktree, runner, author)
	execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil || execution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("Execute = %+v, %v", execution, err)
	}
	if len(author.reviews) != 1 {
		t.Fatalf("reviews = %d, want exactly one", len(author.reviews))
	}
	shown := map[string]bool{}
	for _, file := range author.reviews[0].Files {
		shown[file.Path] = true
	}
	if !shown["css/a.css"] || !shown["js/a.js"] {
		t.Fatalf("the review did not see every file the attempt wrote: %v", shown)
	}
	if got := mustRead(t, filepath.Join(worktree, "css", "a.css")); !strings.Contains(got, "focus-visible") {
		t.Fatalf("the review's fix was not applied: %q", got)
	}
}

// With too little of the attempt left for another reply, what is written is
// kept instead of being lost to the timeout.
func TestAWriterStopsWhileItCanStillKeepItsWork(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "index.html"), "<main></main>\n")
	runner := &lsFilesRunner{files: []string{"index.html", "css/a.css"}}
	author := &turnsAuthor{replies: []func(ports.CodeAuthorRequest) (ports.EditProposal, error){
		func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			time.Sleep(200 * time.Millisecond)
			return ports.EditProposal{Summary: "styles", More: true, Edits: []ports.FileEdit{{Path: "css/a.css", Content: "a{}\n"}}}, nil
		},
	}}
	// The first reply takes 200ms; with less than that left, no second one.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	summary, err := authorInWorktree(ctx, runner, author, worktree, authoringRequest(worktree))
	if err != nil || !strings.Contains(summary, "css/a.css") {
		t.Fatalf("summary = %q, %v", summary, err)
	}
	if len(author.requests) != 1 || len(author.reviews) != 0 {
		t.Fatalf("replies = %d, reviews = %d; want one reply and no more calls", len(author.requests), len(author.reviews))
	}
}

// A writer that answers in prose is told so, with its words, and asked again,
// instead of the attempt failing.
func TestAProseReplyIsSentBackToTheWriter(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "index.html"), "<main></main>\n")
	runner := &lsFilesRunner{files: []string{"index.html"}}
	author := &turnsAuthor{replies: []func(ports.CodeAuthorRequest) (ports.EditProposal, error){
		func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{}, fmt.Errorf("invalid response (%w); it began %q", ports.ErrReplyNotJSON, "The dialog is written")
		},
		func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{Summary: "page", Edits: []ports.FileEdit{{Path: "index.html", Replace: []ports.TextReplacement{{Old: "<main></main>", New: "<main>hi</main>"}}}}}, nil
		},
	}}
	runtime, _ := newAuthoringRuntime(t, worktree, runner, author)
	execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil || execution.Status != ports.WorkerOutcomeCompleted {
		t.Fatalf("Execute = %+v, %v", execution, err)
	}
	if second := author.requests[1]; !strings.Contains(second.PreviousFailure, "not the JSON object") || !strings.Contains(second.PreviousFailure, "The dialog is written") {
		t.Fatalf("the writer was asked again with %q", second.PreviousFailure)
	}
}

func TestAWriterThatKeepsTalkingFailsTheAttemptAfterAFewTries(t *testing.T) {
	worktree := t.TempDir()
	mustWrite(t, filepath.Join(worktree, "index.html"), "<main></main>\n")
	author := &turnsAuthor{}
	for range maxAuthorTurns {
		author.replies = append(author.replies, func(ports.CodeAuthorRequest) (ports.EditProposal, error) {
			return ports.EditProposal{}, fmt.Errorf("invalid response (%w)", ports.ErrReplyNotJSON)
		})
	}
	runtime, _ := newAuthoringRuntime(t, worktree, &lsFilesRunner{files: []string{"index.html"}}, author)
	execution, err := runtime.Execute(context.Background(), authoringRequest(worktree))
	if err != nil || execution.Status != ports.WorkerOutcomeFailed || len(author.requests) != maxProseInARow {
		t.Fatalf("execution = %+v, %v after %d replies; want a failed attempt after %d", execution, err, len(author.requests), maxProseInARow)
	}
}
