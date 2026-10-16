import { randomUUID } from "node:crypto";
import { chmod, mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import {
	CLAUDE_ORCHESTRATOR_SECRET_KEYS,
	CLAUDE_ORCHESTRATOR_SETTING_KEYS,
	isClaudeOrchestratorSecretKey,
	isClaudeOrchestratorSettingKey,
	parseClaudeOrchestratorSettingsUpdate,
	type ClaudeOrchestratorSettingsResult,
	type ClaudeOrchestratorSettingsView,
} from "../shared/claude-orchestrator-settings";

const SETTINGS_VERSION = 1;
const MAX_FILE_BYTES = 256 * 1024;

/** Same path the daemon reads (config.ClaudeOrchestratorSettingsPath). */
export function claudeOrchestratorSettingsPath(dataDir: string): string {
	return path.join(dataDir, "claude-orchestrator", "settings.json");
}

async function readValues(file: string): Promise<Record<string, string>> {
	let raw: string;
	try {
		raw = await readFile(file, "utf8");
	} catch (error) {
		if ((error as NodeJS.ErrnoException).code === "ENOENT") return {};
		throw error;
	}
	if (raw.length > MAX_FILE_BYTES) return {};
	try {
		const parsed = JSON.parse(raw) as { version?: unknown; values?: unknown };
		if (parsed.version !== SETTINGS_VERSION || !parsed.values || typeof parsed.values !== "object") return {};
		const values: Record<string, string> = {};
		for (const [key, value] of Object.entries(parsed.values as Record<string, unknown>)) {
			if ((isClaudeOrchestratorSettingKey(key) || isClaudeOrchestratorSecretKey(key)) && typeof value === "string") values[key] = value;
		}
		return values;
	} catch {
		// A damaged file is replaced on the next save; the daemon reports it.
		return {};
	}
}

/**
 * Reads and writes Mission control settings for the main process. The file is
 * owner-only and lives under the AO data dir; API keys never leave this
 * process except into that file.
 */
export class ClaudeOrchestratorSettingsStore {
	constructor(
		private readonly dataDir: () => string,
		private readonly env: NodeJS.ProcessEnv = process.env,
	) {}

	private file(): string {
		return claudeOrchestratorSettingsPath(this.dataDir());
	}

	async get(): Promise<ClaudeOrchestratorSettingsResult<ClaudeOrchestratorSettingsView>> {
		let saved: Record<string, string>;
		try {
			saved = await readValues(this.file());
		} catch {
			return { ok: false, error: "unavailable" };
		}
		const view: ClaudeOrchestratorSettingsView = { values: {}, secretsSet: {}, fromEnvironment: [] };
		for (const key of CLAUDE_ORCHESTRATOR_SETTING_KEYS) {
			if (saved[key] !== undefined) view.values[key] = saved[key];
			if (this.env[key]?.trim()) view.fromEnvironment.push(key);
		}
		for (const key of CLAUDE_ORCHESTRATOR_SECRET_KEYS) {
			view.secretsSet[key] = Boolean(saved[key]?.trim());
			if (this.env[key]?.trim()) view.fromEnvironment.push(key);
		}
		return { ok: true, value: view };
	}

	async save(input: unknown): Promise<ClaudeOrchestratorSettingsResult<ClaudeOrchestratorSettingsView>> {
		const update = parseClaudeOrchestratorSettingsUpdate(input);
		if (!update) return { ok: false, error: "invalid" };
		const file = this.file();
		try {
			const values = await readValues(file);
			for (const [key, value] of Object.entries(update.values)) {
				if (value === "") delete values[key];
				else values[key] = value;
			}
			for (const [key, value] of Object.entries(update.secrets ?? {})) {
				if (value === null) delete values[key];
				else values[key] = value;
			}
			const directory = path.dirname(file);
			await mkdir(directory, { recursive: true, mode: 0o700 });
			const temporary = `${file}.tmp-${process.pid}-${randomUUID()}`;
			try {
				await writeFile(temporary, `${JSON.stringify({ version: SETTINGS_VERSION, values }, null, "\t")}\n`, { mode: 0o600 });
				await chmod(temporary, 0o600);
				await rename(temporary, file);
			} finally {
				await rm(temporary, { force: true });
			}
		} catch {
			return { ok: false, error: "write_failed" };
		}
		return this.get();
	}
}
