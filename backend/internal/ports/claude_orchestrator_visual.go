package ports

import "context"

// VisualShot is one rendered page at one screen size: the picture the critic
// looks at and what the measurements found there.
type VisualShot struct {
	Page     string
	Viewport string
	Width    int
	Height   int
	JPEG     []byte
	Findings []string
	// Tiles is the picture cut top to bottom into pieces a model can read at
	// full detail. Empty means JPEG is read whole.
	Tiles [][]byte
	// File is where the picture was kept for people to look at, when it was.
	File string
}

// VisualCapturer photographs the pages of a worktree. A worktree without web
// pages yields no shots and no error.
type VisualCapturer interface {
	Capture(ctx context.Context, runID string, round int, worktree string) ([]VisualShot, error)
}

// VisualCritiqueRequest asks the critic to judge a run's pages.
type VisualCritiqueRequest struct {
	Provider ModelProvider
	Task     string
	// Brief is what the plan asked the pages to be: the instructions of the
	// subtasks that wrote them.
	Brief string
	Shots []VisualShot
	Round int
	// PreviousFixes are what the last round asked for, so the critic can
	// check they landed.
	PreviousFixes []string
}

// VisualCritique is the critic's judgement: an overall score out of ten, a
// score per criterion, a one-paragraph summary and the concrete fixes that
// would raise the score most, most important first.
type VisualCritique struct {
	Score   float64            `json:"score"`
	Scores  map[string]float64 `json:"scores,omitempty"`
	Summary string             `json:"summary"`
	Fixes   []string           `json:"fixes"`
}

// VisualCritic judges screenshots with a model that can see.
type VisualCritic interface {
	Critique(ctx context.Context, request VisualCritiqueRequest) (VisualCritique, error)
}

// VisualRound is one look at a run's pages: what the critic said and the
// pictures it saw. Polish names the subtask that acted on the fixes, if any.
type VisualRound struct {
	Round    int                `json:"round"`
	Score    float64            `json:"score"`
	Scores   map[string]float64 `json:"scores,omitempty"`
	Summary  string             `json:"summary"`
	Fixes    []string           `json:"fixes,omitempty"`
	Shots    []VisualShotRecord `json:"shots,omitempty"`
	Polish   string             `json:"polish,omitempty"`
	Error    string             `json:"error,omitempty"`
	Findings int                `json:"findings,omitempty"`
}

// VisualShotRecord is a kept picture: the page, the screen size, the file
// name under the run's visual folder, and the measurements.
type VisualShotRecord struct {
	Page     string   `json:"page"`
	Viewport string   `json:"viewport"`
	Name     string   `json:"name,omitempty"`
	Findings []string `json:"findings,omitempty"`
	// File is where the picture is on disk. It never leaves the daemon.
	File string `json:"-"`
}

// OrchestrationVisualReporter reports a service's visual review: whether it
// is on (and why not, when it was asked for but cannot run), each run's
// rounds, and the kept pictures by name.
type OrchestrationVisualReporter interface {
	VisualReviewStatus() (enabled bool, reason string)
	VisualFor(runID string) []VisualRound
	VisualShotFile(runID, name string) (string, bool)
}

// Reasons the visual review is off although it was asked for.
const (
	VisualReasonNoBrowser = "no_browser"
	VisualReasonNoClaude  = "no_claude"
)
