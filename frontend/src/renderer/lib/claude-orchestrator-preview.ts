import type {
	ClaudeOrchestratorInfo,
	ClaudeOrchestratorResult,
	ClaudeOrchestratorRun,
	ClaudeOrchestratorRunState,
	ClaudeOrchestratorStartInput,
} from "../../shared/claude-orchestrator";

/** The bridge surface the renderer depends on (matches preload's shape). */
export type ClaudeOrchestratorBridge = {
	info: () => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorInfo>>;
	list: () => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun[]>>;
	start: (input: ClaudeOrchestratorStartInput) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>>;
	status: (runId: string) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>>;
	cancel: (runId: string) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>>;
};

const unavailable = async () => ({ ok: false as const, error: "daemon_unavailable" as const });

/** Used outside Electron when no preview data is wanted (tests, plain web). */
export const unavailableClaudeOrchestratorBridge: ClaudeOrchestratorBridge = {
	info: unavailable, list: unavailable, start: unavailable, status: unavailable, cancel: unavailable,
};

// Preview-only stage timing: each stage lasts this long after the run starts.
const PREVIEW_STAGE_MS = 2_500;
const PREVIEW_STAGES: ClaudeOrchestratorRunState[] = ["pending", "planning", "executing", "validating", "reviewing", "completed"];

type PreviewRun = ClaudeOrchestratorRun & { startedAt: number; canceled?: boolean };

/**
 * In-memory stand-in used only by the browser preview build (dev:web), which
 * has no Electron bridge or daemon. The packaged app never uses it.
 */
export function createClaudeOrchestratorPreview(now: () => number = Date.now): ClaudeOrchestratorBridge {
	const base = now();
	const runs: PreviewRun[] = [
		{ runId: "claude-run-3f1c0a9b8e7d6c5b4a39281706f5e4d3", state: "held", title: "Migrate the settings form to the shared select primitive", branch: "ao/claude-orchestrator/claude-run-3f1c0a9b8e7d6c5b4a39281706f5e4d3", commit: "9c41e07b2d", recommendation: "hold", createdAt: new Date(base - 26 * 60_000).toISOString(), updatedAt: new Date(base - 19 * 60_000).toISOString(), startedAt: 0 },
		{ runId: "claude-run-8a2b4c6d8e0f1a3b5c7d9e1f2a4b6c8d", state: "completed", title: "Add retry with backoff to the GitHub client and cover it with tests", branch: "ao/claude-orchestrator/claude-run-8a2b4c6d8e0f1a3b5c7d9e1f2a4b6c8d", commit: "4e7a1c2f90", recommendation: "merge", createdAt: new Date(base - 95 * 60_000).toISOString(), updatedAt: new Date(base - 81 * 60_000).toISOString(), startedAt: 0 },
		{ runId: "claude-run-1b3d5f7a9c2e4a6c8e0b2d4f6a8c0e2a", state: "failed", title: "Upgrade the date library across the renderer", createdAt: new Date(base - 3 * 3_600_000).toISOString(), updatedAt: new Date(base - 3 * 3_600_000 + 4 * 60_000).toISOString(), startedAt: 0 },
	];
	const snapshot = (run: PreviewRun): ClaudeOrchestratorRun => {
		if (run.startedAt > 0 && !run.canceled) {
			const index = Math.min(PREVIEW_STAGES.length - 1, Math.floor((now() - run.startedAt) / PREVIEW_STAGE_MS));
			run.state = PREVIEW_STAGES[index];
			run.updatedAt = new Date(run.startedAt + index * PREVIEW_STAGE_MS).toISOString();
			if (run.state === "completed") {
				run.branch = `ao/claude-orchestrator/${run.runId}`;
				run.commit ??= run.runId.slice(-10);
				run.recommendation = "merge";
			}
		}
		const { startedAt: _startedAt, canceled: _canceled, ...visible } = run;
		return { ...visible };
	};
	const find = (runId: string) => runs.find((run) => run.runId === runId);
	return {
		info: async () => ({ ok: true, value: { enabled: true, repository: "agent-orchestrator", plannerModel: "claude-opus-5-5", workerProvider: "deepseek", workerModel: "deepseek-chat", sandboxed: true, maxActiveRuns: 2 } }),
		list: async () => ({ ok: true, value: runs.map(snapshot) }),
		start: async (input) => {
			const runId = `claude-run-${Array.from({ length: 32 }, () => Math.floor(Math.random() * 16).toString(16)).join("")}`;
			const started = now();
			const run: PreviewRun = { runId, state: "pending", title: input.task.trim().split(/\s+/).join(" ").slice(0, 160), createdAt: new Date(started).toISOString(), updatedAt: new Date(started).toISOString(), startedAt: started };
			runs.unshift(run);
			return { ok: true, value: snapshot(run) };
		},
		status: async (runId) => {
			const run = find(runId);
			return run ? { ok: true, value: snapshot(run) } : { ok: false, error: "not_found" };
		},
		cancel: async (runId) => {
			const run = find(runId);
			if (!run) return { ok: false, error: "not_found" };
			snapshot(run);
			if (["completed", "held", "failed", "canceled"].includes(run.state)) return { ok: false, error: "already_finished" };
			run.canceled = true;
			run.state = "canceled";
			run.updatedAt = new Date(now()).toISOString();
			return { ok: true, value: snapshot(run) };
		},
	};
}
