import {
	CLAUDE_ORCHESTRATOR_MAX_SHOT_BYTES,
	isClaudeOrchestratorRunId,
	isClaudeOrchestratorVisualShotName,
	parseClaudeOrchestratorInfo,
	parseClaudeOrchestratorPlan,
	parseClaudeOrchestratorRun,
	parseClaudeOrchestratorRunList,
	parseClaudeOrchestratorVisual,
	validClaudeOrchestratorStartInput,
	type ClaudeOrchestratorErrorCode,
	type ClaudeOrchestratorInfo,
	type ClaudeOrchestratorPlan,
	type ClaudeOrchestratorResult,
	type ClaudeOrchestratorRun,
	type ClaudeOrchestratorVisual,
} from "../shared/claude-orchestrator";

type Fetcher = (input: string, init: RequestInit) => Promise<Response>;

const BASE_PATH = "/internal/claude-orchestrator";
const REQUEST_TIMEOUT_MS = 5_000;
// Publishing pushes a branch and opens a pull request over the network.
const PUBLISH_TIMEOUT_MS = 150_000;
// A screenshot of a long page is a few megabytes read from local disk.
const SHOT_TIMEOUT_MS = 15_000;

const DISABLED_INFO: ClaudeOrchestratorInfo = {
	enabled: false, repository: "", plannerModel: "", workerProvider: "", workerModel: "", workerMode: "model", sandboxed: false, maxActiveRuns: 0,
	plannerProvider: "claude", budgetTokens: 0, budgetUsd: 0, maxReplans: 0, canPublish: false, visualReview: false,
};

/**
 * Main-process client for the daemon's loopback-only orchestrator routes.
 * Those routes reject any request carrying an Origin header, so the renderer
 * cannot call them directly; it goes through this client over IPC. Responses
 * are rebuilt from allowlisted fields and failures are reduced to codes, so no
 * daemon, provider, or worker text reaches the renderer.
 */
export class ClaudeOrchestratorClient {
	constructor(private readonly origin: () => string | null, private readonly fetcher: Fetcher = fetch) {}

	async info(): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorInfo>> {
		const response = await this.request("GET", BASE_PATH);
		if (!response.ok) {
			// The daemon mounts these routes only when the feature is enabled.
			return response.error === "not_found" ? { ok: true, value: DISABLED_INFO } : response;
		}
		const info = parseClaudeOrchestratorInfo(response.body);
		return info ? { ok: true, value: info } : { ok: false, error: "failed" };
	}

	async list(): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun[]>> {
		const response = await this.request("GET", `${BASE_PATH}/runs`);
		if (!response.ok) return response.error === "not_found" ? { ok: false, error: "disabled" } : response;
		const runs = parseClaudeOrchestratorRunList(response.body);
		return runs ? { ok: true, value: runs } : { ok: false, error: "failed" };
	}

	async start(input: unknown): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>> {
		if (!validClaudeOrchestratorStartInput(input)) return { ok: false, error: "invalid_task" };
		// The wire body is built field by field; renderer input is never spread.
		const response = await this.request("POST", `${BASE_PATH}/runs`, {
			task: input.task, maxRetries: input.maxRetries, explicitOptIn: true,
			...(input.approvePlan === true ? { approvePlan: true } : {}),
		});
		if (!response.ok) return response.error === "not_found" ? { ok: false, error: "disabled" } : response;
		const run = parseClaudeOrchestratorRun(response.body);
		return run ? { ok: true, value: run } : { ok: false, error: "failed" };
	}

	async status(runId: unknown): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>> {
		if (!isClaudeOrchestratorRunId(runId)) return { ok: false, error: "not_found" };
		const response = await this.request("GET", `${BASE_PATH}/runs/${runId}`);
		if (!response.ok) return response;
		const run = parseClaudeOrchestratorRun(response.body);
		return run && run.runId === runId ? { ok: true, value: run } : { ok: false, error: "failed" };
	}

	async cancel(runId: unknown): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>> {
		if (!isClaudeOrchestratorRunId(runId)) return { ok: false, error: "not_found" };
		const response = await this.request("POST", `${BASE_PATH}/runs/${runId}/cancel`);
		if (!response.ok) return response;
		const run = parseClaudeOrchestratorRun(response.body);
		return run && run.runId === runId ? { ok: true, value: run } : { ok: false, error: "failed" };
	}

	async plan(runId: unknown): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorPlan>> {
		if (!isClaudeOrchestratorRunId(runId)) return { ok: false, error: "not_found" };
		const response = await this.request("GET", `${BASE_PATH}/runs/${runId}/plan`);
		if (!response.ok) return response.error === "not_found" ? { ok: false, error: "plan_not_ready" } : response;
		const plan = parseClaudeOrchestratorPlan(response.body);
		return plan && plan.runId === runId ? { ok: true, value: plan } : { ok: false, error: "failed" };
	}

	approve(runId: unknown): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>> {
		return this.decide(runId, "approve");
	}

	reject(runId: unknown): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>> {
		return this.decide(runId, "reject");
	}

