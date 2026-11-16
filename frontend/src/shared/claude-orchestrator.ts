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
	/** Why the orchestrator is off when its saved settings need fixing. */
	configError?: string;
	/** Each run's pages are photographed and judged by Claude, then polished. */
	visualReview: boolean;
	/** Why the visual review cannot run although it was asked for. */
	visualReason?: ClaudeOrchestratorVisualReason;
};

export type ClaudeOrchestratorVisualReason = "no_browser" | "no_claude";

/** One kept screenshot of a run's page at one screen size. */
export type ClaudeOrchestratorVisualShot = {
	page: string;
	viewport: string;
	/** The picture's name under the run; absent when none was kept. */
	name?: string;
	/** What the browser measured there (overflow, contrast, tap targets…). */
	findings: string[];
};

/** One look Claude took at a run's pages, as an art director. */
export type ClaudeOrchestratorVisualRound = {
	round: number;
	/** The overall mark out of ten. */
	score: number;
	scores: Record<string, number>;
	summary: string;
	fixes: string[];
	shots: ClaudeOrchestratorVisualShot[];
	/** A writer polished the pages after this look. */
	polished: boolean;
	/** The look could not be finished; the run went on without it. */
	failed: boolean;
	findings: number;
};

export type ClaudeOrchestratorVisual = { runId: string; rounds: ClaudeOrchestratorVisualRound[] };

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
		...(boundedText(candidate.configError, 1000) ? { configError: candidate.configError as string } : {}),
		visualReview: candidate.visualReview === true,
		...(candidate.visualReason === "no_browser" || candidate.visualReason === "no_claude" ? { visualReason: candidate.visualReason } : {}),
	};
}

const VISUAL_SHOT_NAME = /^r[0-9]{1,2}-[a-z0-9-]{1,64}-[a-z0-9-]{1,64}\.jpg$/;
const VISUAL_MAX_ROUNDS = 10;
const VISUAL_MAX_SHOTS = 12;

/** A kept screenshot's name, as the daemon's camera writes it. */
export function isClaudeOrchestratorVisualShotName(value: unknown): value is string {
	return typeof value === "string" && VISUAL_SHOT_NAME.test(value);
}

function score(value: unknown): number {
	return typeof value === "number" && Number.isFinite(value) ? Math.min(Math.max(value, 0), 10) : 0;
}

function lines(value: unknown, limit: number, max: number): string[] {
	if (!Array.isArray(value)) return [];
	return value.filter((line): line is string => typeof line === "string" && line.length > 0 && line.length <= max).slice(0, limit);
}

/** Rebuilds a run's visual review from allowlisted, length-bounded fields only. */
export function parseClaudeOrchestratorVisual(value: unknown): ClaudeOrchestratorVisual | null {
	const candidate = record(value);
	if (!candidate || !isClaudeOrchestratorRunId(candidate.runId) || !Array.isArray(candidate.rounds)) return null;
	const rounds: ClaudeOrchestratorVisualRound[] = [];
	for (const raw of candidate.rounds.slice(-VISUAL_MAX_ROUNDS)) {
		const round = record(raw);
		if (!round || typeof round.round !== "number" || !Number.isInteger(round.round) || round.round < 1 || round.round > 99) return null;
		const scores: Record<string, number> = {};
		const rawScores = record(round.scores);
		for (const [name, mark] of Object.entries(rawScores ?? {}).slice(0, 12)) {
			if (/^[a-z]{1,40}$/.test(name)) scores[name] = score(mark);
		}
		const shots: ClaudeOrchestratorVisualShot[] = [];
		for (const rawShot of Array.isArray(round.shots) ? round.shots.slice(0, VISUAL_MAX_SHOTS) : []) {
			const shot = record(rawShot);
			if (!shot) continue;
			shots.push({
				page: boundedText(shot.page, 800) ?? "",
				viewport: boundedText(shot.viewport, 200) ?? "",
				...(isClaudeOrchestratorVisualShotName(shot.name) ? { name: shot.name } : {}),
				findings: lines(shot.findings, 20, 1600),
			});
		}
		rounds.push({
			round: round.round,
			score: score(round.score),
			scores,
			summary: boundedText(round.summary, 4800) ?? "",
			fixes: lines(round.fixes, 8, 1600),
			shots,
			polished: typeof round.polish === "string" && round.polish.length > 0,
			failed: round.failed === true,
			findings: Math.floor(count(round.findings) ?? 0),
		});
	}
	return { runId: candidate.runId, rounds };
}

/** A screenshot handed to the renderer: a JPEG data URL, nothing else. */
export const CLAUDE_ORCHESTRATOR_MAX_SHOT_BYTES = 24 * 1024 * 1024;

