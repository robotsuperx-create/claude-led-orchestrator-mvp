import { useEffect, useMemo, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { Check, FolderOpen, LoaderCircle, TriangleAlert } from "lucide-react";
import {
	commandsFromText,
	commandsToText,
	type ClaudeOrchestratorSecretKey,
	type ClaudeOrchestratorSettingKey,
	type ClaudeOrchestratorSettingsUpdate,
	type ClaudeOrchestratorSettingsView,
} from "../../../shared/claude-orchestrator-settings";
import { claudeOrchestratorQueryKeys } from "../../hooks/useClaudeOrchestrator";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Switch } from "../ui/switch";
import { SettingsOptionMenu, type SettingsOption } from "./SettingsOptionMenu";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

type Key = ClaudeOrchestratorSettingKey;
type SecretKey = ClaudeOrchestratorSecretKey;

// Technical defaults shown as placeholders; they are not translatable copy.
const PLACEHOLDERS: Partial<Record<Key, string>> = {
	AO_CLAUDE_ORCHESTRATOR_CLAUDE_BASE_URL: "https://api.anthropic.com/v1",
	AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "claude-opus-5-5",
	AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_BASE_URL: "https://api.deepseek.com/v1",
	AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_MODEL: "deepseek-chat",
	AO_CLAUDE_ORCHESTRATOR_LOCAL_BASE_URL: "http://127.0.0.1:11434/v1",
	AO_CLAUDE_ORCHESTRATOR_LOCAL_MODEL: "qwen2.5-coder:14b",
	AO_CLAUDE_ORCHESTRATOR_WORKER_TIMEOUT: "30m",
	AO_CLAUDE_ORCHESTRATOR_VALIDATOR_TIMEOUT: "10m",
	AO_CLAUDE_ORCHESTRATOR_RUN_TOKEN_BUDGET: "2000000",
	AO_CLAUDE_ORCHESTRATOR_MAX_PARALLEL_SUBTASKS: "3",
	AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_IMAGE: "ghcr.io/…/orchestrator-worker",
	AO_CLAUDE_ORCHESTRATOR_CLAUDE_PRICE_INPUT_PER_MTOK: "3",
	AO_CLAUDE_ORCHESTRATOR_CLAUDE_PRICE_OUTPUT_PER_MTOK: "15",
};
const COMMAND_PLACEHOLDER = "npm ci\nnpm test";
const DURATION = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;
const WHOLE = /^\d+$/;
const DECIMAL = /^\d+(\.\d+)?$/;

type Draft = Partial<Record<Key, string>>;
type FieldError = "required" | "duration" | "number" | "url" | "quote";

function isOn(value: string | undefined) {
	return ["on", "true", "1", "yes"].includes((value ?? "").trim().toLowerCase());
}

