package ports

import (
	"context"
	"time"
)

// SandboxResourceLimits are hard resource ceilings requested for one run.
// NanoCPUs uses Docker's CPU units (1e9 is one CPU).
type SandboxResourceLimits struct {
	MemoryBytes int64
	NanoCPUs    int64
	PIDs        int64
}

// SandboxRunRequest is the complete, explicit execution boundary. RootFS is
// the immutable runtime image reference; ProjectRoot is the sole host
// workspace that may be made available inside the sandbox at /workspace.
// WorkDir is an absolute path inside that image and must be /workspace or a
// descendant. No ambient host environment or network access is permitted.
type SandboxRunRequest struct {
	RootFS         string
	ProjectRoot    string
	WorkDir        string
	NoNetwork      bool
	ResourceLimits SandboxResourceLimits
	Timeout        time.Duration
	EnvAllowlist   []string
	Env            map[string]string
	Argv           []string
}

// SandboxRunResult contains the process exit status and captured output.
type SandboxRunResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// SandboxRunner is the execution port. Implementations must enforce the
// request's filesystem, network, resource, environment, argv, and timeout
// constraints in an actual isolation runtime, and must honor context
// cancellation. A command builder alone does not implement this interface's
// security guarantees.
type SandboxRunner interface {
	Run(ctx context.Context, request SandboxRunRequest) (SandboxRunResult, error)
}
