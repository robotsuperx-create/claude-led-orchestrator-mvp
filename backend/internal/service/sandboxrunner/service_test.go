package sandboxrunner

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func validRequest(t *testing.T) ports.SandboxRunRequest {
	t.Helper()
	root := t.TempDir()
	return ports.SandboxRunRequest{
		RootFS:      "example.invalid/runner:v1",
		ProjectRoot: root,
		WorkDir:     "/workspace",
		NoNetwork:   true,
		ResourceLimits: ports.SandboxResourceLimits{
			MemoryBytes: 512 << 20,
			NanoCPUs:    2_000_000_000,
			PIDs:        128,
		},
		Timeout:      30 * time.Second,
		EnvAllowlist: []string{"CI", "LANG"},
		Env:          map[string]string{"CI": "true", "LANG": "C.UTF-8"},
		Argv:         []string{"/usr/bin/go", "test", "./..."},
	}
}

func TestBuildDockerCommandIsARestrictedArgvPlan(t *testing.T) {
	request := validRequest(t)
	request.Argv = []string{"/usr/bin/printf", "%s", "$(touch /tmp/not-run); *"}
	args, err := BuildDockerCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	for _, forbidden := range []string{"--privileged", "/var/run/docker.sock", "src=/,dst=", "--network=host", "sh -c"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("command includes forbidden setting %q: %q", forbidden, args)
		}
	}
	for _, required := range []string{"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--memory", "--cpus", "--pids-limit", "type=bind,src=" + request.ProjectRoot + ",dst=/workspace", "--workdir", "example.invalid/runner:v1"} {
		if !strings.Contains(joined, required) {
			t.Errorf("command missing %q: %q", required, args)
		}
	}
	entrypoint := indexOf(args, "--entrypoint")
	if entrypoint < 0 || entrypoint+1 >= len(args) || args[entrypoint+1] != request.Argv[0] {
		t.Fatalf("direct executable was not set as entrypoint: %q", args)
	}
	image := indexOf(args, request.RootFS)
	if image < 0 || !reflect.DeepEqual(args[image+1:], request.Argv[1:]) {
		t.Fatalf("argv was not passed literally: got tail %q, want %q", args[image+1:], request.Argv[1:])
	}
	if args[len(args)-1] != "$(touch /tmp/not-run); *" {
		t.Fatalf("shell metacharacters were not preserved as a literal argument: %q", args)
	}
}

func TestNewDockerExecutorRequiresExplicitOptInAndValidImageAllowlist(t *testing.T) {
	cases := []struct {
		name   string
		config DockerConfig
		want   error
	}{
		{name: "opt-in required", config: DockerConfig{AllowedImages: []string{"example.invalid/runner:v1"}}, want: ErrDockerOptInRequired},
		{name: "allowlist required", config: DockerConfig{Enabled: true}, want: ErrInvalidDockerConfig},
		{name: "malformed image", config: DockerConfig{Enabled: true, AllowedImages: []string{"--privileged"}}, want: ErrInvalidDockerConfig},
		{name: "duplicate image", config: DockerConfig{Enabled: true, AllowedImages: []string{"example.invalid/runner:v1", "example.invalid/runner:v1"}}, want: ErrInvalidDockerConfig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if executor, err := NewDockerExecutor(tc.config); !errors.Is(err, tc.want) || executor != nil {
				t.Fatalf("NewDockerExecutor() = (%v, %v), want nil and %v", executor, err, tc.want)
			}
		})
	}
}

func TestDockerExecutorBuildCommandEnforcesAllowlistAndPolicyArgv(t *testing.T) {
	image := "example.invalid/runner:v1"
	executor, err := NewDockerExecutor(DockerConfig{Enabled: true, AllowedImages: []string{image}})
	if err != nil {
		t.Fatal(err)
	}
	request := validRequest(t)
	args, err := executor.BuildCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	if args[0] != "docker" || args[1] != "run" || args[2] != "--rm" {
		t.Fatalf("unexpected Docker invocation prefix: %q", args[:3])
	}
	joined := strings.Join(args, "\n")
	for _, required := range []string{"--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--memory", "--cpus", "--pids-limit", "type=bind,src=" + request.ProjectRoot + ",dst=/workspace", "--workdir", "--tmpfs"} {
		if !strings.Contains(joined, required) {
			t.Errorf("Docker argv missing required policy %q: %q", required, args)
		}
	}
	if indexOf(args, "--privileged") >= 0 || indexOf(args, "--network=host") >= 0 || indexOf(args, "sh") >= 0 {
		t.Fatalf("Docker argv contains a forbidden option or shell executable: %q", args)
	}
	imageIndex := indexOf(args, image)
	if imageIndex < 0 || !reflect.DeepEqual(args[imageIndex+1:], request.Argv[1:]) {
		t.Fatalf("command was not passed as literal argv after the allowlisted image: %q", args)
	}

	request.RootFS = "example.invalid/unapproved:v1"
	if _, err := executor.BuildCommand(request); !errors.Is(err, ErrDockerImageDenied) {
		t.Fatalf("BuildCommand(unapproved image) error = %v, want ErrDockerImageDenied", err)
	}
}

