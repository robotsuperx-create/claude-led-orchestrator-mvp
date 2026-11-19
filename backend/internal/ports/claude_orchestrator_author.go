package ports

import (
	"context"
	"errors"
)

// CodeAuthorRequest is everything a coding model sees for one subtask
// attempt. It deliberately carries no local filesystem paths: files are named
// by their slash-separated path relative to the task worktree.
type CodeAuthorRequest struct {
	// Provider selects which model writes the code. An empty value lets the
	// author fall back to its configured default worker provider.
	Provider ModelProvider `json:"-"`
	// Tier is the subtask's tier: SubtaskTierDesign is written by the
	// design model when one is configured.
	Tier  string `json:"-"`
	Title string `json:"title,omitempty"`
	// Brief is the whole job a crew of writers is building at once;
	// Instructions are this writer's part of it.
	Brief           string         `json:"brief,omitempty"`
	Instructions    string         `json:"instructions"`
	Attempt         int            `json:"attempt"`
	PreviousFailure string         `json:"previous_failure,omitempty"`
	RepositoryFiles []string       `json:"repository_files"`
	Files           []FileSnapshot `json:"files,omitempty"`
	// NotShown names files the writer asked for that were not sent: too
	// large, or past what one reply may be sent.
	NotShown []string `json:"-"`
	// Skills is the guidance the plan chose for this subtask.
	Skills []SkillGuidance `json:"skills,omitempty"`
	// Turn counts the writer's replies within one attempt, from 1: a subtask
	// is written over several short replies rather than one long one.
	Turn int `json:"turn,omitempty"`
	// Progress says what the earlier replies of this attempt already did.
	Progress []string `json:"progress,omitempty"`
	// Review is set once the writer has said it is done: it then looks at its
	// own work as a demanding reviewer and fixes what falls short.
	Review bool `json:"review,omitempty"`
	// Looks are screenshots of the pages, sent to the model as pictures.
	Looks []VisualShot `json:"-"`
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

// FileEdit changes one file. With Replace it swaps exact passages of the
// existing file, in order, and leaves the rest as it is; otherwise it writes
// Content as the whole file, or removes the file when Delete is true.
type FileEdit struct {
	Path    string            `json:"path"`
	Content string            `json:"content"`
	Delete  bool              `json:"delete,omitempty"`
	Replace []TextReplacement `json:"replace,omitempty"`
}

// TextReplacement swaps one passage of a file, which must occur in it
// exactly once, for New.
type TextReplacement struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// EditProposal is the coding model's complete answer for one attempt.
type EditProposal struct {
	Summary string     `json:"summary"`
	Edits   []FileEdit `json:"edits"`
	// More is true when the writer has more of the subtask to write: it is
	// called again with the files as they are now.
	More bool `json:"more,omitempty"`
	// Read names files the writer must see before it can write; it is then
	// called again with them.
	Read []string `json:"read,omitempty"`
	// CutOff marks a reply that ran past the output limit: only the edits
	// that were complete in it are here, and the writer must continue.
	CutOff bool `json:"-"`
}

// CodeAuthor asks a model to write code for a delegated subtask. It only
// returns proposals; validating and applying them is the caller's job, inside
// a verified worktree.
type CodeAuthor interface {
	SelectFiles(ctx context.Context, request CodeAuthorRequest) (FileSelection, error)
	ProposeEdits(ctx context.Context, request CodeAuthorRequest) (EditProposal, error)
}

// ErrReplyCutOff means a model reached its output limit before it finished
// its reply: the reply is incomplete and cannot be used. A writer asked to
// write less in one reply can carry on.
var ErrReplyCutOff = errors.New("the reply was cut off at the output limit before it was complete; write less in one reply")

// ErrReplyNotJSON means the writer answered in prose, or in JSON that could
// not be read, instead of the JSON object it was asked for.
var ErrReplyNotJSON = errors.New("the reply was not the JSON object asked for")
