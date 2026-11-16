import type { ClaudeOrchestratorSettingsResult, ClaudeOrchestratorSettingsUpdate, ClaudeOrchestratorSettingsView } from "../../shared/claude-orchestrator-settings";
import type {
	ClaudeOrchestratorInfo,
	ClaudeOrchestratorPlan,
	ClaudeOrchestratorResult,
	ClaudeOrchestratorRun,
	ClaudeOrchestratorRunState,
	ClaudeOrchestratorStartInput,
	ClaudeOrchestratorVisual,
} from "../../shared/claude-orchestrator";

/** The bridge surface the renderer depends on (matches preload's shape). */
export type ClaudeOrchestratorBridge = {
	info: () => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorInfo>>;
	list: () => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun[]>>;
	start: (input: ClaudeOrchestratorStartInput) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>>;
	status: (runId: string) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>>;
	cancel: (runId: string) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>>;
	plan: (runId: string) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorPlan>>;
	approve: (runId: string) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>>;
	reject: (runId: string) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>>;
	publish: (runId: string) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorRun>>;
	visual: (runId: string) => Promise<ClaudeOrchestratorResult<ClaudeOrchestratorVisual>>;
	visualShot: (runId: string, name: string) => Promise<ClaudeOrchestratorResult<string>>;
};

const unavailable = async () => ({ ok: false as const, error: "daemon_unavailable" as const });

/** Used outside Electron when no preview data is wanted (tests, plain web). */
export const unavailableClaudeOrchestratorBridge: ClaudeOrchestratorBridge = {
	info: unavailable, list: unavailable, start: unavailable, status: unavailable, cancel: unavailable,
	plan: unavailable, approve: unavailable, reject: unavailable, publish: unavailable,
	visual: unavailable, visualShot: unavailable,
};

// Preview-only stage timing: each stage lasts this long after the run starts.
const PREVIEW_STAGE_MS = 2_500;
const PREVIEW_STAGES: ClaudeOrchestratorRunState[] = ["pending", "planning", "executing", "validating", "reviewing", "completed"];

type PreviewRun = ClaudeOrchestratorRun & { startedAt: number; canceled?: boolean; awaiting?: boolean };

const previewPlan = (runId: string): ClaudeOrchestratorPlan => ({
	runId,
	summary: "Validate signup input on the client and the server, then cover both with tests.",
	steps: [
		{ id: "schema", title: "Add a shared signup schema", instructions: "Create a schema for email, password, and name that both the form and the API can use.", provider: "claude", dependsOn: [] },
		{ id: "form", title: "Validate the signup form", instructions: "Show field errors next to each input and block submit until the form is valid.", provider: "deepseek", dependsOn: ["schema"] },
		{ id: "api", title: "Validate the signup endpoint", instructions: "Reject invalid payloads with a 400 and field-level messages.", provider: "deepseek", dependsOn: ["schema"] },
		{ id: "tests", title: "Cover both with tests", instructions: "Add unit tests for the schema and integration tests for the form and endpoint.", provider: "deepseek", dependsOn: ["form", "api"] },
	],
});

const PREVIEW_VISUAL_RUN = "claude-run-8a2b4c6d8e0f1a3b5c7d9e1f2a4b6c8d";

const previewVisual = (runId: string): ClaudeOrchestratorVisual => ({
	runId,
	rounds: runId !== PREVIEW_VISUAL_RUN ? [] : [
		{
			round: 1, score: 6.4, polished: true, failed: false, findings: 3,
			scores: { hierarchy: 7, typography: 6.5, spacing: 6, colour: 7.5, imagery: 5, consistency: 7, responsiveness: 4, polish: 6, originality: 6.5 },
			summary: "A confident hero and a clear palette, but the phone layout scrolls sideways and the feature cards lose their rhythm below 768px.",
			fixes: [
				"Stack the hero image under the headline on phones and cap it at 320px tall.",
				"Give the feature cards one 24px gap at every size instead of 12px and 40px.",
				"Raise the body text from #8A8A8A to #4A4A4A so it meets 4.5:1 on white.",
			],
			shots: ["desktop", "tablet", "phone"].map((viewport) => ({ page: "index.html", viewport, name: `r1-index-${viewport}.jpg`, findings: viewport === "phone" ? ["The page scrolls sideways: 512px of content on a 390px screen.", "Tap target too small: “Learn more” is 28px tall."] : [] })),
		},
		{
			round: 2, score: 9.1, polished: false, failed: false, findings: 0,
			scores: { hierarchy: 9, typography: 9, spacing: 9, colour: 9.5, imagery: 8.5, consistency: 9, responsiveness: 9.5, polish: 9, originality: 8.5 },
			summary: "All three fixes landed: the hero stacks cleanly on phones, the cards keep one rhythm, and the body text reads well.",
			fixes: [],
			shots: ["desktop", "tablet", "phone"].map((viewport) => ({ page: "index.html", viewport, name: `r2-index-${viewport}.jpg`, findings: [] })),
		},
	],
});

