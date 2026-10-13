// Shared contract for the Claude orchestrator bridge. The main process calls
// the daemon's loopback-only /internal/claude-orchestrator routes and returns
// these validated shapes to the renderer over IPC; nothing here carries a
// credential or a local path. The only model output is a run's plan, which a
// person reviews before approving; it arrives secret-redacted and bounded.

export const CLAUDE_ORCHESTRATOR_RUN_STATES = [
	"pending",
	"planning",
	"executing",
	"validating",
	"reviewing",
	"awaiting_approval",
	"completed",
	"held",
	"failed",
	"canceled",
] as const;
export type ClaudeOrchestratorRunState = (typeof CLAUDE_ORCHESTRATOR_RUN_STATES)[number];

export const CLAUDE_ORCHESTRATOR_ACTIVE_STAGES = ["planning", "executing", "validating", "reviewing"] as const;
export type ClaudeOrchestratorStage = (typeof CLAUDE_ORCHESTRATOR_ACTIVE_STAGES)[number];

export type ClaudeOrchestratorRecommendation = "merge" | "hold";

export type ClaudeOrchestratorWorkerMode = "model" | "agents";

export type ClaudeOrchestratorInfo = {
	enabled: boolean;
	repository: string;
	plannerModel: string;
	workerProvider: string;
	workerModel: string;
	/** "agents" when AO agent sessions write the code; workerModel is then the harness. */
	workerMode: ClaudeOrchestratorWorkerMode;
	sandboxed: boolean;
	maxActiveRuns: number;
	/** "claude" or "local": who plans and reviews. */
	plannerProvider: string;
	/** Each run's token budget; 0 means no limit. */
	budgetTokens: number;
	/** Each run's cost budget in US dollars; 0 means no limit. */
	budgetUsd: number;
	/** How many times Claude may revise a plan after failures. */
	maxReplans: number;
	/** Finished runs can be pushed and opened as a pull request. */
	canPublish: boolean;
};

/** A run's model usage. Agent sessions bill through their own harness. */
export type ClaudeOrchestratorUsage = {
	inputTokens: number;
	outputTokens: number;
	calls: number;
	/** Estimated cost in US dollars; absent when a provider has no price. */
	costUsd?: number;
	budgetTokens?: number;
	budgetUsd?: number;
	/** The run stopped because its budget was used up. */
	exhausted?: boolean;
};

export type ClaudeOrchestratorRun = {
	runId: string;
	state: ClaudeOrchestratorRunState;
	title: string;
	branch?: string;
	commit?: string;
	recommendation?: ClaudeOrchestratorRecommendation;
	/** The run was still active when the app or daemon stopped. */
	interrupted?: boolean;
	createdAt?: string;
	updatedAt?: string;
	usage?: ClaudeOrchestratorUsage;
	/** How many times Claude revised the plan after failures. */
	replans?: number;
	/** The pull request opened for this run. */
	pullRequestUrl?: string;
	/** Open this to create the pull request by hand. */
	compareUrl?: string;
};

export type ClaudeOrchestratorStartInput = { task: string; maxRetries: number; approvePlan?: boolean };

export type ClaudeOrchestratorPlanStep = {
	id: string;
	title: string;
	instructions: string;
	provider?: string;
	dependsOn: string[];
};

/** Claude's plan for a run, shown for approval before anything runs. */
export type ClaudeOrchestratorPlan = { runId: string; summary: string; steps: ClaudeOrchestratorPlanStep[] };

/** Why a bridge call did not succeed. Never carries daemon or provider text. */
export type ClaudeOrchestratorErrorCode =
	| "daemon_unavailable"
	| "disabled"
	| "too_many_runs"
	| "invalid_task"
	| "not_found"
	| "already_finished"
	| "plan_not_ready"
	| "no_changes"
	| "no_remote"
	| "publish_failed"
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
	if (Object.keys(candidate).some((key) => key !== "task" && key !== "maxRetries" && key !== "approvePlan")) return false;
	if (candidate.approvePlan !== undefined && typeof candidate.approvePlan !== "boolean") return false;
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

