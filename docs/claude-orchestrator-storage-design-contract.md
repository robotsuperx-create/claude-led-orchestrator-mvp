# Claude orchestrator persistence: design contract

**Status: deferred; no SQLite migration or store is added by this change.** The current run API and service keep lifecycle state in process memory. This document records the safety contract required before adding durable `orchestrator_runs` / `orchestrator_events` storage; it is not a claim that those tables or behaviors exist.

## Why a schema change is not safe as a small isolated migration

- SQLite schema and query SQL are inputs to the pinned sqlc generator (`backend/sqlc.yaml`, `backend/internal/storage/sqlite/migrations`, and `backend/internal/storage/sqlite/queries`). Generated code under `backend/internal/storage/sqlite/gen` must be regenerated with `npm run sqlc`; it must not be hand-edited. A store also needs application wiring and migration/store tests, not only table DDL.
- Change-log CDC is database-trigger-owned. `change_log.event_type` has a SQL `CHECK` allowlist, mirrored by `internal/cdc.EventType`; extending it entails a table-rebuild migration and coordinated updates/tests for the SQL allowlist, typed events, and sqlc output. Store methods must not manually emit parallel CDC events.
- The existing `change_log` requires a non-null `project_id` referencing `projects`. The current internal run contract has no project identity, so a run cannot safely emit a project-scoped CDC event. Adding project identity or a deliberately global CDC scope is a product/API decision, not an implicit migration detail.
- The service's states are in-memory and the current API has no durable idempotency key or cancellation operation. Durable leases, fencing, terminal-state races, and cancellation therefore need one coherent store/worker contract before they can be safely layered onto the existing behavior.

## Data and privacy contract

1. **Never persist task text or prompts.** This includes the request task, planned subtasks/instructions, previous failures, model/provider inputs or outputs, validation output, worker stdout/stderr, raw errors, and exception strings. Do not persist API keys, authorization headers, or credentials. Existing project-memory outcome recording is a separate integration and must not be treated as permission to copy request text into the new run store.
2. Persist only lifecycle metadata (opaque run ID, allowlisted state/event code, timestamps, attempt count, monotonically increasing fence, cancellation marker, and an idempotency-token digest) plus an optional **redacted summary**. Prefer a fixed `summary_code`; if human-readable summaries are retained, cap their encoded length, apply the shared secret redactor before persistence, and fail closed to a generic summary when the source is not explicitly safe. Redaction is not permission to store prompts or arbitrary model/worker text.
3. Never put summaries, task-derived text, or credentials into CDC payloads. CDC payloads are a minimal allowlisted projection (opaque run ID, event code/state, and event sequence only). Until a project/global CDC scope is approved, keep run events private to the store contract and do not invent a manual `change_log` emission path.
4. The client supplies a high-entropy idempotency token (at least 128 random bits); persist only its digest, never the raw token. A repeated token resolves to the original run and must not enqueue a second worker, regardless of retry timing. A reused token never starts a different task; because task text/fingerprints are not retained, the store must not claim it can compare request bodies.

## Proposed persistence semantics (not implemented)

- `orchestrator_runs` is the current snapshot; `orchestrator_events` is append-only history. Use stable, allowlisted lifecycle values. A minimal shape is: run ID, idempotency digest, state, fence, lease expiry, cancellation time, safe summary/code, and creation/update/terminal times; events contain run ID, per-run sequence, event code/state, fence, safe summary/code, and creation time. No request or provider payload columns.
- Create-or-get by the unique idempotency digest is atomic: concurrent requests with the same token return the same run ID and create at most one dispatchable run.
- Claim/reclaim occurs in one SQLite write transaction. A successful claim increments the run's integer fence; a lease expiry permits reclaim but never decrements or reuses a fence. Every heartbeat, event append, and state transition from a worker is conditional on its claimed fence still being current. A stale fence changes neither the snapshot nor history.
- Cancellation is a durable state transition with a single transactional winner against completion/failure. Cancellation invalidates the active fence so a late worker cannot publish success; cancellation of the live context is best-effort process control, while the persisted fence/state is the correctness boundary. Repeated cancellation is idempotent; a terminal run cannot be reopened.
- The event row and current snapshot transition commit atomically. If/when CDC is supported, SQLite triggers project these changes into the approved CDC scope; no store method emits a second event. Payloads remain metadata-only and event ordering is deterministic.
- Retention and crash recovery must be specified before shipping: expired nonterminal leases can be reclaimed with a new fence; terminal history is retained/pruned under an explicit policy without changing idempotency guarantees for the documented replay window.

## Schema-independent contract test vectors

These tests are intended for a small fake-store / service contract suite. They exercise the semantics without SQLite, migrations, sqlc-generated types, or CDC wiring; they do **not** replace later migration, trigger, and store integration tests.

| Case | Setup and operation | Required assertion |
|---|---|---|
| Same-key replay | Submit twice concurrently with the same high-entropy idempotency token. | One run ID, one dispatchable run, and no duplicate worker execution. |
| Key reuse | Submit a second body with a token already used by another request. | Resolve to the original run; never enqueue the second body. The contract does not compare or persist either body. |
| Privacy boundary | Use unique sentinel prompt, subtask, worker-output, error, and API-key strings; create, advance, fail, cancel, and read an event. | None of the sentinels appears in any fake-store write, event, summary, or externally observable persisted representation. |
| Summary redaction | Feed a candidate summary containing bearer/API-key-like material or an unapproved raw output. | Persist only the approved redacted summary or generic `summary_code`; never persist the original candidate. |
| Fence monotonicity | Claim, expire, and reclaim a run. | The new fence is strictly greater; the old claimant cannot heartbeat, append an event, or change state. |
| Stale completion | Claim with fence N, cancel or reclaim (fence N+1), then deliver a late success from N. | No completion is recorded and no success event is emitted; the newer state remains authoritative. |
| Cancel/completion race | Race cancellation and completion at the transaction boundary. | Exactly one terminal outcome wins; repeated cancel is idempotent; no later transition reopens the run. |
| Event atomicity/order | Cause a state transition and inject a persistence failure between snapshot/event operations in the fake transaction. | Both snapshot and event commit or neither does; committed per-run event sequences are strictly increasing and unique. |
| CDC projection | Project a committed event through a fake trigger-boundary adapter. | Only allowlisted metadata is emitted; no free text, prompt, credential, or raw error is present. |

## Implementation gate

Before a follow-up changes schema: decide how a run receives project scope (or approve a separate global CDC contract); add the query source and migration together; update the CDC allowlist/types and tests if trigger events are required; run the pinned `npm run sqlc`; implement a narrow store interface with transaction/fence predicates; integrate durable cancellation/idempotency with the worker lifecycle; and add both schema-independent contract tests above and SQLite migration/store/trigger tests. Do not expose persisted task/result retrieval as an accidental consequence of storing runs.