/** Client-side checks so a save never leaves Mission control half set up. */
function validate(draft: Draft, commands: { worker: string; validator: string }, secretsSet: (key: SecretKey) => boolean): Partial<Record<Key | "WORKER_COMMANDS" | "VALIDATOR_COMMANDS" | SecretKey, FieldError>> {
	const errors: ReturnType<typeof validate> = {};
	const value = (key: Key) => (draft[key] ?? "").trim();
	for (const key of ["AO_CLAUDE_ORCHESTRATOR_WORKER_TIMEOUT", "AO_CLAUDE_ORCHESTRATOR_VALIDATOR_TIMEOUT"] as const) {
		if (value(key) && !DURATION.test(value(key))) errors[key] = "duration";
	}
	for (const key of ["AO_CLAUDE_ORCHESTRATOR_RUN_TOKEN_BUDGET", "AO_CLAUDE_ORCHESTRATOR_MAX_PARALLEL_SUBTASKS"] as const) {
		if (value(key) && !WHOLE.test(value(key))) errors[key] = "number";
	}
	for (const key of [
		"AO_CLAUDE_ORCHESTRATOR_CLAUDE_PRICE_INPUT_PER_MTOK", "AO_CLAUDE_ORCHESTRATOR_CLAUDE_PRICE_OUTPUT_PER_MTOK",
		"AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_PRICE_INPUT_PER_MTOK", "AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_PRICE_OUTPUT_PER_MTOK",
	] as const) {
		if (value(key) && !DECIMAL.test(value(key))) errors[key] = "number";
	}
	for (const key of ["AO_CLAUDE_ORCHESTRATOR_CLAUDE_BASE_URL", "AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_BASE_URL", "AO_CLAUDE_ORCHESTRATOR_LOCAL_BASE_URL"] as const) {
		if (value(key) && !/^https?:\/\/\S+$/.test(value(key))) errors[key] = "url";
	}
	if (commandsFromText(commands.worker).error) errors.WORKER_COMMANDS = "quote";
	if (commandsFromText(commands.validator).error) errors.VALIDATOR_COMMANDS = "quote";
	if (!isOn(draft.AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED)) return errors;

	// Turning it on needs a project, a planner, a writer in model mode, and checks.
	if (!value("AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT")) errors.AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT = "required";
	const needs = new Set<string>([value("AO_CLAUDE_ORCHESTRATOR_PLANNER_PROVIDER") || "claude"]);
	if ((value("AO_CLAUDE_ORCHESTRATOR_WORKER_MODE") || "model") === "model") needs.add(value("AO_CLAUDE_ORCHESTRATOR_WORKER_DEFAULT_PROVIDER") || "deepseek");
	const providerKeys = {
		claude: ["AO_CLAUDE_ORCHESTRATOR_CLAUDE_BASE_URL", "AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL"],
		deepseek: ["AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_BASE_URL", "AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_MODEL"],
		local: ["AO_CLAUDE_ORCHESTRATOR_LOCAL_BASE_URL", "AO_CLAUDE_ORCHESTRATOR_LOCAL_MODEL"],
	} as const;
	for (const provider of needs) {
		for (const key of providerKeys[provider as keyof typeof providerKeys] ?? []) {
			if (!value(key)) errors[key] ??= "required";
		}
	}
	if (needs.has("claude") && !secretsSet("AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY")) errors.AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY = "required";
	if (needs.has("deepseek") && !secretsSet("AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY")) errors.AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY = "required";
	if (!commands.worker.trim()) errors.WORKER_COMMANDS ??= "required";
	if (!commands.validator.trim()) errors.VALIDATOR_COMMANDS ??= "required";
	return errors;
}

/**
 * Mission control's own settings: who plans, who writes the code, which checks
 * run, and the limits. Saving restarts the daemon, which reads them at start.
 * Values set in the environment win and are shown read-only here.
 */
