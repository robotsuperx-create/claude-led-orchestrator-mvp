import {
	isClaudeOrchestratorRunId,
	parseClaudeOrchestratorInfo,
	parseClaudeOrchestratorRun,
	parseClaudeOrchestratorRunList,
	validClaudeOrchestratorStartInput,
	type ClaudeOrchestratorErrorCode,
	type ClaudeOrchestratorInfo,
	type ClaudeOrchestratorResult,
	type ClaudeOrchestratorRun,
} from "../shared/claude-orchestrator";

type Fetcher = (input: string, init: RequestInit) => Promise<Response>;

const BASE_PATH = "/internal/claude-orchestrator";
const REQUEST_TIMEOUT_MS = 5_000;

const DISABLED_INFO: ClaudeOrchestratorInfo = {
	enabled: false, repository: "", plannerModel: "", workerProvider: "", workerModel: "", workerMode: "model", sandboxed: false, maxActiveRuns: 0,
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

	private async request(method: "GET" | "POST", pathname: string, body?: object): Promise<{ ok: true; body: unknown } | { ok: false; error: ClaudeOrchestratorErrorCode }> {
		const base = this.origin();
		if (!base) return { ok: false, error: "daemon_unavailable" };
		let parsed: URL;
		try {
			parsed = new URL(base);
		} catch {
			return { ok: false, error: "daemon_unavailable" };
		}
		if (parsed.protocol !== "http:" || parsed.hostname !== "127.0.0.1" || parsed.username || parsed.password || parsed.pathname !== "/" || parsed.search || parsed.hash) {
			return { ok: false, error: "daemon_unavailable" };
		}
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
		try {
			const response = await this.fetcher(`${parsed.origin}${pathname}`, {
				method,
				signal: controller.signal,
				headers: body ? { "content-type": "application/json" } : undefined,
				body: body ? JSON.stringify(body) : undefined,
			});
			if (!response.ok) return { ok: false, error: errorForStatus(response.status) };
			return { ok: true, body: await response.json() };
		} catch {
			return { ok: false, error: "daemon_unavailable" };
		} finally {
			clearTimeout(timer);
		}
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
