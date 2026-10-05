package ports

import "context"

// WorktreeCreateRequest asks for a new linked worktree beneath ProjectRoot.
type WorktreeCreateRequest struct {
	ProjectRoot string
	Path        string
	Branch      string
}

// WorktreeCreateResult identifies the newly-created worktree.
type WorktreeCreateResult struct {
	ProjectRoot string
	Path        string
	Branch      string
}

// WorktreeRemoveRequest explicitly asks to remove one linked worktree.
// Removal is never performed implicitly by another WorktreeManager operation.
type WorktreeRemoveRequest struct {
	ProjectRoot string
	Path        string
	Force       bool
}

// WorktreeRemoveResult reports a successful explicit removal.
type WorktreeRemoveResult struct {
	Path    string
	Removed bool
}

// WorktreeStatusRequest asks for the working-tree status of a linked worktree.
type WorktreeStatusRequest struct {
	ProjectRoot string
	Path        string
}

// WorktreeStatusResult reports the selected worktree's branch and porcelain
// status. Output contains the raw `git status --porcelain=v1 --branch` output.
type WorktreeStatusResult struct {
	Path   string
	Branch string
	Clean  bool
	Output string
}

// WorktreeManager creates, explicitly removes, and inspects project worktrees.
type WorktreeManager interface {
	Create(ctx context.Context, request WorktreeCreateRequest) (WorktreeCreateResult, error)
	Remove(ctx context.Context, request WorktreeRemoveRequest) (WorktreeRemoveResult, error)
	Status(ctx context.Context, request WorktreeStatusRequest) (WorktreeStatusResult, error)
}
