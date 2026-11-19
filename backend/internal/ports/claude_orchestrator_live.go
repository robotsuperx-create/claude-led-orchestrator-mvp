package ports

import "context"

// LiveStepState is how far one step of a run's plan has gone.
type LiveStepState string

// The states a plan step passes through while a run works.
const (
	LiveStepWaiting LiveStepState = "waiting"
	LiveStepRunning LiveStepState = "running"
	LiveStepDone    LiveStepState = "done"
	LiveStepFailed  LiveStepState = "failed"
	LiveStepBlocked LiveStepState = "blocked"
)

// LiveStep is one plan step as the run's crew carries it: which step, how it
// is going, and on which attempt. Index is the step's place in the plan; the
// crew member who carries it follows from that place.
type LiveStep struct {
	Index   int
	ID      string
	Title   string
	State   LiveStepState
	Attempt int
	// Failure is why the step's last attempt failed, bounded and redacted,
	// so the person can see what went wrong while the crew retries.
	Failure string
}

// LiveFileChange is what the run has written to one file so far: how many
// lines it added and removed, and the last lines it added. Lines are the
// run's own code in its own worktree; they are bounded and secret-redacted
// before anyone sees them.
type LiveFileChange struct {
	Path    string
	Added   int
	Removed int
	// New is true when the run created the file.
	New   bool
	Lines []string
}

// LiveWork is what a run is doing right now: its steps and the code it has
// written, newest file first.
type LiveWork struct {
	Steps []LiveStep
	Files []LiveFileChange
}

// OrchestrationLiveReporter reports a run's live work while it runs and after.
type OrchestrationLiveReporter interface {
	LiveFor(ctx context.Context, runID string) (LiveWork, bool)
}

// RunWorkspaceChanges reads what a run has written in its worktree so far,
// without touching the worktree, its index, or its branch.
type RunWorkspaceChanges interface {
	Changes(ctx context.Context, workspace RunWorkspace) ([]LiveFileChange, error)
}

// RunSite is a run's web pages: the worktree they live in and the pages found
// there, as paths relative to it. Root never leaves the daemon's loopback
// control routes for the renderer; only the main process reads it.
type RunSite struct {
	Root  string
	Pages []string
}

// OrchestrationSiteReporter reports where a run's pages are, for a live preview.
type OrchestrationSiteReporter interface {
	SiteFor(runID string) (RunSite, bool)
}