export function MissionControlSettingsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const client = useQueryClient();
	const [view, setView] = useState<ClaudeOrchestratorSettingsView | null>(null);
	const [loadFailed, setLoadFailed] = useState(false);
	const [draft, setDraft] = useState<Draft>({});
	const [workerCommands, setWorkerCommands] = useState("");
	const [validatorCommands, setValidatorCommands] = useState("");
	const [secrets, setSecrets] = useState<Partial<Record<SecretKey, string | null>>>({});
	const [status, setStatus] = useState<"idle" | "saving" | "saved" | "failed">("idle");
	const [showErrors, setShowErrors] = useState(false);

	const adopt = (next: ClaudeOrchestratorSettingsView) => {
		setView(next);
		setDraft({ ...next.values });
		setWorkerCommands(commandsToText(next.values.AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS));
		setValidatorCommands(commandsToText(next.values.AO_CLAUDE_ORCHESTRATOR_VALIDATOR_COMMANDS));
		setSecrets({});
	};

	useEffect(() => {
		let active = true;
		const bridge = aoBridge.claudeOrchestratorSettings;
		if (!bridge) return;
		void bridge.get().then((result) => {
			if (!active) return;
			if (result.ok) adopt(result.value);
			else setLoadFailed(true);
		});
		return () => {
			active = false;
		};
	}, []);

	const fromEnv = useMemo(() => new Set<string>(view?.fromEnvironment ?? []), [view]);
	const secretSet = (key: SecretKey) => secrets[key] === null ? false : Boolean(secrets[key]?.trim()) || Boolean(view?.secretsSet[key]) || fromEnv.has(key);
	const errors = validate(draft, { worker: workerCommands, validator: validatorCommands }, secretSet);
	const hasErrors = Object.keys(errors).length > 0;
	const set = (key: Key, value: string) => {
		setDraft((current) => ({ ...current, [key]: value }));
		setStatus("idle");
	};

	// Older or partial bridges (tests, plain web) carry no settings surface.
	if (!aoBridge.claudeOrchestratorSettings) return null;
	if (loadFailed) {
		return (
			<SettingsSection title={t("settings.missionControl")} titleHidden={titleHidden}>
				<p role="alert" className="px-3 text-sm text-error">{t("settings.missionControl.loadFailed")}</p>
			</SettingsSection>
		);
	}
	if (!view) {
		return (
			<SettingsSection title={t("settings.missionControl")} titleHidden={titleHidden}>
				<p role="status" className="px-3 text-sm text-settings-muted">{t("settings.missionControl.loading")}</p>
			</SettingsSection>
		);
	}

	const save = async () => {
		setShowErrors(true);
		if (hasErrors) return;
		const worker = commandsFromText(workerCommands);
		const validator = commandsFromText(validatorCommands);
		const values: ClaudeOrchestratorSettingsUpdate["values"] = { ...draft };
		values.AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS = worker.json ?? "";
		values.AO_CLAUDE_ORCHESTRATOR_VALIDATOR_COMMANDS = validator.json ?? "";
		for (const key of Object.keys(values) as Key[]) {
			if (fromEnv.has(key)) delete values[key];
		}
		const pendingSecrets = Object.fromEntries(
			Object.entries(secrets).filter(([, value]) => value === null || Boolean(value?.trim())),
		) as ClaudeOrchestratorSettingsUpdate["secrets"];
		setStatus("saving");
		const result = await aoBridge.claudeOrchestratorSettings?.save({ values, secrets: pendingSecrets }) ?? { ok: false as const, error: "unavailable" as const };
		if (!result.ok) {
			setStatus("failed");
			return;
		}
		adopt(result.value);
		setShowErrors(false);
		setStatus("saved");
		void client.invalidateQueries({ queryKey: claudeOrchestratorQueryKeys.info });
		void client.invalidateQueries({ queryKey: claudeOrchestratorQueryKeys.runs });
	};

	const errorText = (error: FieldError | undefined) => error && showErrors ? t(`settings.missionControl.error.${error}`) : undefined;
	const field = (key: Key, label: string, options: { description?: string; mono?: boolean; inputMode?: "numeric" | "decimal" } = {}) => (
		<TextField
			key={key}
			id={key}
			label={label}
			description={options.description}
			value={draft[key] ?? ""}
			placeholder={PLACEHOLDERS[key]}
			mono={options.mono}
			inputMode={options.inputMode}
			locked={fromEnv.has(key)}
			error={errorText(errors[key])}
			onChange={(value) => set(key, value)}
		/>
	);
	const secret = (key: SecretKey, label: string) => (
		<SecretField
			key={key}
			id={key}
			label={label}
			saved={Boolean(view.secretsSet[key]) && secrets[key] !== null}
			locked={fromEnv.has(key)}
			value={secrets[key] ?? ""}
			error={errorText(errors[key])}
			onChange={(value) => { setSecrets((current) => ({ ...current, [key]: value })); setStatus("idle"); }}
			onRemove={() => { setSecrets((current) => ({ ...current, [key]: null })); setStatus("idle"); }}
		/>
	);
	const choice = <T extends string>(key: Key, label: string, options: SettingsOption<T>[], fallback: T, description?: string) => (
		<SettingsRow label={label} description={description}>
			<SettingsOptionMenu
				aria-label={label}
				disabled={fromEnv.has(key)}
				value={(draft[key] as T | undefined) || fallback}
				options={options}
				onChange={(next) => set(key, next)}
			/>
		</SettingsRow>
	);

	const providers: SettingsOption<string>[] = [
		{ value: "deepseek", label: "DeepSeek" },
		{ value: "claude", label: "Claude" },
		{ value: "local", label: t("orchestrator.provider.local") },
	];
	const enabled = isOn(draft.AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED);
	const workerMode = draft.AO_CLAUDE_ORCHESTRATOR_WORKER_MODE || "model";

	return (
		<div className="flex flex-col gap-(--size-settings-section-gap,1.5rem)" data-testid="mission-control-settings">
			<SettingsSection title={t("settings.missionControl")} titleHidden={titleHidden} grouped>
				<SettingsRow label={t("settings.missionControl.enabled")} description={t("settings.missionControl.enabledDescription")}>
					<Switch
						aria-label={t("settings.missionControl.enabled")}
						checked={enabled}
						disabled={fromEnv.has("AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED")}
						onCheckedChange={(next) => set("AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED", next ? "on" : "off")}
					/>
				</SettingsRow>
				<TextField
					id="AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT"
					label={t("settings.missionControl.projectRoot")}
					description={t("settings.missionControl.projectRootDescription")}
					value={draft.AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT ?? ""}
					mono
					locked={fromEnv.has("AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT")}
					error={errorText(errors.AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT)}
					onChange={(value) => set("AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT", value)}
					action={(
						<Button
							variant="ghost"
							size="icon-sm"
							aria-label={t("settings.missionControl.chooseFolder")}
							title={t("settings.missionControl.chooseFolder")}
							disabled={fromEnv.has("AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT")}
							onClick={() => {
								void aoBridge.app.chooseDirectory({ title: t("settings.missionControl.chooseFolder") }).then((selected) => {
									if (selected) set("AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT", selected);
								});
							}}
						>
							<FolderOpen className="size-icon-md" aria-hidden="true" />
						</Button>
					)}
				/>
			</SettingsSection>

			<SettingsSection title={t("settings.missionControl.planner")} grouped>
				{choice("AO_CLAUDE_ORCHESTRATOR_PLANNER_PROVIDER", t("settings.missionControl.plannerProvider"), [
					{ value: "claude", label: "Claude" },
					{ value: "local", label: t("orchestrator.provider.local") },
				], "claude", t("settings.missionControl.plannerProviderDescription"))}
				{field("AO_CLAUDE_ORCHESTRATOR_CLAUDE_BASE_URL", t("settings.missionControl.claudeBaseUrl"), { mono: true })}
				{field("AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL", t("settings.missionControl.claudeModel"), { mono: true })}
				{secret("AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY", t("settings.missionControl.claudeApiKey"))}
			</SettingsSection>

			<SettingsSection title={t("settings.missionControl.workers")} grouped>
				{choice("AO_CLAUDE_ORCHESTRATOR_WORKER_MODE", t("settings.missionControl.workerMode"), [
					{ value: "model", label: t("settings.missionControl.workerModeModel") },
					{ value: "agents", label: t("settings.missionControl.workerModeAgents") },
				], "model", t(workerMode === "agents" ? "settings.missionControl.workerModeAgentsDescription" : "settings.missionControl.workerModeModelDescription"))}
				{choice("AO_CLAUDE_ORCHESTRATOR_WORKER_DEFAULT_PROVIDER", t("settings.missionControl.workerProvider"), providers, "deepseek")}
				{field("AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_BASE_URL", t("settings.missionControl.deepseekBaseUrl"), { mono: true })}
				{field("AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_MODEL", t("settings.missionControl.deepseekModel"), { mono: true })}
				{secret("AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY", t("settings.missionControl.deepseekApiKey"))}
				{field("AO_CLAUDE_ORCHESTRATOR_LOCAL_BASE_URL", t("settings.missionControl.localBaseUrl"), { mono: true, description: t("settings.missionControl.localDescription") })}
				{field("AO_CLAUDE_ORCHESTRATOR_LOCAL_MODEL", t("settings.missionControl.localModel"), { mono: true })}
			</SettingsSection>

			<SettingsSection title={t("settings.missionControl.checks")} grouped>
				<CommandsField
					id="mission-control-worker-commands"
					label={t("settings.missionControl.workerCommands")}
					description={t("settings.missionControl.workerCommandsDescription")}
					value={workerCommands}
					locked={fromEnv.has("AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS")}
					error={errorText(errors.WORKER_COMMANDS)}
					onChange={(value) => { setWorkerCommands(value); setStatus("idle"); }}
				/>
				<CommandsField
					id="mission-control-validator-commands"
					label={t("settings.missionControl.validatorCommands")}
					description={t("settings.missionControl.validatorCommandsDescription")}
					value={validatorCommands}
					locked={fromEnv.has("AO_CLAUDE_ORCHESTRATOR_VALIDATOR_COMMANDS")}
					error={errorText(errors.VALIDATOR_COMMANDS)}
					onChange={(value) => { setValidatorCommands(value); setStatus("idle"); }}
				/>
				{field("AO_CLAUDE_ORCHESTRATOR_WORKER_TIMEOUT", t("settings.missionControl.workerTimeout"), { mono: true, description: t("settings.missionControl.durationHint") })}
				{field("AO_CLAUDE_ORCHESTRATOR_VALIDATOR_TIMEOUT", t("settings.missionControl.validatorTimeout"), { mono: true })}
				<SettingsRow label={t("settings.missionControl.sandbox")} description={t("settings.missionControl.sandboxDescription")}>
					<Switch
						aria-label={t("settings.missionControl.sandbox")}
						checked={isOn(draft.AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_ENABLED)}
						disabled={fromEnv.has("AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_ENABLED")}
						onCheckedChange={(next) => set("AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_ENABLED", next ? "on" : "off")}
					/>
				</SettingsRow>
				{isOn(draft.AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_ENABLED)
					? field("AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_IMAGE", t("settings.missionControl.sandboxImage"), { mono: true })
					: null}
			</SettingsSection>

			<SettingsSection title={t("settings.missionControl.limits")} grouped>
				{field("AO_CLAUDE_ORCHESTRATOR_RUN_TOKEN_BUDGET", t("settings.missionControl.tokenBudget"), { inputMode: "numeric", description: t("settings.missionControl.tokenBudgetDescription") })}
				{choice("AO_CLAUDE_ORCHESTRATOR_MAX_REPLANS", t("settings.missionControl.maxReplans"), ["0", "1", "2", "3"].map((value) => ({ value, label: value })), "1", t("settings.missionControl.maxReplansDescription"))}
				{field("AO_CLAUDE_ORCHESTRATOR_MAX_PARALLEL_SUBTASKS", t("settings.missionControl.parallel"), { inputMode: "numeric", description: t("settings.missionControl.parallelDescription") })}
				{field("AO_CLAUDE_ORCHESTRATOR_CLAUDE_PRICE_INPUT_PER_MTOK", t("settings.missionControl.claudePriceInput"), { inputMode: "decimal", description: t("settings.missionControl.priceDescription") })}
				{field("AO_CLAUDE_ORCHESTRATOR_CLAUDE_PRICE_OUTPUT_PER_MTOK", t("settings.missionControl.claudePriceOutput"), { inputMode: "decimal" })}
				{field("AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_PRICE_INPUT_PER_MTOK", t("settings.missionControl.deepseekPriceInput"), { inputMode: "decimal" })}
				{field("AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_PRICE_OUTPUT_PER_MTOK", t("settings.missionControl.deepseekPriceOutput"), { inputMode: "decimal" })}
			</SettingsSection>

			<div className="flex flex-wrap items-center gap-3 px-1 pb-2">
				<Button variant="primary" size="sm" disabled={status === "saving"} onClick={() => void save()}>
					{status === "saving" ? <LoaderCircle className="size-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" /> : null}
					{t("settings.missionControl.save")}
				</Button>
				{status === "saved" ? (
					<span role="status" className="flex items-center gap-1.5 text-xs text-settings-muted">
						<Check className="size-3.5 text-status-ready" aria-hidden="true" />{t("settings.missionControl.saved")}
					</span>
				) : null}
				{status === "failed" ? <span role="alert" className="text-xs text-error">{t("settings.missionControl.saveFailed")}</span> : null}
				{showErrors && hasErrors ? (
					<span role="alert" className="flex items-center gap-1.5 text-xs text-error">
						<TriangleAlert className="size-3.5" aria-hidden="true" />{t("settings.missionControl.fixErrors")}
					</span>
				) : null}
				<p className="w-full text-xs leading-4 text-settings-muted">{t("settings.missionControl.saveNote")}</p>
			</div>
		</div>
	);
}

