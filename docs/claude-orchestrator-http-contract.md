# Claude orchestrator HTTP contract status

The router now supports an **experimental internal control API** at `/internal/claude-orchestrator/runs`, but mounts it only when `APIDeps.ClaudeOrchestrator` is supplied. The daemon does not currently supply that dependency: its Claude orchestrator has no model, worker, validator, or project-memory adapters, so production does not expose a nonfunctional start/status API.

This is deliberately **not an OpenAPI API**. The generated OpenAPI contract and its 1:1 parity test cover `/api/v1` routes; these process-local control routes stay outside that namespace. Do not add operations by editing generated `backend/internal/httpd/apispec/openapi.yaml`. The active internal wire contract and handlers are defined together in `backend/internal/httpd/claude_orchestrator_api.go`. The controller DTOs in `backend/internal/httpd/controllers/claude_orchestrator_contract.go` remain a separate proposal for a future public contract and do not describe these handlers.

## Preconditions and safeguards

- `AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED` is the daemon configuration flag; it defaults to false. The router constructs the run policy from that immutable config, and the handler denies starts while it is false.
- Every start also requires `explicitOptIn: true`. Both start and status use `localControlRequest`, which rejects requests bearing an `Origin` and requires a loopback `Host`. The daemon's ordinary HTTP listener binds loopback; the separate LAN listener blocks the entire `/internal/` path prefix before dispatch, regardless of a caller-supplied Host header.
- The request decoder caps bodies at 16 KiB, rejects unknown fields and trailing JSON values, caps task text at 8 KiB, and permits `maxRetries` only from 0 through 3.
- Responses expose only an opaque run ID and lifecycle state. They do not serialize task text, model/worker output, provider settings, credentials, or raw service errors. Internal gate and service failures use fixed public codes/messages.
- A nil service leaves the routes unregistered. The current daemon composition intentionally leaves it nil because orchestration adapters are not configured.

## Internal route contract

| Method and path | Behavior |
|---|---|
| `POST /internal/claude-orchestrator/runs` | Requires JSON `{ "task": string, "maxRetries"?: integer, "explicitOptIn": true }`; returns `202` with `{ "runId": string, "state": "pending" }`. The server creates the run ID. |
| `GET /internal/claude-orchestrator/runs/{runId}` | Returns `200` with only `{ "runId": string, "state": string }` while the run is present in this process, or a fixed `404` if not found. |

Run state is held in memory and is not durable across restarts. This API does not provide cancellation, persisted history, task/result retrieval, or merge execution. The merge recommendation itself remains advisory in the underlying service.

## Existing service and proposed public API

The service exposes synchronous `Run`/`RunGated` application operations; its run-state accessor is process-local. The existing `/api/v1/orchestrators` session routes are a separate session lifecycle and are not this feature. The DTOs in `controllers/claude_orchestrator_contract.go` describe a proposed public shape (`runId`, task, opt-in, and an advisory decision), not the internal asynchronous route above.

If the feature later becomes a public `/api/v1` API, first settle the public lifecycle and service adapters, then update the canonical code-first source (`backend/internal/httpd/apispec/specgen/build.go` and controller DTOs/operation registry), regenerate the OpenAPI and frontend types through the repository's generation flow, and retain route/spec parity. Do not hand-edit generated schema artifacts.