const PREVIEW_SIZES: Record<string, [number, number]> = { desktop: [1440, 2000], tablet: [768, 2400], phone: [390, 2600] };

/** Draws a stand-in page for the preview build; real runs show real screenshots. */
function previewShot(name: string): string | null {
	const match = /^r(\d)-index-(desktop|tablet|phone)\.jpg$/.exec(name);
	if (!match || typeof document === "undefined") return null;
	const [width, height] = PREVIEW_SIZES[match[2]];
	const canvas = document.createElement("canvas");
	canvas.width = width;
	canvas.height = height;
	const context = canvas.getContext("2d");
	if (!context) return null;
	const first = match[1] === "1";
	const gutter = Math.round(width * 0.06);
	context.fillStyle = "#f7f5f2";
	context.fillRect(0, 0, width, height);
	context.fillStyle = "#1d2a3a";
	context.fillRect(0, 0, width, 72);
	context.fillStyle = "#e8553d";
	context.fillRect(gutter, 26, 96, 20);
	const stacked = width < 800;
	const heroWidth = stacked ? width - gutter * 2 : (width - gutter * 3) / 2;
	context.fillStyle = "#1d2a3a";
	context.fillRect(gutter, 140, heroWidth * 0.9, 56);
	context.fillRect(gutter, 212, heroWidth * 0.7, 56);
	context.fillStyle = first ? "#b9b9b9" : "#4a4a4a";
	for (let line = 0; line < 3; line++) context.fillRect(gutter, 300 + line * 30, heroWidth * (0.95 - line * 0.1), 14);
	context.fillStyle = "#e8553d";
	context.fillRect(gutter, 420, 180, first && stacked ? 28 : 48);
	context.fillStyle = "#cfd8e3";
	const imageTop = stacked ? 520 : 140;
	const imageLeft = stacked ? gutter : gutter * 2 + heroWidth;
	context.fillRect(imageLeft, imageTop, first && stacked ? width * 1.3 : heroWidth, stacked && !first ? 320 : 380);
	const columns = width >= 1200 ? 3 : width >= 700 ? 2 : 1;
	const gap = first ? (columns === 1 ? 12 : 40) : 24;
	const cardWidth = (width - gutter * 2 - gap * (columns - 1)) / columns;
	let top = stacked ? 900 : 640;
	for (let card = 0; card < 6; card++) {
		const column = card % columns;
		if (column === 0 && card > 0) top += 260 + gap;
		context.fillStyle = "#ffffff";
		context.fillRect(gutter + column * (cardWidth + gap), top, cardWidth, 260);
		context.fillStyle = "#e8553d";
		context.fillRect(gutter + column * (cardWidth + gap) + 24, top + 24, 40, 40);
		context.fillStyle = first ? "#b9b9b9" : "#4a4a4a";
		context.fillRect(gutter + column * (cardWidth + gap) + 24, top + 96, cardWidth * 0.6, 14);
	}
	context.fillStyle = "#1d2a3a";
	context.fillRect(0, height - 160, width, 160);
	return canvas.toDataURL("image/jpeg", 0.8);
}

/**
 * In-memory stand-in used only by the browser preview build (dev:web), which
 * has no Electron bridge or daemon. The packaged app never uses it.
 */
