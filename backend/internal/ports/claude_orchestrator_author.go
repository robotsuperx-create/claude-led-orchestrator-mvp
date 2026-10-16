package ports

import "context"

// CodeAuthorRequest is everything a coding model sees for one subtask
// attempt. It deliberately carries no local filesystem paths: files are named
// by their slash-separated path relative to the task worktree.
type CodeAuthorRequest struct {
	// Provider selects which model writes the code. An empty value lets the
	// author fall back to its configured default worker provider.
	Provider        ModelProvider  `json:"-"`
	Title           string         `json:"title,omitempty"`
	Instructions    string         `json:"instructions"`
	Attempt         int            `json:"attempt"`
	PreviousFailure string         `json:"previous_failure,omitempty"`
	RepositoryFiles []string       `json:"repository_files"`
	Files           []FileSnapshot `json:"files,omitempty"`
}

// FileSnapshot is the current content of one repository file.
type FileSnapshot struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// FileSelection names the repository files a coding model wants to read
// before it proposes edits.
type FileSelection struct {
	Paths []string `json:"paths"`
}

// FileEdit replaces Path with Content, or removes it when Delete is true.
type FileEdit struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Delete  bool   `json:"delete,omitempty"`
}

// EditProposal is the coding model's complete answer for one attempt.
type EditProposal struct {
	Summary string     `json:"summary"`
	Edits   []FileEdit `json:"edits"`
}

// CodeAuthor asks a model to write code for a delegated subtask. It only
// returns proposals; validating and applying them is the caller's job, inside
// a verified worktree.
type CodeAuthor interface {
	SelectFiles(ctx context.Context, request CodeAuthorRequest) (FileSelection, error)
	ProposeEdits(ctx context.Context, request CodeAuthorRequest) (EditProposal, error)
}