	/** Pushes a finished run's own branch and opens a pull request. Never merges. */
	async publish(runId: unknown): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>> {
		if (!isClaudeOrchestratorRunId(runId)) return { ok: false, error: "not_found" };
		const response = await this.request("POST", `${BASE_PATH}/runs/${runId}/publish`, undefined, PUBLISH_TIMEOUT_MS);
		if (!response.ok) {
			if (response.error === "already_finished") return { ok: false, error: "no_changes" };
			if (response.error === "failed") return { ok: false, error: response.code === "NO_REMOTE" ? "no_remote" : "publish_failed" };
			return { ok: false, error: response.error };
		}
		const run = parseClaudeOrchestratorRun(response.body);
		return run && run.runId === runId ? { ok: true, value: run } : { ok: false, error: "failed" };
	}

	/** The critic's looks at a run's pages, round by round. */
	async visual(runId: unknown): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorVisual>> {
		if (!isClaudeOrchestratorRunId(runId)) return { ok: false, error: "not_found" };
		const response = await this.request("GET", `${BASE_PATH}/runs/${runId}/visual`);
		if (!response.ok) return response;
		const visual = parseClaudeOrchestratorVisual(response.body);
		return visual && visual.runId === runId ? { ok: true, value: visual } : { ok: false, error: "failed" };
	}

	/** One screenshot the run's visual review kept, as a JPEG data URL. */
	async visualShot(runId: unknown, name: unknown): Promise<ClaudeOrchestratorResult<string>> {
		if (!isClaudeOrchestratorRunId(runId) || !isClaudeOrchestratorVisualShotName(name)) return { ok: false, error: "not_found" };
		const base = this.daemonOrigin();
		if (!base) return { ok: false, error: "daemon_unavailable" };
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), SHOT_TIMEOUT_MS);
		try {
			const response = await this.fetcher(`${base}${BASE_PATH}/runs/${runId}/visual/${name}`, { method: "GET", signal: controller.signal });
			if (!response.ok) return { ok: false, error: errorForStatus(response.status) };
			if (response.headers.get("content-type") !== "image/jpeg") return { ok: false, error: "failed" };
			const declared = Number(response.headers.get("content-length") ?? "0");
			if (declared > CLAUDE_ORCHESTRATOR_MAX_SHOT_BYTES) return { ok: false, error: "failed" };
			const bytes = Buffer.from(await response.arrayBuffer());
			if (bytes.length < 3 || bytes.length > CLAUDE_ORCHESTRATOR_MAX_SHOT_BYTES || bytes[0] !== 0xff || bytes[1] !== 0xd8 || bytes[2] !== 0xff) {
				return { ok: false, error: "failed" };
			}
			return { ok: true, value: `data:image/jpeg;base64,${bytes.toString("base64")}` };
		} catch {
			return { ok: false, error: "daemon_unavailable" };
		} finally {
			clearTimeout(timer);
		}
	}

	private async decide(runId: unknown, decision: "approve" | "reject"): Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>> {
		if (!isClaudeOrchestratorRunId(runId)) return { ok: false, error: "not_found" };
		const response = await this.request("POST", `${BASE_PATH}/runs/${runId}/${decision}`);
		if (!response.ok) return response;
		const run = parseClaudeOrchestratorRun(response.body);
		return run && run.runId === runId ? { ok: true, value: run } : { ok: false, error: "failed" };
	}

	private async request(
		method: "GET" | "POST",
		pathname: string,
		body?: object,
		timeoutMs = REQUEST_TIMEOUT_MS,
	): Promise<{ ok: true; body: unknown } | { ok: false; error: ClaudeOrchestratorErrorCode; code?: string }> {
		const origin = this.daemonOrigin();
		if (!origin) return { ok: false, error: "daemon_unavailable" };
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), timeoutMs);
		try {
			const response = await this.fetcher(`${origin}${pathname}`, {
				method,
				signal: controller.signal,
				headers: body ? { "content-type": "application/json" } : undefined,
				body: body ? JSON.stringify(body) : undefined,
			});
			if (!response.ok) return { ok: false, error: errorForStatus(response.status), code: await errorCode(response) };
			return { ok: true, body: await response.json() };
		} catch {
			return { ok: false, error: "daemon_unavailable" };
		} finally {
			clearTimeout(timer);
		}
	}

	/** The daemon's loopback origin, or null when it is unknown or not loopback. */
	private daemonOrigin(): string | null {
		const base = this.origin();
		if (!base) return null;
		let parsed: URL;
		try {
			parsed = new URL(base);
		} catch {
			return null;
		}
		if (parsed.protocol !== "http:" || parsed.hostname !== "127.0.0.1" || parsed.username || parsed.password || parsed.pathname !== "/" || parsed.search || parsed.hash) {
			return null;
		}
		return parsed.origin;
	}
}

/** The daemon's machine-readable error code; its message is never read. */
async function errorCode(response: Response): Promise<string | undefined> {
	try {
		const body = (await response.json()) as { code?: unknown };
		return typeof body.code === "string" && /^[A-Z_]{1,64}$/.test(body.code) ? body.code : undefined;
	} catch {
		return undefined;
	}
}

function errorForStatus(status: number): ClaudeOrchestratorErrorCode {
	switch (status) {
		case 400:
		case 413:
			return "invalid_task";
		case 403:
			return "disabled";
		case 404:
			return "not_found";
		case 409:
			return "already_finished";
		case 429:
			return "too_many_runs";
		default:
			return "failed";
	}
}
