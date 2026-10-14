// Mission control settings saved by the desktop app. The main process writes
// them to <data dir>/claude-orchestrator/settings.json (owner-only); the
// daemon reads that file at start, and environment variables still win.
// API keys go into the file but never come back to the renderer: it only
// learns whether each one is set.

export const CLAUDE_ORCHESTRATOR_SETTING_KEYS = [
	"AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED",
	"AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT",
	"AO_CLAUDE_ORCHESTRATOR_PLANNER_PROVIDER",
	"AO_CLAUDE_ORCHESTRATOR_WORKER_MODE",
	"AO_CLAUDE_ORCHESTRATOR_WORKER_DEFAULT_PROVIDER",
	"AO_CLAUDE_ORCHESTRATOR_CLAUDE_BASE_URL",
	"AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL",
	"AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_BASE_URL",
	"AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_MODEL",
	"AO_CLAUDE_ORCHESTRATOR_LOCAL_BASE_URL",
	"AO_CLAUDE_ORCHESTRATOR_LOCAL_MODEL",
	"AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS",
	"AO_CLAUDE_ORCHESTRATOR_VALIDATOR_COMMANDS",
	"AO_CLAUDE_ORCHESTRATOR_WORKER_TIMEOUT",
	"AO_CLAUDE_ORCHESTRATOR_VALIDATOR_TIMEOUT",
	"AO_CLAUDE_ORCHESTRATOR_RUN_TOKEN_BUDGET",
	"AO_CLAUDE_ORCHESTRATOR_MAX_REPLANS",
	"AO_CLAUDE_ORCHESTRATOR_MAX_PARALLEL_SUBTASKS",
	"AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_ENABLED",
	"AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_IMAGE",
	"AO_CLAUDE_ORCHESTRATOR_CLAUDE_PRICE_INPUT_PER_MTOK",
	"AO_CLAUDE_ORCHESTRATOR_CLAUDE_PRICE_OUTPUT_PER_MTOK",
	"AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_PRICE_INPUT_PER_MTOK",
	"AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_PRICE_OUTPUT_PER_MTOK",
] as const;
export type ClaudeOrchestratorSettingKey = (typeof CLAUDE_ORCHESTRATOR_SETTING_KEYS)[number];

export const CLAUDE_ORCHESTRATOR_SECRET_KEYS = [
	"AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY",
	"AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY",
	"AO_CLAUDE_ORCHESTRATOR_LOCAL_API_KEY",
] as const;
export type ClaudeOrchestratorSecretKey = (typeof CLAUDE_ORCHESTRATOR_SECRET_KEYS)[number];

/** What the renderer may see: plain values, and only whether each key is set. */
export type ClaudeOrchestratorSettingsView = {
	values: Partial<Record<ClaudeOrchestratorSettingKey, string>>;
	secretsSet: Partial<Record<ClaudeOrchestratorSecretKey, boolean>>;
	/** Keys set in the environment; those override what is saved here. */
	fromEnvironment: Array<ClaudeOrchestratorSettingKey | ClaudeOrchestratorSecretKey>;
};

/**
 * A save. Values replace the saved ones (an empty string removes one). For a
 * secret, a string sets it, null removes it, and leaving it out keeps it.
 */
export type ClaudeOrchestratorSettingsUpdate = {
	values: Partial<Record<ClaudeOrchestratorSettingKey, string>>;
	secrets?: Partial<Record<ClaudeOrchestratorSecretKey, string | null>>;
};

export type ClaudeOrchestratorSettingsResult<T> =
	| { ok: true; value: T }
	| { ok: false; error: "invalid" | "write_failed" | "unavailable" };

const MAX_VALUE_LENGTH = 16 * 1024;
const MULTILINE_KEYS = new Set<string>(["AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS", "AO_CLAUDE_ORCHESTRATOR_VALIDATOR_COMMANDS"]);

export function isClaudeOrchestratorSettingKey(key: string): key is ClaudeOrchestratorSettingKey {
	return (CLAUDE_ORCHESTRATOR_SETTING_KEYS as readonly string[]).includes(key);
}

export function isClaudeOrchestratorSecretKey(key: string): key is ClaudeOrchestratorSecretKey {
	return (CLAUDE_ORCHESTRATOR_SECRET_KEYS as readonly string[]).includes(key);
}

