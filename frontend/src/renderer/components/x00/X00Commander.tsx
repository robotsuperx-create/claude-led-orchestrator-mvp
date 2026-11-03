import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { Bell, BellOff, Check, LoaderCircle, Mic, Send, Square, Trash2, TriangleAlert, Volume2, VolumeX, X } from "lucide-react";
import type { ClaudeOrchestratorRun } from "../../../shared/claude-orchestrator";
import type { X00Action, X00Mood } from "../../../shared/x00";
import { useClaudeOrchestratorInfo, useClaudeOrchestratorRuns } from "../../hooks/useClaudeOrchestrator";
import { useX00Actions, useX00Alerts, useX00Conversation, useX00Info, useX00Listen, x00Hush, x00VoiceLevel } from "../../hooks/useX00";
import { x00StateForRuns } from "../../lib/x00-fleet";
import type { X00State } from "../../lib/x00-portrait";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { useX00Store, type X00Entry, type X00Proposal } from "../../stores/x00-store";
import { SpaceBackdrop } from "../SpaceBackdrop";
import { Button } from "../ui/button";
import { X00Mark, X00Portrait } from "../X00Portrait";

const isWindows = typeof navigator !== "undefined" && /Windows/i.test(navigator.userAgent);

function moodState(mood: X00Mood): X00State {
	return mood === "pleased" ? "done" : mood === "concerned" ? "held" : "watching";
}

/** X-00's light right now: dark without a key, breathing while he thinks, his mood while he speaks, else the fleet's. */
export function useX00PortraitState(): { state: X00State; speaking: boolean; enabled: boolean } {
	const info = useX00Info();
	const orchestrator = useClaudeOrchestratorInfo();
	const runs = useClaudeOrchestratorRuns(orchestrator.data?.enabled === true);
	const thinking = useX00Store((store) => store.thinking);
	const speaking = useX00Store((store) => store.speaking);
	const hearing = useX00Store((store) => store.hearing);
	const mood = useX00Store((store) => store.mood);
	const enabled = info.data?.enabled === true;
	const fleet = x00StateForRuns(runs.data ?? []);
	const state: X00State = !enabled ? "failed" : thinking || hearing === "transcribing" ? "working" : speaking ? moodState(mood) : hearing === "listening" ? "watching" : fleet;
	return { state, speaking, enabled };
}

/**
 * X-00 on every screen: he keeps watch over the runs (alerts) and, when
 * opened, talks with the person in a panel at the window's edge. The panel is
 * not modal; the app stays usable behind it.
 */
export function X00Commander() {
	useX00Alerts();
	const open = useX00Store((store) => store.open);
	return open ? <X00Panel /> : null;
}

/** The sidebar row that opens X-00, with his head, his state and unseen alerts. */
export function X00SidebarRow({ className, tabIndex, compact = false }: { className?: string; tabIndex?: number; compact?: boolean }) {
	const { t } = useTranslation();
	const open = useX00Store((store) => store.open);
	const unseen = useX00Store((store) => store.unseen);
	const toggle = useX00Store((store) => store.toggle);
	const { state, speaking, enabled } = useX00PortraitState();
	const label = open ? t("x00.close") : t("x00.open");
	return (
		<button
			type="button"
			aria-label={label}
			aria-expanded={open}
			aria-controls="x00-panel"
			title={compact ? label : undefined}
			tabIndex={tabIndex}
			className={cn("x00-entry", className)}
			data-state={state}
			data-speaking={speaking ? "true" : undefined}
			data-testid={compact ? "x00-rail-button" : "x00-sidebar-row"}
			onClick={toggle}
		>
			<span className="x00-entry-head relative z-[1] shrink-0">
				<X00Mark className="size-6" />
				<span className={cn("x00-entry-dot", enabled ? "x00-entry-dot--on" : "x00-entry-dot--off")} aria-hidden="true" />
			</span>
			{compact ? null : (
				<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2">
					<span className="xr-wordmark text-[13px] leading-5">{t("x00.title")}</span>
					<span className="truncate text-[11px] text-muted-foreground">
						{!enabled ? t("x00.status.offline") : speaking ? t("x00.status.speaking") : t("x00.status.watching")}
					</span>
				</span>
			)}
			{unseen > 0 ? (
				<span className="x00-entry-badge relative z-[1]" data-testid="x00-unseen">{unseen > 9 ? "9+" : unseen}</span>
			) : null}
		</button>
	);
}