export function isClaudeOrchestratorShotDataUrl(value: unknown): value is string {
	return typeof value === "string" && value.startsWith("data:image/jpeg;base64,") && /^[A-Za-z0-9+/]*={0,2}$/.test(value.slice(23))
		&& value.length <= 23 + Math.ceil(CLAUDE_ORCHESTRATOR_MAX_SHOT_BYTES / 3) * 4;
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

/** How one step of a run's plan is going. */
export const CLAUDE_ORCHESTRATOR_LIVE_STEP_STATES = ["waiting", "running", "done", "failed", "blocked"] as const;
export type ClaudeOrchestratorLiveStepState = (typeof CLAUDE_ORCHESTRATOR_LIVE_STEP_STATES)[number];

/** One plan step as the crew carries it; its index names the crew member (X-01 is 0). */
export type ClaudeOrchestratorLiveStep = {
	index: number;
	id: string;
	title: string;
	state: ClaudeOrchestratorLiveStepState;
	attempt: number;
};

/**
 * One file the run has written to: counts, and the last lines it gained. The
 * lines are the run's own code from its own worktree, secret-redacted and
 * bounded by the daemon; the renderer shows them as text, never as markup.
 */
export type ClaudeOrchestratorLiveFile = {
	path: string;
	added: number;
	removed: number;
	created: boolean;
	lines: string[];
};

/** What a run is doing right now, for the deck. */
export type ClaudeOrchestratorLive = { runId: string; steps: ClaudeOrchestratorLiveStep[]; files: ClaudeOrchestratorLiveFile[] };

const LIVE_MAX_STEPS = 40;
const LIVE_MAX_FILES = 12;
const LIVE_MAX_LINES = 24;
const LIVE_MAX_LINE = 240;
const LIVE_PATH = /^(?!\/)(?![A-Za-z]:)(?!.*(?:^|\/)\.\.(?:\/|$))[^\0\r\n\\:]{1,200}$/;

/** A repository-relative path the deck may show: never absolute, never climbing out. */
export function isClaudeOrchestratorLivePath(value: unknown): value is string {
	return typeof value === "string" && LIVE_PATH.test(value);
}

/** Rebuilds a run's live work from allowlisted, length-bounded fields only. */
export function parseClaudeOrchestratorLive(value: unknown): ClaudeOrchestratorLive | null {
	const candidate = record(value);
	if (!candidate || !isClaudeOrchestratorRunId(candidate.runId) || !Array.isArray(candidate.steps) || !Array.isArray(candidate.files)) return null;
	const steps: ClaudeOrchestratorLiveStep[] = [];
	for (const raw of candidate.steps.slice(0, LIVE_MAX_STEPS)) {
		const step = record(raw);
		const id = step && boundedText(step.id, 200);
		const state = step?.state;
		if (!step || !id || typeof state !== "string" || !(CLAUDE_ORCHESTRATOR_LIVE_STEP_STATES as readonly string[]).includes(state)) continue;
		const index = count(step.index);
		if (index === undefined || !Number.isInteger(index) || index >= LIVE_MAX_STEPS) continue;
		steps.push({
			index,
			id,
			title: boundedText(step.title, 800) ?? "",
			state: state as ClaudeOrchestratorLiveStepState,
			attempt: Math.min(Math.floor(count(step.attempt) ?? 0), 99),
		});
	}
	const files: ClaudeOrchestratorLiveFile[] = [];
	for (const raw of candidate.files.slice(0, LIVE_MAX_FILES)) {
		const file = record(raw);
		if (!file || !isClaudeOrchestratorLivePath(file.path)) continue;
		const codeLines = Array.isArray(file.lines)
			? file.lines.filter((line): line is string => typeof line === "string" && line.length <= LIVE_MAX_LINE).slice(-LIVE_MAX_LINES)
			: [];
		files.push({
			path: file.path,
			added: Math.floor(count(file.added) ?? 0),
			removed: Math.floor(count(file.removed) ?? 0),
			created: file.new === true,
			lines: codeLines,
		});
	}
	return { runId: candidate.runId, steps, files };
}

/** A run's web pages, as the renderer knows them: names only, never where they live. */
export type ClaudeOrchestratorSitePages = { runId: string; pages: string[] };

const SITE_MAX_PAGES = 8;
const SITE_PAGE = /^(?!\/)(?!.*(?:^|\/)\.\.(?:\/|$))[A-Za-z0-9._ /-]{1,200}\.html?$/;

/** A page name the live preview may open: an HTML file inside the run's worktree. */
export function isClaudeOrchestratorSitePage(value: unknown): value is string {
	return typeof value === "string" && SITE_PAGE.test(value);
}

/** The scheme the main process serves a run's pages on, one origin per run. */
export const CLAUDE_ORCHESTRATOR_SITE_SCHEME = "xrobot-site";

/** The address of one of a run's pages in the live preview. */
export function claudeOrchestratorSiteUrl(runId: string, page: string): string {
	return `${CLAUDE_ORCHESTRATOR_SITE_SCHEME}://${runId.replace("claude-run-", "run-")}/${page.split("/").map(encodeURIComponent).join("/")}`;
}

/** The run a preview address belongs to, from its host. */
export function claudeOrchestratorSiteRunId(host: string): string | null {
	const runId = host.replace(/^run-/, "claude-run-");
	return isClaudeOrchestratorRunId(runId) ? runId : null;
}

/** Reads the daemon's site answer in the main process: the root stays there. */
export function parseClaudeOrchestratorSite(value: unknown): { runId: string; root: string; pages: string[] } | null {
	const candidate = record(value);
	if (!candidate || !isClaudeOrchestratorRunId(candidate.runId) || typeof candidate.root !== "string" || !Array.isArray(candidate.pages)) return null;
	const root = boundedText(candidate.root, 4096);
	if (!root) return null;
	return { runId: candidate.runId, root, pages: candidate.pages.filter(isClaudeOrchestratorSitePage).slice(0, SITE_MAX_PAGES) };
}
