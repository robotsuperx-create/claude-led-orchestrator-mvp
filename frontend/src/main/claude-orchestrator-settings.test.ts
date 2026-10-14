import { mkdtemp, readFile, rm, stat, writeFile, mkdir } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ClaudeOrchestratorSettingsStore, claudeOrchestratorSettingsPath } from "./claude-orchestrator-settings";

let dataDir: string;

beforeEach(async () => {
	dataDir = await mkdtemp(path.join(os.tmpdir(), "xr-settings-"));
});
afterEach(async () => {
	await rm(dataDir, { recursive: true, force: true });
});

describe("ClaudeOrchestratorSettingsStore", () => {
	it("saves owner-only and never hands API keys back", async () => {
		const store = new ClaudeOrchestratorSettingsStore(() => dataDir, {});
		const saved = await store.save({
			values: { AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED: "on", AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "claude-opus-5-5" },
			secrets: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: "sk-very-secret" },
		});
		expect(saved.ok).toBe(true);
		if (!saved.ok) return;
		expect(saved.value.values.AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL).toBe("claude-opus-5-5");
		expect(saved.value.secretsSet.AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY).toBe(true);
		expect(JSON.stringify(saved.value)).not.toContain("sk-very-secret");

		const file = claudeOrchestratorSettingsPath(dataDir);
		const onDisk = JSON.parse(await readFile(file, "utf8"));
		expect(onDisk).toEqual({ version: 1, values: {
			AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED: "on",
			AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "claude-opus-5-5",
			AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: "sk-very-secret",
		} });
		if (process.platform !== "win32") {
			expect((await stat(file)).mode & 0o777).toBe(0o600);
			expect((await stat(path.dirname(file))).mode & 0o777).toBe(0o700);
		}
	});

	it("keeps an omitted key, removes a null one, and drops emptied values", async () => {
		const store = new ClaudeOrchestratorSettingsStore(() => dataDir, {});
		await store.save({
			values: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "m", AO_CLAUDE_ORCHESTRATOR_LOCAL_MODEL: "l" },
			secrets: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: "a", AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY: "b" },
		});
		const next = await store.save({
			values: { AO_CLAUDE_ORCHESTRATOR_LOCAL_MODEL: "" },
			secrets: { AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY: null },
		});
		expect(next.ok && next.value.secretsSet).toEqual({
			AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: true,
			AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY: false,
			AO_CLAUDE_ORCHESTRATOR_LOCAL_API_KEY: false,
		});
		expect(next.ok && next.value.values).toEqual({ AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "m" });
	});

	it("reports which settings the environment overrides", async () => {
		const store = new ClaudeOrchestratorSettingsStore(() => dataDir, {
			AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: "from-env",
			AO_CLAUDE_ORCHESTRATOR_WORKER_MODE: "agents",
			AO_CLAUDE_ORCHESTRATOR_LOCAL_MODEL: "  ",
		});
		const view = await store.get();
		expect(view.ok && view.value.fromEnvironment.sort()).toEqual(["AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY", "AO_CLAUDE_ORCHESTRATOR_WORKER_MODE"]);
		expect(JSON.stringify(view)).not.toContain("from-env");
	});

	it("rejects anything outside the allowlist and survives a damaged file", async () => {
		const store = new ClaudeOrchestratorSettingsStore(() => dataDir, {});
		expect(await store.save({ values: { AO_DATA_DIR: "/" } })).toEqual({ ok: false, error: "invalid" });
		const file = claudeOrchestratorSettingsPath(dataDir);
		await mkdir(path.dirname(file), { recursive: true });
		await writeFile(file, "{broken");
		const view = await store.get();
		expect(view.ok && view.value.values).toEqual({});
		expect((await store.save({ values: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "m" } })).ok).toBe(true);
	});
});