function X00Panel() {
	const { t } = useTranslation();
	const info = useX00Info();
	const entries = useX00Store((store) => store.entries);
	const thinking = useX00Store((store) => store.thinking);
	const prefs = useX00Store((store) => store.prefs);
	const setPrefs = useX00Store((store) => store.setPrefs);
	const setOpen = useX00Store((store) => store.setOpen);
	const clear = useX00Store((store) => store.clear);
	const { state, speaking, enabled } = useX00PortraitState();
	const send = useX00Conversation();
	const ear = useX00Listen(send);
	const canListen = enabled && ear.supported && info.data?.listen !== undefined;
	const orchestrator = useClaudeOrchestratorInfo();
	const runs = useClaudeOrchestratorRuns(orchestrator.data?.enabled === true);
	const [draft, setDraft] = useState("");
	const input = useRef<HTMLTextAreaElement>(null);
	const transcript = useRef<HTMLDivElement>(null);

	useEffect(() => {
		input.current?.focus();
	}, []);

	useEffect(() => {
		const node = transcript.current;
		if (node) node.scrollTop = node.scrollHeight;
	}, [entries.length, thinking]);

	const titles = useMemo(() => new Map((runs.data ?? []).map((run: ClaudeOrchestratorRun) => [run.runId, run.title])), [runs.data]);

	const submit = () => {
		const text = draft.trim();
		if (!text || thinking || !enabled) return;
		setDraft("");
		void send(text);
	};

	const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
		if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
			event.preventDefault();
			submit();
		}
	};

	const status = !enabled
		? t("x00.status.offline")
		: ear.hearing === "listening"
			? t("x00.status.listening")
			: ear.hearing === "transcribing"
				? t("x00.status.transcribing")
				: thinking
					? t("x00.status.thinking")
					: speaking
						? t("x00.status.speaking")
						: t("x00.status.watching");

	return (
		<aside
			id="x00-panel"
			aria-label={t("x00.title")}
			className="x00-panel"
			data-testid="x00-panel"
			onKeyDown={(event) => {
				if (event.key === "Escape") {
					event.stopPropagation();
					if (ear.hearing === "listening") ear.cancel();
					else setOpen(false);
				}
				// Ctrl+Space talks, like a radio's push-to-talk.
				if (event.code === "Space" && (event.ctrlKey || event.metaKey) && canListen) {
					event.preventDefault();
					ear.toggle();
				}
			}}
		>
			<SpaceBackdrop className="x00-panel-space" />
			<header className="relative z-[1] flex items-center gap-1 px-3 pt-2.5">
				<div className="min-w-0 flex-1 px-1">
					<h2 className="xr-wordmark text-[17px] leading-6">{t("x00.title")}</h2>
					<p className="truncate text-[11px] leading-4 text-muted-foreground">{t("x00.subtitle")}</p>
				</div>
				{speaking ? (
					<Button variant="ghost" size="icon-sm" aria-label={t("x00.hush")} title={t("x00.hush")} onClick={() => x00Hush()}>
						<Square className="size-3.5" aria-hidden="true" />
					</Button>
				) : null}
				<Button
					variant="ghost"
					size="icon-sm"
					aria-label={prefs.voice ? t("x00.voiceOn") : t("x00.voiceOff")}
					aria-pressed={prefs.voice}
					title={prefs.voice ? t("x00.voiceOn") : t("x00.voiceOff")}
					onClick={() => {
						if (prefs.voice) x00Hush();
						setPrefs({ voice: !prefs.voice });
					}}
				>
					{prefs.voice ? <Volume2 className="size-icon-md" aria-hidden="true" /> : <VolumeX className="size-icon-md" aria-hidden="true" />}
				</Button>
				<Button
					variant="ghost"
					size="icon-sm"
					aria-label={prefs.alerts ? t("x00.alertsOn") : t("x00.alertsOff")}
					aria-pressed={prefs.alerts}
					title={prefs.alerts ? t("x00.alertsOn") : t("x00.alertsOff")}
					onClick={() => setPrefs({ alerts: !prefs.alerts })}
				>
					{prefs.alerts ? <Bell className="size-icon-md" aria-hidden="true" /> : <BellOff className="size-icon-md" aria-hidden="true" />}
				</Button>
				<Button variant="ghost" size="icon-sm" aria-label={t("x00.clear")} title={t("x00.clear")} disabled={entries.length === 0} onClick={() => { x00Hush(); clear(); }}>
					<Trash2 className="size-icon-md" aria-hidden="true" />
				</Button>
				<Button variant="ghost" size="icon-sm" aria-label={t("x00.close")} title={t("x00.close")} onClick={() => setOpen(false)}>
					<X className="size-icon-md" aria-hidden="true" />
				</Button>
			</header>

			<div className="relative z-[1] flex flex-col items-center px-4 pb-1">
				<X00Portrait state={state} speaking={speaking} voice={x00VoiceLevel} className="w-40" label={`${t("x00.title")}: ${status}`} />
				<p className="mt-1 text-[12px] leading-4 text-muted-foreground" role="status" aria-live="polite" data-testid="x00-status">{status}</p>
			</div>

			<div ref={transcript} className="x00-transcript relative z-[1] min-h-0 flex-1 overflow-y-auto px-4 py-3" data-testid="x00-transcript">
				{!enabled && info.data && !info.data.enabled ? <SetupCard reason={info.data.reason} /> : null}
				{enabled && entries.length === 0 ? (
					<p className="x00-line x00-line--x00" dir="auto">{t("x00.greeting")}</p>
				) : null}
				{entries.map((entry) => <EntryView key={entry.id} entry={entry} titles={titles} />)}
				{thinking ? (
					<p className="x00-line x00-line--thinking" role="status">
						<LoaderCircle className="size-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" />
						{t("x00.status.thinking")}
					</p>
				) : null}
			</div>

			<form
				className="relative z-[1] border-t border-border px-3 pb-3 pt-2.5"
				onSubmit={(event) => {
					event.preventDefault();
					submit();
				}}
			>
				<div className="x00-composer flex items-end gap-2">
					<textarea
						ref={input}
						dir="auto"
						rows={2}
						value={draft}
						disabled={!enabled}
						maxLength={4000}
						aria-label={t("x00.placeholder")}
						placeholder={t("x00.placeholder")}
						className="min-h-10 flex-1 resize-none bg-transparent px-2 py-1.5 text-[13px] leading-5 text-foreground outline-none placeholder:text-muted-foreground disabled:opacity-60"
						onChange={(event) => setDraft(event.target.value)}
						onKeyDown={onKeyDown}
						data-testid="x00-input"
					/>
					{canListen ? <MicButton hearing={ear.hearing} level={ear.level} disabled={thinking} onToggle={ear.toggle} /> : null}
					<Button type="submit" variant="primary" size="icon-sm" aria-label={t("x00.send")} title={t("x00.send")} disabled={!enabled || thinking || !draft.trim()}>
						<Send className="size-3.5 rtl:-scale-x-100" aria-hidden="true" />
					</Button>
				</div>
				{canListen ? (
					<p className="mt-1.5 flex items-center gap-1.5 text-[11px] leading-4 text-muted-foreground">
						<Mic className="size-3" aria-hidden="true" />{t("x00.micHint")}
					</p>
				) : isWindows && enabled ? (
					<p className="mt-1.5 flex items-center gap-1.5 text-[11px] leading-4 text-muted-foreground">
						<Mic className="size-3" aria-hidden="true" />{t("x00.dictateHint")}
					</p>
				) : null}
			</form>
		</aside>
	);
}