function validValue(key: string, value: unknown): value is string {
	if (typeof value !== "string" || value.length > MAX_VALUE_LENGTH || value.includes("\u0000")) return false;
	return MULTILINE_KEYS.has(key) || !/[\r\n]/.test(value);
}

/** Rebuilds an update from allowlisted keys only; null when anything is off. */
export function parseClaudeOrchestratorSettingsUpdate(input: unknown): ClaudeOrchestratorSettingsUpdate | null {
	if (!input || typeof input !== "object" || Array.isArray(input)) return null;
	const candidate = input as Record<string, unknown>;
	if (Object.keys(candidate).some((key) => key !== "values" && key !== "secrets")) return null;
	const rawValues = candidate.values;
	if (!rawValues || typeof rawValues !== "object" || Array.isArray(rawValues)) return null;
	const values: ClaudeOrchestratorSettingsUpdate["values"] = {};
	for (const [key, value] of Object.entries(rawValues)) {
		if (!isClaudeOrchestratorSettingKey(key) || !validValue(key, value)) return null;
		values[key] = value.trim();
	}
	const update: ClaudeOrchestratorSettingsUpdate = { values };
	if (candidate.secrets !== undefined) {
		const rawSecrets = candidate.secrets;
		if (!rawSecrets || typeof rawSecrets !== "object" || Array.isArray(rawSecrets)) return null;
		const secrets: NonNullable<ClaudeOrchestratorSettingsUpdate["secrets"]> = {};
		for (const [key, value] of Object.entries(rawSecrets)) {
			if (!isClaudeOrchestratorSecretKey(key)) return null;
			if (value === null) {
				secrets[key] = null;
				continue;
			}
			if (!validValue(key, value) || value.trim() === "") return null;
			secrets[key] = value.trim();
		}
		update.secrets = secrets;
	}
	return update;
}

/** One configured check or worker command, run without a shell. */
export type ClaudeOrchestratorCommand = { name: string; argv: string[] };

/**
 * Splits one command line into argv the way a person would type it: spaces
 * separate words, and single or double quotes keep spaces inside a word.
 * Returns null for an unterminated quote.
 */
export function splitCommandLine(line: string): string[] | null {
	const argv: string[] = [];
	let current = "";
	let quote: "'" | "\"" | null = null;
	let started = false;
	for (const char of line) {
		if (quote) {
			if (char === quote) quote = null;
			else current += char;
			continue;
		}
		if (char === "'" || char === "\"") {
			quote = char;
			started = true;
			continue;
		}
		if (/\s/.test(char)) {
			if (started) argv.push(current);
			current = "";
			started = false;
			continue;
		}
		current += char;
		started = true;
	}
	if (quote) return null;
	if (started) argv.push(current);
	return argv;
}

/** Turns one command per line into the daemon's JSON argv list. */
export function commandsFromText(text: string): { json: string; error: null } | { json: null; error: "unterminated_quote" } {
	const commands: ClaudeOrchestratorCommand[] = [];
	for (const line of text.split(/\r?\n/)) {
		if (line.trim() === "") continue;
		const argv = splitCommandLine(line.trim());
		if (!argv) return { json: null, error: "unterminated_quote" };
		if (argv.length === 0) continue;
		const base = argv[0].split(/[\\/]/).pop() || "command";
		commands.push({ name: `${base}-${commands.length + 1}`, argv });
	}
	return { json: commands.length ? JSON.stringify(commands) : "", error: null };
}

function quoteWord(word: string): string {
	if (word !== "" && !/[\s'"]/.test(word)) return word;
	return word.includes("\"") ? `'${word}'` : `"${word}"`;
}

/** Shows a saved JSON argv list as one command per line. */
export function commandsToText(json: string | undefined): string {
	if (!json?.trim()) return "";
	try {
		const commands = JSON.parse(json) as unknown;
		if (!Array.isArray(commands)) return "";
		return commands
			.map((command) => (command && typeof command === "object" && Array.isArray((command as ClaudeOrchestratorCommand).argv)
				? (command as ClaudeOrchestratorCommand).argv.filter((word) => typeof word === "string").map(quoteWord).join(" ")
				: ""))
			.filter(Boolean)
			.join("\n");
	} catch {
		return "";
	}
}
