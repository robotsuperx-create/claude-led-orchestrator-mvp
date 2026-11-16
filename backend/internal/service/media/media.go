// Package media is X-ROBOT-AI's media gateway. It asks the person's own GPU
// server (ComfyUI behind their token, rented by the hour for their use only)
// for pictures and videos, and keeps what comes back in a library under the
// data dir. Each kind of work is a workflow: two come built in (a picture and
// a short video), and any workflow the person exports from ComfyUI and drops
// into the library's workflows folder becomes a new capability, with no
// change to the app. X-00's voice goes through the same server, but never
// through here: it is live and never kept.
package media

import (
	"errors"
	"time"
)

// Kinds of media a capability makes.
const (
	KindImage = "image"
	KindVideo = "video"
	// KindAudio is music and sound effects (never X-00's voice, which is
	// live and never kept).
	KindAudio = "audio"
)

// Item states.
const (
	StateQueued  = "queued"
	StateRunning = "running"
	StateDone    = "done"
	StateFailed  = "failed"
)

// Why an item failed: codes the app turns into words. They never carry the
// server's own text, which could echo the token.
const (
	FailUnreachable = "unreachable"
	FailRejected    = "rejected"
	FailFailed      = "failed"
	FailTimeout     = "timeout"
	FailNoOutput    = "no_output"
	FailTooLarge    = "too_large"
	FailInterrupted = "interrupted"
)

// Where a request came from.
const (
	SourceApp = "app"
	SourceX00 = "x00"
	// SourceUpload is a picture the person added from their computer.
	SourceUpload = "upload"
)

// Limits on what is asked for and kept.
const (
	MaxPromptRunes   = 2000
	MaxNegativeRunes = 1000
	MaxListed        = 200
	MaxCapabilities  = 64
	maxFilesPerItem  = 8
	maxQueued        = 16
	maxRunning       = 2
	maxImageBytes    = 40 << 20
	maxVideoBytes    = 512 << 20
	maxAudioBytes    = 64 << 20
	maxWorkflowBytes = 1 << 20
	// MaxUploadBytes bounds a picture the person adds to the library.
	MaxUploadBytes = 20 << 20
	imageTimeout   = 10 * time.Minute
	videoTimeout   = 45 * time.Minute
)

// Sizes a request may ask for.
const (
	MinSide         = 256
	MaxImageSide    = 2048
	MaxVideoSide    = 1280
	MaxVideoSeconds = 10
	MaxAudioSeconds = 47
	videoFPS        = 16
)

var (
	// ErrNotConfigured means no GPU server is set, or its settings are invalid.
	ErrNotConfigured = errors.New("media: no GPU server is set")
	// ErrInvalidRequest means the request is malformed or out of bounds.
	ErrInvalidRequest = errors.New("media: invalid request")
	// ErrUnknownCapability means no workflow has that name.
	ErrUnknownCapability = errors.New("media: unknown capability")
	// ErrBusy means the queue is full.
	ErrBusy = errors.New("media: the queue is full")
	// ErrNotFound means no item has that id.
	ErrNotFound = errors.New("media: no such item")
)

// File is one picture or video an item holds, named inside the item's folder.
type File struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Bytes int64  `json:"bytes"`
}

// Item is one request and what came of it.
type Item struct {
	ID         string `json:"id"`
	Capability string `json:"capability"`
	Kind       string `json:"kind"`
	Prompt     string `json:"prompt"`
	Negative   string `json:"negative,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Seconds    int    `json:"seconds,omitempty"`
	Seed       int64  `json:"seed"`
	// Input is the library item whose picture the capability worked on.
	Input     string    `json:"input,omitempty"`
	Source    string    `json:"source"`
	State     string    `json:"state"`
	Failure   string    `json:"failure,omitempty"`
	Files     []File    `json:"files"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Request asks for one item. Zero sizes take the capability's defaults; a
// zero seed is chosen at random.
type Request struct {
	Capability string `json:"capability"`
	Prompt     string `json:"prompt"`
	Negative   string `json:"negative,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	Seconds    int    `json:"seconds,omitempty"`
	Seed       int64  `json:"seed,omitempty"`
	// Input names a library item holding a picture, for capabilities that
	// work on one.
	Input  string `json:"input,omitempty"`
	Source string `json:"source,omitempty"`
}

// Capability is one workflow the gateway can run.
type Capability struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Title   string `json:"title,omitempty"`
	BuiltIn bool   `json:"builtIn"`
	// Input is "image" when the capability works on a picture the person
	// picks from the library (upscaling, a new background, animating it).
	Input string `json:"input,omitempty"`
}

// Info says whether the gateway can make anything and what. Root is the
// library folder: the desktop app's main process serves files from it and
// never passes it on to the page.
type Info struct {
	Enabled      bool         `json:"enabled"`
	Reason       string       `json:"reason,omitempty"`
	Voice        bool         `json:"voice"`
	Capabilities []Capability `json:"capabilities"`
	Root         string       `json:"root"`
}

// ServerStatus is what the GPU server says about itself.
type ServerStatus struct {
	Reachable bool   `json:"reachable"`
	Device    string `json:"device,omitempty"`
	VRAMBytes int64  `json:"vramBytes,omitempty"`
	Queue     int    `json:"queue,omitempty"`
}
