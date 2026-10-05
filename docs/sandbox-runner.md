# SandboxRunner (injected executor and opt-in Docker runtime)

The backend sandbox boundary is defined in `internal/ports/sandbox_runner.go`; its request makes the rootfs image, host project worktree, in-container working directory, network policy, resource limits, timeout, environment allowlist, and direct argv explicit. `internal/service/sandboxrunner` validates the contract, applies a context deadline, and delegates to an injected `Executor`. No executor is selected implicitly. `NewDockerExecutor` is available only when configured with `Enabled: true` and a non-empty exact image allowlist; otherwise it rejects the configuration. Images must already exist locally (`--pull=never`).

## Docker executor policy

- Docker runs only when an explicitly created `DockerExecutor` is injected into `Service`; the command builder alone never launches it. `DockerExecutor.BuildCommand` is provided for review and tests without Docker.
- The executor checks the request image against its exact allowlist. It does not pull images and fixes `--network=none` (requests that enable networking are rejected).
- The container root filesystem is read-only, capabilities are dropped, `no-new-privileges` is set, `/tmp` is a private size-limited tmpfs, and memory, CPU, and PID limits are explicit.
- Only the selected project directory is bind-mounted at `/workspace`; filesystem root paths are rejected, with no configurable host mount list, host namespace, socket, or device option. The workdir must be `/workspace` or one of its descendants.
- The invocation uses an argv vector with `os/exec`, not a shell string. Known shell executables and `env`-launched known shells are rejected; other arguments remain literal argv elements.
- Container environment values are limited to names in the request allowlist. The Docker client process receives a clean, temporary configuration environment rather than inheriting ambient host Docker settings or credentials.

## Required security review and limitations

**A security review is required before enabling this executor for untrusted workloads.** Docker containers share the host kernel, and access to a Docker daemon is a highly privileged boundary; these flags alone do not establish a security boundary against kernel/runtime vulnerabilities or hostile container workloads. Review the daemon's isolation and access, image provenance, mount permissions, cancellation/container cleanup behavior, resource enforcement, and output handling. Consider a VM-backed runtime where stronger isolation is required. Do not treat unit tests of generated argv as proof of runtime isolation; tests intentionally do not launch Docker.

`Service.Run` applies a context deadline, and DockerExecutor uses a context-bound Docker CLI call, but timeout/cancellation and cleanup behavior must be validated against the deployed Docker Engine before relying on it. Captured output is currently buffered in memory without a size cap, so output exhaustion also requires review/mitigation for hostile workloads. The project-worktree bind mount intentionally exposes that directory (writable) to the container: use a dedicated worktree, never a broad checkout or sensitive host directory. Resource maxima and the accepted workdir/image formats are encoded in the service validator.