/** Push to talk: a ring that swells with your voice while he listens. */
function MicButton({ hearing, level, disabled, onToggle }: { hearing: "idle" | "listening" | "transcribing"; level: { current: number }; disabled: boolean; onToggle: () => void }) {
	const { t } = useTranslation();
	const ring = useRef<HTMLSpanElement>(null);
	useEffect(() => {
		if (hearing !== "listening") return;
		let frame = 0;
		const tick = () => {
			ring.current?.style.setProperty("--x00-level", level.current.toFixed(3));
			frame = window.requestAnimationFrame(tick);
		};
		frame = window.requestAnimationFrame(tick);
		return () => window.cancelAnimationFrame(frame);
	}, [hearing, level]);
	const label = hearing === "listening" ? t("x00.listenStop") : hearing === "transcribing" ? t("x00.status.transcribing") : t("x00.listen");
	return (
		<button
			type="button"
			className="x00-mic"
			data-hearing={hearing}
			aria-label={label}
			aria-pressed={hearing === "listening"}
			title={hearing === "idle" ? `${label} (Ctrl+Space)` : label}
			disabled={hearing === "transcribing" || (disabled && hearing === "idle")}
			onClick={onToggle}
			data-testid="x00-mic"
		>
			<span ref={ring} className="x00-mic-ring" aria-hidden="true" />
			{hearing === "transcribing" ? (
				<LoaderCircle className="relative size-4 animate-spin motion-reduce:animate-none" aria-hidden="true" />
			) : hearing === "listening" ? (
				<Square className="relative size-3.5" aria-hidden="true" />
			) : (
				<Mic className="relative size-4" aria-hidden="true" />
			)}
		</button>
	);
}