function count(value: unknown): number | undefined {
	return typeof value === "number" && Number.isFinite(value) && value >= 0 && value <= Number.MAX_SAFE_INTEGER ? value : undefined;
}

function parseUsage(value: unknown): ClaudeOrchestratorUsage | undefined {
	const candidate = record(value);
	if (!candidate) return undefined;
	const usage: ClaudeOrchestratorUsage = {
		inputTokens: Math.floor(count(candidate.inputTokens) ?? 0),
		outputTokens: Math.floor(count(candidate.outputTokens) ?? 0),
		calls: Math.floor(count(candidate.calls) ?? 0),
	};
	const cost = count(candidate.costUsd);
	if (cost !== undefined) usage.costUsd = cost;
	const budgetTokens = count(candidate.budgetTokens);
	if (budgetTokens) usage.budgetTokens = Math.floor(budgetTokens);
	const budgetUsd = count(candidate.budgetUsd);
	if (budgetUsd) usage.budgetUsd = budgetUsd;
	if (candidate.exhausted === true) usage.exhausted = true;
	return usage;
}

/** Only plain https review links without credentials are kept. */
function reviewUrl(value: unknown): string | undefined {
	if (typeof value !== "string" || value.length > 2048) return undefined;
	try {
		const url = new URL(value);
		if (url.protocol !== "https:" || url.username || url.password) return undefined;
		return url.toString();
	} catch {
		return undefined;
	}
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
	if (candidate.interrupted === true) run.interrupted = true;
	const usage = parseUsage(candidate.usage);
	if (usage) run.usage = usage;
	if (typeof candidate.replans === "number" && Number.isInteger(candidate.replans) && candidate.replans > 0) run.replans = Math.min(candidate.replans, 10);
	const pullRequestUrl = reviewUrl(candidate.pullRequestUrl);
	if (pullRequestUrl) run.pullRequestUrl = pullRequestUrl;
	const compareUrl = reviewUrl(candidate.compareUrl);
	if (compareUrl) run.compareUrl = compareUrl;
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
		workerMode: candidate.workerMode === "agents" ? "agents" : "model",
		sandboxed: candidate.sandboxed === true,
		maxActiveRuns,
		plannerProvider: candidate.plannerProvider === "local" ? "local" : "claude",
		budgetTokens: Math.floor(count(candidate.budgetTokens) ?? 0),
		budgetUsd: count(candidate.budgetUsd) ?? 0,
		maxReplans: Math.min(Math.floor(count(candidate.maxReplans) ?? 0), 10),
		canPublish: candidate.canPublish === true,
	};
}

const PLAN_MAX_STEPS = 50;

/** Rebuilds a plan from allowlisted, length-bounded fields only. */
export function parseClaudeOrchestratorPlan(value: unknown): ClaudeOrchestratorPlan | null {
	const candidate = record(value);
	if (!candidate || !isClaudeOrchestratorRunId(candidate.runId) || !Array.isArray(candidate.subtasks)) return null;
	const steps: ClaudeOrchestratorPlanStep[] = [];
	for (const raw of candidate.subtasks.slice(0, PLAN_MAX_STEPS)) {
		const step = record(raw);
		const id = step && boundedText(step.id, 200);
		if (!step || !id) return null;
		const dependsOn = Array.isArray(step.dependsOn)
			? step.dependsOn.filter((dependency): dependency is string => typeof dependency === "string" && dependency.length <= 200).slice(0, 20)
			: [];
		const provider = boundedText(step.provider, 200);
		steps.push({
			id,
			title: boundedText(step.title, 800) ?? "",
			instructions: boundedText(step.instructions, 16_000) ?? "",
			...(provider ? { provider } : {}),
			dependsOn,
		});
	}
	return { runId: candidate.runId, summary: boundedText(candidate.summary, 4000) ?? "", steps };
}