export function createClaudeOrchestratorPreview(now: () => number = Date.now): ClaudeOrchestratorBridge {
	const base = now();
	const runs: PreviewRun[] = [
		{ runId: "claude-run-3f1c0a9b8e7d6c5b4a39281706f5e4d3", state: "held", title: "Migrate the settings form to the shared select primitive", branch: "ao/claude-orchestrator/claude-run-3f1c0a9b8e7d6c5b4a39281706f5e4d3", commit: "9c41e07b2d", recommendation: "hold", replans: 1, usage: { inputTokens: 184_200, outputTokens: 31_900, calls: 14, costUsd: 1.03, budgetTokens: 2_000_000 }, createdAt: new Date(base - 26 * 60_000).toISOString(), updatedAt: new Date(base - 19 * 60_000).toISOString(), startedAt: 0 },
		{ runId: "claude-run-8a2b4c6d8e0f1a3b5c7d9e1f2a4b6c8d", state: "completed", title: "Add retry with backoff to the GitHub client and cover it with tests", branch: "ao/claude-orchestrator/claude-run-8a2b4c6d8e0f1a3b5c7d9e1f2a4b6c8d", commit: "4e7a1c2f90", recommendation: "merge", usage: { inputTokens: 96_400, outputTokens: 18_250, calls: 8, costUsd: 0.56, budgetTokens: 2_000_000 }, pullRequestUrl: "https://github.com/robotsuperx-create/claude-led-orchestrator-mvp/pull/12", createdAt: new Date(base - 95 * 60_000).toISOString(), updatedAt: new Date(base - 81 * 60_000).toISOString(), startedAt: 0 },
		{ runId: "claude-run-1b3d5f7a9c2e4a6c8e0b2d4f6a8c0e2a", state: "failed", title: "Upgrade the date library across the renderer", usage: { inputTokens: 2_000_400, outputTokens: 12_100, calls: 21, costUsd: 6.18, budgetTokens: 2_000_000, exhausted: true }, createdAt: new Date(base - 3 * 3_600_000).toISOString(), updatedAt: new Date(base - 3 * 3_600_000 + 4 * 60_000).toISOString(), startedAt: 0 },
	];
	const snapshot = (run: PreviewRun): ClaudeOrchestratorRun => {
		if (run.awaiting && now() - run.startedAt >= PREVIEW_STAGE_MS) {
			run.state = "awaiting_approval";
		}
		if (run.startedAt > 0 && !run.canceled && !run.awaiting) {
			const index = Math.min(PREVIEW_STAGES.length - 1, Math.floor((now() - run.startedAt) / PREVIEW_STAGE_MS));
			run.state = PREVIEW_STAGES[index];
			run.updatedAt = new Date(run.startedAt + index * PREVIEW_STAGE_MS).toISOString();
			if (run.state === "completed") {
				run.branch = `ao/claude-orchestrator/${run.runId}`;
				run.commit ??= run.runId.slice(-10);
				run.recommendation = "merge";
			}
			const elapsed = Math.max(0, now() - run.startedAt);
			const calls = Math.min(8, 1 + Math.floor(elapsed / PREVIEW_STAGE_MS));
			run.usage = { inputTokens: calls * 11_800, outputTokens: calls * 2_300, calls, costUsd: Math.round(calls * 7) / 100, budgetTokens: 2_000_000 };
		}
		const { startedAt: _startedAt, canceled: _canceled, awaiting: _awaiting, ...visible } = run;
		return { ...visible };
	};
	const find = (runId: string) => runs.find((run) => run.runId === runId);
	return {
		info: async () => ({ ok: true, value: { enabled: true, repository: "agent-orchestrator", plannerModel: "claude-opus-5-5", workerProvider: "deepseek", workerModel: "deepseek-chat", workerMode: "model", sandboxed: true, maxActiveRuns: 2, plannerProvider: "claude", budgetTokens: 2_000_000, budgetUsd: 0, maxReplans: 1, canPublish: true, visualReview: true } }),
		list: async () => ({ ok: true, value: runs.map(snapshot) }),
		start: async (input) => {
			const runId = `claude-run-${Array.from({ length: 32 }, () => Math.floor(Math.random() * 16).toString(16)).join("")}`;
			const started = now();
			const run: PreviewRun = { runId, state: input.approvePlan ? "planning" : "pending", title: input.task.trim().split(/\s+/).join(" ").slice(0, 160), createdAt: new Date(started).toISOString(), updatedAt: new Date(started).toISOString(), startedAt: started, awaiting: input.approvePlan === true };
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
			run.awaiting = false;
			run.state = "canceled";
			run.updatedAt = new Date(now()).toISOString();
			return { ok: true, value: snapshot(run) };
		},
		plan: async (runId) => {
			const run = find(runId);
			if (!run) return { ok: false, error: "not_found" };
			snapshot(run);
			return run.state === "planning" || run.state === "pending" ? { ok: false, error: "plan_not_ready" } : { ok: true, value: previewPlan(runId) };
		},
		approve: async (runId) => {
			const run = find(runId);
			if (!run || run.state !== "awaiting_approval") return { ok: false, error: "already_finished" };
			// Continue through the remaining stages from "planning".
			run.awaiting = false;
			run.startedAt = now() - PREVIEW_STAGE_MS;
			return { ok: true, value: snapshot(run) };
		},
		publish: async (runId) => {
			const run = find(runId);
			if (!run) return { ok: false, error: "not_found" };
			snapshot(run);
			if ((run.state !== "completed" && run.state !== "held") || !run.commit) return { ok: false, error: "no_changes" };
			run.pullRequestUrl ??= `https://github.com/robotsuperx-create/claude-led-orchestrator-mvp/pull/${13 + runs.indexOf(run)}`;
			return { ok: true, value: snapshot(run) };
		},
		visual: async (runId) => (find(runId) ? { ok: true, value: previewVisual(runId) } : { ok: false, error: "not_found" }),
		visualShot: async (runId, name) => {
			const picture = runId === PREVIEW_VISUAL_RUN ? previewShot(name) : null;
			return picture ? { ok: true, value: picture } : { ok: false, error: "not_found" };
		},
		reject: async (runId) => {
			const run = find(runId);
			if (!run || run.state !== "awaiting_approval") return { ok: false, error: "already_finished" };
			run.awaiting = false;
			run.canceled = true;
			run.state = "canceled";
			run.updatedAt = new Date(now()).toISOString();
			return { ok: true, value: snapshot(run) };
		},
	};
}