const SETUP_KEYS = {
	no_key: "x00.setup.no_key",
	off: "x00.setup.off",
	invalid_url: "x00.setup.invalid_url",
	invalid_model: "x00.setup.invalid_model",
} as const;

function SetupCard({ reason }: { reason: "no_key" | "off" | "invalid_url" | "invalid_model" }) {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	return (
		<div className="x00-card mb-3" data-testid="x00-setup">
			<p className="text-[13px] font-medium leading-5 text-foreground">{t("x00.setup.title")}</p>
			<p className="mt-1 text-[12px] leading-5 text-muted-foreground">{t(SETUP_KEYS[reason])}</p>
			<Button className="mt-2.5" variant="primary" size="sm" onClick={() => openGlobalSettings("missionControl")}>{t("x00.setup.open")}</Button>
		</div>
	);
}

function EntryView({ entry, titles }: { entry: X00Entry; titles: Map<string, string> }) {
	const { t } = useTranslation();
	if (entry.kind === "error") {
		return (
			<p className="x00-line x00-line--error" role="alert">
				<TriangleAlert className="size-3.5 shrink-0" aria-hidden="true" />
				{t(errorKey(entry.code))}
			</p>
		);
	}
	if (entry.kind === "alert") {
		return <p className="x00-line x00-line--alert" data-tone={entry.tone} dir="auto" data-testid="x00-alert">{entry.content}</p>;
	}
	if (entry.role === "user") {
		return (
			<div className="mb-3 flex justify-end">
				<p className="x00-line x00-line--you" dir="auto"><span className="sr-only">{t("x00.you")}: </span>{entry.content}</p>
			</div>
		);
	}
	return (
		<div className="mb-3" data-testid="x00-reply">
			<p className="x00-line x00-line--x00" data-mood={entry.mood} dir="auto">{entry.content}</p>
			{entry.proposals?.map((proposal) => <ProposalCard key={proposal.id} entryId={entry.id} proposal={proposal} titles={titles} />)}
		</div>
	);
}

