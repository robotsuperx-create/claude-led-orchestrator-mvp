package sandboxrunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	ErrDockerOptInRequired = errors.New("Docker executor requires explicit opt-in")
	ErrInvalidDockerConfig = errors.New("invalid Docker executor configuration")
	ErrDockerImageDenied   = errors.New("sandbox image is not allowlisted")
)

// DockerConfig enables the Docker executor only when Enabled is explicitly
// true. AllowedImages are exact image references that must already be present
// locally; the executor never pulls an image.
type DockerConfig struct {
	Enabled       bool
	AllowedImages []string
}

// DockerExecutor executes a hardened Docker CLI argv. It is never constructed
// implicitly by Service.New; callers must explicitly opt in and provide an
// image allowlist.
type DockerExecutor struct {
	allowedImages map[string]struct{}
}

// NewDockerExecutor rejects implicit/empty configuration and invalid image
// references. The accepted allowlist is copied so caller mutations cannot
// broaden the executor's image policy after construction.
func NewDockerExecutor(config DockerConfig) (*DockerExecutor, error) {
	if !config.Enabled {
		return nil, ErrDockerOptInRequired
	}
	if len(config.AllowedImages) == 0 {
		return nil, fmt.Errorf("%w: at least one image must be allowlisted", ErrInvalidDockerConfig)
	}
	allowed := make(map[string]struct{}, len(config.AllowedImages))
	for _, image := range config.AllowedImages {
		if !validImageReference(image) {
			return nil, fmt.Errorf("%w: malformed image reference", ErrInvalidDockerConfig)
		}
		if _, duplicate := allowed[image]; duplicate {
			return nil, fmt.Errorf("%w: duplicate image reference %q", ErrInvalidDockerConfig, image)
		}
		allowed[image] = struct{}{}
	}
	return &DockerExecutor{allowedImages: allowed}, nil
}

// BuildCommand returns the exact argv DockerExecutor would execute without
// launching Docker. It is intended for review and deterministic tests.
func (e *DockerExecutor) BuildCommand(request ports.SandboxRunRequest) ([]string, error) {
	if e == nil || len(e.allowedImages) == 0 {
		return nil, ErrUnavailable
	}
	if _, ok := e.allowedImages[request.RootFS]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrDockerImageDenied, request.RootFS)
	}
	return BuildDockerCommand(request)
}

// Run executes Docker directly with exec.CommandContext; it never interpolates
// request data into a shell command. The Docker client receives a clean
// process environment (rather than inheriting DOCKER_HOST, contexts, proxies,
// credentials, or other ambient host settings).
func (e *DockerExecutor) Run(ctx context.Context, request ports.SandboxRunRequest) (ports.SandboxRunResult, error) {
	if e == nil || len(e.allowedImages) == 0 {
		return ports.SandboxRunResult{}, ErrUnavailable
	}
	if ctx == nil {
		return ports.SandboxRunResult{}, invalid("context is required")
	}
	args, err := e.BuildCommand(request)
	if err != nil {
		return ports.SandboxRunResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()

	configDir, err := os.MkdirTemp("", "sandboxrunner-docker-config-")
	if err != nil {
		return ports.SandboxRunResult{}, fmt.Errorf("create isolated Docker client config: %w", err)
	}
	defer os.RemoveAll(configDir)

	cmd := exec.CommandContext(runCtx, args[0], args[1:]...)
	cmd.Dir = configDir
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=" + configDir,
		"DOCKER_CONFIG=" + configDir,
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	result := ports.SandboxRunResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if runErr == nil {
		return result, nil
	}
	if runCtx.Err() != nil {
		return result, runCtx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, fmt.Errorf("run Docker executor: %w", runErr)
}

func validImageReference(image string) bool {
	if image == "" || strings.TrimSpace(image) != image || strings.HasPrefix(image, "-") {
		return false
	}
	for i, r := range image {
		if r > 127 {
			return false
		}
		if i == 0 && !isASCIIAlphaNumeric(byte(r)) {
			return false
		}
		if !isASCIIAlphaNumeric(byte(r)) && !strings.ContainsRune("._:/@+-", r) {
			return false
		}
	}
	return true
}

func isASCIIAlphaNumeric(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

var _ Executor = (*DockerExecutor)(nil)