func TestDockerExecutorBuildCommandRejectsUnsafeRequestConfiguration(t *testing.T) {
	executor, err := NewDockerExecutor(DockerConfig{Enabled: true, AllowedImages: []string{"example.invalid/runner:v1"}})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*ports.SandboxRunRequest){
		"network enabled":        func(r *ports.SandboxRunRequest) { r.NoNetwork = false },
		"unapproved environment": func(r *ports.SandboxRunRequest) { r.Env["SECRET"] = "value" },
		"shell invocation":       func(r *ports.SandboxRunRequest) { r.Argv = []string{"/bin/sh", "-c", "echo unsafe"} },
		"unscoped workdir":       func(r *ports.SandboxRunRequest) { r.WorkDir = "/tmp" },
		"missing resource limit": func(r *ports.SandboxRunRequest) { r.ResourceLimits.PIDs = 0 },
		"missing timeout":        func(r *ports.SandboxRunRequest) { r.Timeout = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := validRequest(t)
			mutate(&request)
			if _, err := executor.BuildCommand(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("BuildCommand() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestBuildDockerCommandSortsAndOnlyPassesAllowlistedEnvironment(t *testing.T) {
	request := validRequest(t)
	request.EnvAllowlist = []string{"ZED", "ALPHA"}
	request.Env = map[string]string{"ZED": "two", "ALPHA": "one"}
	args, err := BuildDockerCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	alpha := indexOf(args, "ALPHA=one")
	zed := indexOf(args, "ZED=two")
	if alpha < 0 || zed < 0 || alpha >= zed {
		t.Fatalf("environment arguments are not deterministic: %q", args)
	}
}

func TestBuildDockerCommandRejectsRootProjectExposure(t *testing.T) {
	request := validRequest(t)
	request.ProjectRoot = string(filepath.Separator)
	if _, err := BuildDockerCommand(request); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("BuildDockerCommand(root) error = %v, want ErrInvalidRequest", err)
	}
}

func TestValidateRequestRejectsUnsafeConfiguration(t *testing.T) {
	cases := map[string]func(*ports.SandboxRunRequest){
		"network enabled":         func(r *ports.SandboxRunRequest) { r.NoNetwork = false },
		"environment not allowed": func(r *ports.SandboxRunRequest) { r.Env["SECRET"] = "value" },
		"malformed environment":   func(r *ports.SandboxRunRequest) { r.EnvAllowlist = []string{"BAD=NAME"} },
		"relative workdir":        func(r *ports.SandboxRunRequest) { r.WorkDir = "workspace" },
		"workdir outside project": func(r *ports.SandboxRunRequest) { r.WorkDir = "/etc" },
		"shell":                   func(r *ports.SandboxRunRequest) { r.Argv = []string{"/bin/bash", "-c", "echo unsafe"} },
		"env shell":               func(r *ports.SandboxRunRequest) { r.Argv = []string{"/usr/bin/env", "sh", "-c", "echo unsafe"} },
		"bad timeout":             func(r *ports.SandboxRunRequest) { r.Timeout = 0 },
		"no memory limit":         func(r *ports.SandboxRunRequest) { r.ResourceLimits.MemoryBytes = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := validRequest(t)
			mutate(&request)
			if err := ValidateRequest(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("ValidateRequest() error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestBuildDockerCommandHasNoConfigurableHostMountOrPrivilegedFlag(t *testing.T) {
	request := validRequest(t)
	args, err := BuildDockerCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	mountArgs := 0
	for i, arg := range args {
		if arg == "--mount" {
			mountArgs++
			if i+1 >= len(args) || args[i+1] != "type=bind,src="+request.ProjectRoot+",dst=/workspace" {
				t.Fatalf("unexpected mount configuration: %q", args)
			}
		}
		if strings.Contains(arg, "privileged") || strings.Contains(arg, "/etc:") || strings.Contains(arg, "/var/run") || strings.Contains(arg, "src=/,") {
			t.Fatalf("unsafe host mount/privilege configuration: %q", args)
		}
	}
	if mountArgs != 1 {
		t.Fatalf("mount count = %d, want exactly one isolated project worktree bind", mountArgs)
	}
}

func TestServiceAppliesRequestedTimeoutBeforeDelegating(t *testing.T) {
	request := validRequest(t)
	request.Timeout = time.Millisecond
	runner := &fakeRunner{run: func(ctx context.Context, _ ports.SandboxRunRequest) (ports.SandboxRunResult, error) {
		<-ctx.Done()
		return ports.SandboxRunResult{}, ctx.Err()
	}}
	_, err := New(runner).Run(context.Background(), request)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context deadline exceeded", err)
	}
	if !runner.called {
		t.Fatal("runner was not called")
	}
}

type fakeRunner struct {
	called bool
	run    func(context.Context, ports.SandboxRunRequest) (ports.SandboxRunResult, error)
}

func (f *fakeRunner) Run(ctx context.Context, request ports.SandboxRunRequest) (ports.SandboxRunResult, error) {
	f.called = true
	if f.run != nil {
		return f.run(ctx, request)
	}
	return ports.SandboxRunResult{}, nil
}

func indexOf(values []string, value string) int {
	for i, candidate := range values {
		if candidate == value {
			return i
		}
	}
	return -1
}