const ACTION_KEYS = {
	start_run: "x00.action.start_run",
	approve_plan: "x00.action.approve_plan",
	reject_plan: "x00.action.reject_plan",
	cancel_run: "x00.action.cancel_run",
	publish_run: "x00.action.publish_run",
} as const;
const OPEN_KEYS = {
	mission_control: "x00.action.open.mission_control",
	settings: "x00.action.open.settings",
	home: "x00.action.open.home",
} as const;

function actionKey(action: X00Action) {
	return action.type === "open" ? OPEN_KEYS[action.screen] : ACTION_KEYS[action.type];
}

const ERROR_KEYS = {
	daemon_unavailable: "x00.error.daemon_unavailable",
	disabled: "x00.error.disabled",
	busy: "x00.error.busy",
	invalid: "x00.error.invalid",
	timeout: "x00.error.timeout",
	provider_failed: "x00.error.provider_failed",
	failed: "x00.error.failed",
	mic_denied: "x00.error.mic_denied",
	no_mic: "x00.error.no_mic",
	no_speech: "x00.error.no_speech",
	voice_off: "x00.error.voice_off",
} as const;

function errorKey(code: string) {
	return code in ERROR_KEYS ? ERROR_KEYS[code as keyof typeof ERROR_KEYS] : ERROR_KEYS.failed;
}

function ProposalCard({ entryId, proposal, titles }: { entryId: string; proposal: X00Proposal; titles: Map<string, string> }) {
	const { t } = useTranslation();
	const run = useX00Actions();
	const setProposal = useX00Store((store) => store.setProposal);
	const { action, status } = proposal;
	const detail = action.type === "start_run"
		? action.task
		: "runId" in action
			? titles.get(action.runId) || action.runId.slice(-10)
			: null;
	return (
		<div className="x00-card mt-2" data-status={status} data-testid="x00-proposal">
			<p className="text-[12px] font-medium leading-5 text-foreground">{t(actionKey(action))}</p>
			{detail ? <p className="mt-0.5 line-clamp-4 whitespace-pre-wrap text-[12px] leading-5 text-muted-foreground" dir="auto">{detail}</p> : null}
			{action.type === "start_run" && action.approvePlan ? <p className="mt-0.5 text-[11px] leading-4 text-muted-foreground">{t("x00.action.withApproval")}</p> : null}
			<div className="mt-2 flex items-center gap-2">
				{status === "proposed" ? (
					<>
						<Button variant="primary" size="sm" onClick={() => void run(entryId, proposal)} data-testid="x00-proposal-run">{t("x00.action.run")}</Button>
						<Button variant="ghost" size="sm" onClick={() => setProposal(entryId, proposal.id, "dismissed")}>{t("x00.action.dismiss")}</Button>
					</>
				) : status === "running" ? (
					<span className="flex items-center gap-1.5 text-[12px] text-muted-foreground" role="status"><LoaderCircle className="size-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" />{t("x00.action.running")}</span>
				) : status === "done" ? (
					<span className="flex items-center gap-1.5 text-[12px] text-foreground" role="status"><Check className="size-3.5 text-status-ready" aria-hidden="true" />{t("x00.action.done")}</span>
				) : status === "failed" ? (
					<span className="flex items-center gap-1.5 text-[12px] text-destructive" role="alert"><TriangleAlert className="size-3.5" aria-hidden="true" />{t("x00.action.failed")}</span>
				) : (
					<span className="text-[12px] text-muted-foreground">{t("x00.action.dismissed")}</span>
				)}
			</div>
		</div>
	);
}

/** Opens X-00 from anywhere (a portrait on Home or Mission control, a shortcut). */
export function openX00(): void {
	useX00Store.getState().setOpen(true);
}