function FieldShell({ id, label, description, locked, error, children }: { id: string; label: string; description?: string; locked: boolean; error?: string; children: ReactNode }) {
	const { t } = useTranslation();
	return (
		<div className="settings-row-bar h-auto min-h-(--size-settings-row) flex-col items-stretch gap-1.5 py-3 sm:flex-row sm:items-start">
			<div className="min-w-0 sm:w-56 sm:shrink-0">
				<label htmlFor={id} className="block text-sm leading-5 text-settings-label">{label}</label>
				{description ? <span className="mt-0.5 block text-pretty text-xs leading-4 text-settings-muted">{description}</span> : null}
				{locked ? <span className="mt-0.5 block text-xs leading-4 text-settings-muted">{t("settings.missionControl.fromEnvironment")}</span> : null}
			</div>
			<div className="flex min-w-0 flex-1 flex-col gap-1">
				{children}
				{error ? <p id={`${id}-error`} className="text-xs leading-4 text-error">{error}</p> : null}
			</div>
		</div>
	);
}

function TextField({ id, label, description, value, placeholder, mono, inputMode, locked, error, onChange, action }: {
	id: string;
	label: string;
	description?: string;
	value: string;
	placeholder?: string;
	mono?: boolean;
	inputMode?: "numeric" | "decimal";
	locked: boolean;
	error?: string;
	onChange: (value: string) => void;
	action?: ReactNode;
}) {
	return (
		<FieldShell id={id} label={label} description={description} locked={locked} error={error}>
			<div className="flex items-center gap-1">
				<Input
					id={id}
					dir={mono ? "ltr" : undefined}
					className={cn(mono && "font-mono text-xs")}
					value={value}
					placeholder={placeholder}
					inputMode={inputMode}
					disabled={locked}
					spellCheck={false}
					autoComplete="off"
					aria-invalid={Boolean(error) || undefined}
					aria-describedby={error ? `${id}-error` : undefined}
					onChange={(event) => onChange(event.target.value)}
				/>
				{action}
			</div>
		</FieldShell>
	);
}