/** The settings bridge surface (matches preload's shape). */
export type ClaudeOrchestratorSettingsBridge = {
	get: () => Promise<ClaudeOrchestratorSettingsResult<ClaudeOrchestratorSettingsView>>;
	save: (update: ClaudeOrchestratorSettingsUpdate) => Promise<ClaudeOrchestratorSettingsResult<ClaudeOrchestratorSettingsView>>;
};

export const unavailableClaudeOrchestratorSettingsBridge: ClaudeOrchestratorSettingsBridge = {
	get: async () => ({ ok: false, error: "unavailable" }),
	save: async () => ({ ok: false, error: "unavailable" }),
};

/** In-memory settings for the browser preview build only. */
export function createClaudeOrchestratorSettingsPreview(): ClaudeOrchestratorSettingsBridge {
	const values: ClaudeOrchestratorSettingsView["values"] = {
		AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED: "on",
		AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT: "/demo/agent-orchestrator",
		AO_CLAUDE_ORCHESTRATOR_CLAUDE_BASE_URL: "https://api.anthropic.com/v1",
		AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "claude-opus-5-5",
		AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_BASE_URL: "https://api.deepseek.com/v1",
		AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_MODEL: "deepseek-chat",
		AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS: JSON.stringify([{ name: "go-1", argv: ["go", "test", "./..."] }]),
		AO_CLAUDE_ORCHESTRATOR_VALIDATOR_COMMANDS: JSON.stringify([{ name: "go-1", argv: ["go", "vet", "./..."] }, { name: "go-2", argv: ["go", "test", "./..."] }]),
	};
	const secretsSet: ClaudeOrchestratorSettingsView["secretsSet"] = { AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: true };
	const view = (): ClaudeOrchestratorSettingsView => ({ values: { ...values }, secretsSet: { ...secretsSet }, fromEnvironment: [] });
	return {
		get: async () => ({ ok: true, value: view() }),
		save: async (update) => {
			for (const [key, value] of Object.entries(update.values) as Array<[keyof typeof values, string]>) {
				if (value === "") delete values[key];
				else values[key] = value;
			}
			for (const [key, value] of Object.entries(update.secrets ?? {}) as Array<[keyof typeof secretsSet, string | null]>) {
				secretsSet[key] = value !== null;
			}
			return { ok: true, value: view() };
		},
	};
}
