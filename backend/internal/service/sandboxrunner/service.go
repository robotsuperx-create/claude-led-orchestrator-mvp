// Package sandboxrunner validates sandbox requests, delegates through an
// injected Executor, and provides an opt-in Docker-backed executor.
package sandboxrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	workspaceMountPath = "/workspace"
	maxMemoryBytes     = int64(64 << 30)
	maxNanoCPUs        = int64(64_000_000_000)
	maxPIDs            = int64(65_536)
	maxTimeout         = 24 * time.Hour
)

var (
	ErrInvalidRequest = errors.New("invalid sandbox request")
	ErrUnavailable    = errors.New("sandbox runner is unavailable")
)

// Service applies the request timeout and delegates execution to an injected
// runtime. It does not provide a local or Docker execution implementation.
type Service struct {
	executor Executor
}

// Executor runs a single validated sandbox request. Executors must enforce
// the request's isolation and resource constraints and honor cancellation.
type Executor interface {
	Run(ctx context.Context, request ports.SandboxRunRequest) (ports.SandboxRunResult, error)
}

// New constructs the service around an explicitly supplied executor. No
// executor, including DockerExecutor, is selected implicitly.
func New(executor Executor) *Service {
	return &Service{executor: executor}
}

// Run validates the request, constrains its context to the requested timeout,
// and delegates to the runtime. The runtime must enforce the deadline as well.
func (s *Service) Run(ctx context.Context, request ports.SandboxRunRequest) (ports.SandboxRunResult, error) {
	if s == nil || s.executor == nil {
		return ports.SandboxRunResult{}, ErrUnavailable
	}
	if ctx == nil {
		return ports.SandboxRunResult{}, invalid("context is required")
	}
	if err := ValidateRequest(request); err != nil {
		return ports.SandboxRunResult{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	return s.executor.Run(runCtx, request)
}

// BuildDockerCommand returns a safe-by-construction Docker CLI argv for
// inspection or handoff to a separately implemented runtime. It never starts
// Docker, invokes a shell, or executes any part of the returned command.
func BuildDockerCommand(request ports.SandboxRunRequest) ([]string, error) {
	if err := ValidateRequest(request); err != nil {
		return nil, err
	}
	projectRoot, err := canonicalProjectRoot(request.ProjectRoot)
	if err != nil {
		return nil, invalid("project root: %v", err)
	}

	args := []string{
		"docker", "run", "--rm",
		"--pull=never",
		"--network=none",
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges:true",
		"--memory", strconv.FormatInt(request.ResourceLimits.MemoryBytes, 10),
		"--cpus", strconv.FormatFloat(float64(request.ResourceLimits.NanoCPUs)/1e9, 'f', 9, 64),
		"--pids-limit", strconv.FormatInt(request.ResourceLimits.PIDs, 10),
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=64m",
		"--mount", "type=bind,src=" + projectRoot + ",dst=" + workspaceMountPath,
		"--workdir", request.WorkDir,
	}
	for _, name := range sortedEnvironmentNames(request.Env) {
		args = append(args, "--env", name+"="+request.Env[name])
	}
	args = append(args, "--entrypoint", request.Argv[0])
	args = append(args, request.RootFS)
	args = append(args, request.Argv[1:]...)
	return args, nil
}

// ValidateRequest enforces the builder and service's shared fail-closed
// contract. It never reads process environment variables or runs a command.
func ValidateRequest(request ports.SandboxRunRequest) error {
	if strings.TrimSpace(request.RootFS) == "" || strings.HasPrefix(request.RootFS, "-") || hasControlOrWhitespace(request.RootFS) {
		return invalid("rootfs image reference is empty or malformed")
	}
	if _, err := canonicalProjectRoot(request.ProjectRoot); err != nil {
		return invalid("project root: %v", err)
	}
	if err := validateWorkDir(request.WorkDir); err != nil {
		return err
	}
	if !request.NoNetwork {
		return invalid("network access must be disabled")
	}
	limits := request.ResourceLimits
	if limits.MemoryBytes <= 0 || limits.MemoryBytes > maxMemoryBytes {
		return invalid("memory limit must be in (0, %d] bytes", maxMemoryBytes)
	}
	if limits.NanoCPUs <= 0 || limits.NanoCPUs > maxNanoCPUs {
		return invalid("CPU limit must be in (0, %d] NanoCPUs", maxNanoCPUs)
	}
	if limits.PIDs <= 0 || limits.PIDs > maxPIDs {
		return invalid("PID limit must be in (0, %d]", maxPIDs)
	}
	if request.Timeout <= 0 || request.Timeout > maxTimeout {
		return invalid("timeout must be positive and no greater than %s", maxTimeout)
	}
	if len(request.Argv) == 0 || strings.TrimSpace(request.Argv[0]) == "" {
		return invalid("argv must contain an executable")
	}
	for i, arg := range request.Argv {
		if strings.ContainsRune(arg, '\x00') {
			return invalid("argv[%d] contains a NUL byte", i)
		}
	}
	if isShellExecutable(request.Argv[0]) {
		return invalid("shell executables are not accepted; supply argv for a direct executable")
	}
	if isEnvShellInvocation(request.Argv) {
		return invalid("env may not be used to launch a shell")
	}
	if err := validateEnvironment(request.EnvAllowlist, request.Env); err != nil {
		return err
	}
	return nil
}

func validateEnvironment(allowlist []string, env map[string]string) error {
	allowed := make(map[string]struct{}, len(allowlist))
	for _, name := range allowlist {
		if !validEnvironmentName(name) {
			return invalid("environment allowlist contains an invalid name")
		}
		if _, duplicate := allowed[name]; duplicate {
			return invalid("environment allowlist contains duplicate name %q", name)
		}
		allowed[name] = struct{}{}
	}
	for name, value := range env {
		if _, ok := allowed[name]; !ok {
			return invalid("environment variable %q is not allowlisted", name)
		}
		if strings.ContainsRune(value, '\x00') {
			return invalid("environment variable %q contains a NUL byte", name)
		}
	}
	return nil
}

func validateWorkDir(workDir string) error {
	if !path.IsAbs(workDir) || strings.ContainsRune(workDir, '\x00') || path.Clean(workDir) != workDir {
		return invalid("workdir must be a clean absolute container path")
	}
	if workDir != workspaceMountPath && !strings.HasPrefix(workDir, workspaceMountPath+"/") {
		return invalid("workdir must be inside %s", workspaceMountPath)
	}
	return nil
}

func canonicalProjectRoot(projectRoot string) (string, error) {
	if strings.TrimSpace(projectRoot) == "" || strings.ContainsRune(projectRoot, '\x00') || strings.Contains(projectRoot, ",") {
		return "", errors.New("path is empty or contains unsupported characters")
	}
	if !filepath.IsAbs(projectRoot) {
		return "", errors.New("path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(projectRoot))
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("make path absolute: %w", err)
	}
	resolved = filepath.Clean(resolved)
	volumeRoot := filepath.VolumeName(resolved) + string(os.PathSeparator)
	if resolved == volumeRoot {
		return "", errors.New("filesystem root cannot be exposed as a project")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("path is not a directory")
	}
	return resolved, nil
}

func validEnvironmentName(name string) bool {
	if name == "" || !((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z') || name[0] == '_') {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

func isShellExecutable(executable string) bool {
	base := path.Base(strings.ReplaceAll(executable, `\`, "/"))
	switch strings.ToLower(base) {
	case "sh", "bash", "dash", "zsh", "fish", "ksh", "mksh", "ash", "csh", "tcsh", "pwsh", "powershell", "busybox":
		return true
	default:
		return false
	}
}

func isEnvShellInvocation(argv []string) bool {
	if path.Base(strings.ReplaceAll(argv[0], `\`, "/")) != "env" {
		return false
	}
	for _, arg := range argv[1:] {
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
			continue
		}
		return isShellExecutable(arg)
	}
	return false
}

func sortedEnvironmentNames(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func hasControlOrWhitespace(value string) bool {
	for _, r := range value {
		if r <= 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, fmt.Sprintf(format, args...))
}
