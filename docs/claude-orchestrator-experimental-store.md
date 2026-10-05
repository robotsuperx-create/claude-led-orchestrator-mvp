# Experimental orchestrator run store

**Status: interface and volatile adapter only; no database schema or CDC behavior is implemented.**

## Blocker to a safe migration

The requested `orchestrator_runs` / `orchestrator_events` migration was deliberately deferred after inspecting the SQLite migration, sqlc, and CDC paths:

- `change_log.project_id` is non-null and references `projects`; the current `OrchestrationRequest` has only a run ID, task, and retry count, not a project identity. There is no safely derivable project scope for a database-backed run.
- CDC is owned by SQLite triggers. Its `event_type` constraint is a fixed allowlist and is mirrored in Go. Adding run events to that channel requires an intentional table-rebuild migration plus coordinated trigger, allowlist, typed-event, and tests changes. No global CDC scope has been approved, so this change must not invent a manual `change_log` write path.
- The existing orchestrator service is in-memory. It has no durable idempotency token or fencing-token contract. Persisting state without atomically integrating create/replay, worker claim/reclaim, cancellation, terminal transitions, and worker wiring would imply guarantees the service does not provide.

These blockers mean no migration, query SQL, generated sqlc changes, or persistent SQLite store are added. The existing design gate is in [the storage design contract](claude-orchestrator-storage-design-contract.md).

## Experimental adapter delivered

- `ports.ProjectOrchestratorRunStore` is a project-scoped interface. It accepts lifecycle metadata only; it has no task, prompt, provider input, credential, or worker-output fields.
- `adapters/orchestratorstore.ExperimentalMemoryStore` hashes idempotency tokens immediately (minimum 16 bytes), stores no raw token, supports atomic create-or-get, lease claims with monotonic fencing, stale-worker rejection, append-only metadata events, allowlisted summary codes, and idempotent cancellation that invalidates the active fence.
- “Summary” means an allowlisted classification such as `succeeded` or `failed`, not human-readable or redacted free text. Events contain only opaque run/project IDs, sequence, event/state codes, fence, summary code, and timestamp.
- The adapter is intentionally volatile and is not wired to the current service. It is a contract/test prototype only; it does not provide restart recovery, multi-process locking, durable idempotency retention, or CDC projection.

Before durable implementation, project scope must be propagated through the authenticated request contract, CDC scope and lifecycle state machine approved, storage retention/replay policy set, then a migration/query/store can be added together and wired to the worker lifecycle.