function SecretField({ id, label, saved, locked, value, error, onChange, onRemove }: {
	id: string;
	label: string;
	saved: boolean;
	locked: boolean;
	value: string;
	error?: string;
	onChange: (value: string) => void;
	onRemove: () => void;
}) {
	const { t } = useTranslation();
	return (
		<FieldShell id={id} label={label} description={saved ? t("settings.missionControl.keySaved") : t("settings.missionControl.keyNotSaved")} locked={locked} error={error}>
			<div className="flex items-center gap-1">
				<Input
					id={id}
					type="password"
					dir="ltr"
					className="font-mono text-xs"
					value={value}
					placeholder={saved ? t("settings.missionControl.keyKeepPlaceholder") : t("settings.missionControl.keyPlaceholder")}
					disabled={locked}
					autoComplete="off"
					spellCheck={false}
					aria-invalid={Boolean(error) || undefined}
					aria-describedby={error ? `${id}-error` : undefined}
					onChange={(event) => onChange(event.target.value)}
				/>
				{saved && !locked ? (
					<Button variant="ghost" size="sm" onClick={onRemove}>{t("settings.missionControl.keyRemove")}</Button>
				) : null}
			</div>
		</FieldShell>
	);
}

function CommandsField({ id, label, description, value, locked, error, onChange }: {
	id: string;
	label: string;
	description: string;
	value: string;
	locked: boolean;
	error?: string;
	onChange: (value: string) => void;
}) {
	return (
		<FieldShell id={id} label={label} description={description} locked={locked} error={error}>
			<textarea
				id={id}
				dir="ltr"
				value={value}
				disabled={locked}
				spellCheck={false}
				placeholder={COMMAND_PLACEHOLDER}
				aria-invalid={Boolean(error) || undefined}
				aria-describedby={error ? `${id}-error` : undefined}
				onChange={(event) => onChange(event.target.value)}
				className="min-h-20 w-full resize-y rounded-md border border-transparent bg-input/50 px-3 py-2 font-mono text-xs leading-5 outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 disabled:opacity-50 aria-invalid:border-destructive"
			/>
		</FieldShell>
	);
}
