// Shared contract for the Claude orchestrator bridge. The main process calls
// the daemon's loopback-only /internal/claude-orchestrator routes and returns
// these validated shapes to the renderer over IPC; nothing here carries a
// credential, a local path, or model/worker output.

export const CLAUDE_ORCHESTRATOR_RUN_STATES = [
	"pending",
	"planning",
	"executing",
	"validating",
	"reviewing",
	"completed",
	"held",
	"failed",
	"canceled",
] as const;
export type ClaudeOrchestratorRunState = (typeof CLAUDE_ORCHESTRATOR_RUN_STATES)[number];

export const CLAUDE_ORCHESTRATOR_ACTIVE_STAGES = ["planning", "executing", "validating", "reviewing"] as const;
export type ClaudeOrchestratorStage = (typeof CLAUDE_ORCHESTRATOR_ACTIVE_STAGES)[number];

export type ClaudeOrchestratorRecommendation = "merge" | "hold";

export type ClaudeOrchestratorInfo = {
	enabled: boolean;
	repository: string;
	plannerModel: string;
	workerProvider: string;
	workerModel: string;
	sandboxed: boolean;
	maxActiveRuns: number;
};

export type ClaudeOrchestratorRun = {
	runId: string;
	state: ClaudeOrchestratorRunState;
	title: string;
	branch?: string;
	commit?: string;
	recommendation?: ClaudeOrchestratorRecommendation;
	createdAt?: string;
	updatedAt?: string;
};

export type ClaudeOrchestratorStartInput = { task: string; maxRetries: number };

/** Why a bridge call did not succeed. Never carries daemon or provider text. */
export type ClaudeOrchestratorErrorCode =
	| "daemon_unavailable"
	| "disabled"
	| "too_many_runs"
	| "invalid_task"
	| "not_found"
	| "already_finished"
	| "failed";

export type ClaudeOrchestratorResult<T> = { ok: true; value: T } | { ok: false; error: ClaudeOrchestratorErrorCode };

export const CLAUDE_ORCHESTRATOR_MAX_TASK_BYTES = 8 * 1024;
export const CLAUDE_ORCHESTRATOR_MAX_RETRIES = 3;
const RUN_ID_PATTERN = /^claude-run-[0-9a-f]{32}$/;
const COMMIT_PATTERN = /^[0-9a-f]{7,64}$/;
const BRANCH_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$/;

export function isClaudeOrchestratorRunId(value: unknown): value is string {
	return typeof value === "string" && RUN_ID_PATTERN.test(value);
}

export function isTerminalClaudeOrchestratorState(state: ClaudeOrchestratorRunState): boolean {
	return state === "completed" || state === "held" || state === "failed" || state === "canceled";
}

export function validClaudeOrchestratorStartInput(input: unknown): input is ClaudeOrchestratorStartInput {
	if (!input || typeof input !== "object" || Array.isArray(input)) return false;
	const candidate = input as Record<string, unknown>;
	if (Object.keys(candidate).some((key) => key !== "task" && key !== "maxRetries")) return false;
	if (typeof candidate.task !== "string" || candidate.task.trim() === "" || candidate.task.includes("\u0000")) return false;
	if (new TextEncoder().encode(candidate.task).byteLength > CLAUDE_ORCHESTRATOR_MAX_TASK_BYTES) return false;
	return typeof candidate.maxRetries === "number" && Number.isInteger(candidate.maxRetries)
		&& candidate.maxRetries >= 0 && candidate.maxRetries <= CLAUDE_ORCHESTRATOR_MAX_RETRIES;
}

function record(value: unknown): Record<string, unknown> | null {
	return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function boundedText(value: unknown, max: number): string | null {
	if (typeof value !== "string" || value.length > max) return null;
	return value;
}

function isoTime(value: unknown): string | undefined {
	if (typeof value !== "string" || value.length > 64 || Number.isNaN(Date.parse(value))) return undefined;
	// The daemon reports zero times as 0001-01-01; treat them as unknown.
	return value.startsWith("0001-") ? undefined : value;
}

/** Rebuilds a run from allowlisted fields only; anything else is dropped. */
export function parseClaudeOrchestratorRun(value: unknown): ClaudeOrchestratorRun | null {
	const candidate = record(value);
	if (!candidate || !isClaudeOrchestratorRunId(candidate.runId)) return null;
	const state = candidate.state;
	if (typeof state !== "string" || !CLAUDE_ORCHESTRATOR_RUN_STATES.includes(state as ClaudeOrchestratorRunState)) return null;
	const run: ClaudeOrchestratorRun = {
		runId: candidate.runId,
		state: state as ClaudeOrchestratorRunState,
		title: boundedText(candidate.title, 400) ?? "",
	};
	if (typeof candidate.branch === "string" && BRANCH_PATTERN.test(candidate.branch)) run.branch = candidate.branch;
	if (typeof candidate.commit === "string" && COMMIT_PATTERN.test(candidate.commit)) run.commit = candidate.commit;
	if (candidate.recommendation === "merge" || candidate.recommendation === "hold") run.recommendation = candidate.recommendation;
	const createdAt = isoTime(candidate.createdAt);
	const updatedAt = isoTime(candidate.updatedAt);
	if (createdAt) run.createdAt = createdAt;
	if (updatedAt) run.updatedAt = updatedAt;
	return run;
}

export function parseClaudeOrchestratorRunList(value: unknown): ClaudeOrchestratorRun[] | null {
	const candidate = record(value);
	if (!candidate || !Array.isArray(candidate.runs) || candidate.runs.length > 1000) return null;
	const runs: ClaudeOrchestratorRun[] = [];
	for (const item of candidate.runs) {
		const run = parseClaudeOrchestratorRun(item);
		if (!run) return null;
		runs.push(run);
	}
	return runs;
}

export function parseClaudeOrchestratorInfo(value: unknown): ClaudeOrchestratorInfo | null {
	const candidate = record(value);
	if (!candidate || typeof candidate.enabled !== "boolean") return null;
	const text = (key: string) => boundedText(candidate[key], 200) ?? "";
	const maxActiveRuns = typeof candidate.maxActiveRuns === "number" && Number.isInteger(candidate.maxActiveRuns) && candidate.maxActiveRuns > 0
		? Math.min(candidate.maxActiveRuns, 100)
		: 1;
	return {
		enabled: candidate.enabled,
		repository: text("repository"),
		plannerModel: text("plannerModel"),
		workerProvider: text("workerProvider"),
		workerModel: text("workerModel"),
		sandboxed: candidate.sandboxed === true,
		maxActiveRuns,
	};
}
