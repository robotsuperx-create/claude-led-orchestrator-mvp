/** Local presentation states only; these are not generated API enums. */
export const ORCHESTRATOR_RUN_STATUSES = [
	"queued",
	"running",
	"awaiting_consent",
	"on_hold",
	"succeeded",
	"failed",
	"cancelled",
] as const;
export type OrchestratorRunStatus = (typeof ORCHESTRATOR_RUN_STATUSES)[number];

/** Lifecycle states returned by the injected local-run API client. */
export const ORCHESTRATOR_RUN_WIRE_STATES = [
	"pending",
	"planning",
	"executing",
	"validating",
	"reviewing",
	"completed",
	"held",
	"failed",
	// Matches the daemon's RunStateCanceled ("canceled", one l).
	"canceled",
] as const;
export type OrchestratorRunWireState = (typeof ORCHESTRATOR_RUN_WIRE_STATES)[number];
export type OrchestratorRunSnapshot = { runId: string; state: OrchestratorRunWireState };

const runStatusByWireState: Record<OrchestratorRunWireState, OrchestratorRunStatus> = {
	pending: "queued",
	planning: "running",
	executing: "running",
	validating: "running",
	reviewing: "running",
	completed: "succeeded",
	held: "on_hold",
	failed: "failed",
	canceled: "cancelled",
};
// `held` is a terminal service outcome (the merge decision is advisory); keep
// polling/cancel controls closed once the backend reports it.
const terminalWireStates = new Set<OrchestratorRunWireState>(["completed", "held", "failed", "canceled"]);

export function isTerminalOrchestratorRunState(state: OrchestratorRunWireState): boolean {
	return terminalWireStates.has(state);
}

/** Projects only display-safe lifecycle fields; wire extras are intentionally ignored. */
export function toOrchestratorRunViewModel(
	snapshot: OrchestratorRunSnapshot,
	options: { cancelAvailable?: boolean } = {},
): OrchestratorRunViewModel {
	return {
		runId: snapshot.runId,
		status: runStatusByWireState[snapshot.state],
		consent: "not_required",
		held: snapshot.state === "held",
		merge: "not_requested",
		cancelAvailable: Boolean(options.cancelAvailable) && !isTerminalOrchestratorRunState(snapshot.state),
	};
}

export const ORCHESTRATOR_CONSENT_STATES = ["required", "granted", "declined", "not_required"] as const;
export type OrchestratorConsentState = (typeof ORCHESTRATOR_CONSENT_STATES)[number];

export const ORCHESTRATOR_MERGE_STATES = ["not_requested", "waiting", "ready", "merged", "blocked"] as const;
export type OrchestratorMergeState = (typeof ORCHESTRATOR_MERGE_STATES)[number];

export type OrchestratorBudgetView = {
	used: number;
	limit: number;
};

/**
 * An intentionally allowlisted UI projection. Do not pass a wire response or
 * credentials to the view; adapt only these display-safe fields at the host.
 */
export type OrchestratorRunViewModel = {
	runId: string;
	status: OrchestratorRunStatus;
	provider?: string;
	model?: string;
	tokenBudget?: OrchestratorBudgetView;
	costBudgetUsd?: OrchestratorBudgetView;
	consent: OrchestratorConsentState;
	held: boolean;
	merge: OrchestratorMergeState;
	cancelAvailable: boolean;
};
