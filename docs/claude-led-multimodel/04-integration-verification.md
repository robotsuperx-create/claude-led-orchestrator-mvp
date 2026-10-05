# Integration verification

> **Historical snapshot:** This document predates the conditional internal route, injected renderer hook contract, SCM HTTP adapter, WorkerRuntime, and HTTP smoke test. Its route/UI statements describe that earlier checkpoint, not the current tree. For the final operational state and exact verification scope, see [05-operational-verification.md](05-operational-verification.md).

## Integration decision

The new Claude-led multi-model pieces are present behind default-deny boundaries, but they are **not enabled as a user-facing product feature**. The daemon constructs a minimal orchestrator composition with `claudeorchestrator.New(Dependencies{})` and derives its run gate from `AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED`; the zero-value/default is `false`. The wiring test verifies that a run is rejected while disabled and that shutdown is safe. Since the dependency set is empty, this composition does not invoke a model provider, worker, validator, workspace operation, or Git operation. No current HTTP or renderer entry point calls it.

## What was added or connected

- **Daemon/config boundary:** added the environment-controlled, default-off feature flag and a small daemon-owned orchestrator wiring/cleanup boundary. The new service instance has no production adapters attached.
- **Typed integration contracts:** added SCM write/read contracts for branch creation, push, pull-request creation, and checks. External writes require explicit matching approval; the credential wrapper redacts formatting and serialization. These are interfaces/contracts, not a GitHub/GitLab implementation.
- **Run storage contract and test implementation:** added typed run/event/idempotency/fencing/cancel-state contracts and an in-memory store with tests for idempotency, compare-and-swap, event ordering, and a shared-backing-state restart simulation. The simulation is not process-durable storage; no SQLite implementation or migration was added.
- **Sandbox boundary:** added `SandboxRunner` request/port validation and `BuildDockerCommand`, which returns a constrained Docker argv plan. It does **not** start Docker or provide a sandbox; no production container/VM runtime is connected.
- **HTTP contract:** added JSON DTOs and serialization tests. No orchestrator route was registered, and no generated OpenAPI source/schema was changed.
- **UI presentation component:** added an allowlisted run view/model to `@aoagents/product-ui` and exported it from that package. It returns no UI unless a host explicitly passes `enabled={true}`. The renderer does not import or mount it, and it has no API client or persistence.

## Still unconnected / required before product use

- Live model/provider adapters and the provider-selection/execution path.
- Worker, validator, project-worktree, and SCM implementations wired into the orchestrator dependency graph.
- A real sandbox runtime that enforces isolation, resource limits, timeouts, cancellation, and bounded output.
- Durable database-backed run/event storage; the in-memory implementation does not survive a process restart.
- A registered, local-only run API with an actual status/cancel lifecycle, generated OpenAPI/client types, and a tested authorization/consent boundary. The new DTO file is not a route. No changes were made to the generated API schema.
- A renderer API client/React Query integration and a deliberately enabled UI mount. Do not attach the view to the existing session-orchestrator lifecycle, which is not a run-management API.

Accordingly, this work adds typed seams and a default-off composition boundary, not a runnable multi-model workflow. Keep the feature flag off until the missing adapters, persistence, sandbox runtime, route, and UI integration are implemented and reviewed.

## Verification performed

- `npm --prefix packages/claude-led-orchestrator run check` — passed: TypeScript typecheck, 28 tests, and package build.
- `/usr/local/go/bin/gofmt -w` — applied to all newly added Go source and test files.
- In `backend/`, `go test` passed for `internal/config`, `internal/ports`, `internal/service/claudeorchestrator`, `internal/service/worktree`, `internal/adapters/modelgateway`, `internal/httpd`, `internal/daemon`, `internal/service/sandboxrunner`, `internal/storage/memory`, and `internal/httpd/controllers`.
- In `backend/`, `go vet` passed for the same affected packages.
- `git diff --check` — passed. Router/reference inspection confirmed that the new DTO has no registration and the renderer has no `OrchestratorRunView` mount.

The verification above covers the requested package set and the newly added packages; it is not a claim that every package in the repository has been tested.
