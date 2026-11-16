import { useNavigate } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState, type CSSProperties, type MouseEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { ClaudeOrchestratorRun } from "../../../shared/claude-orchestrator";
import {
	useCancelClaudeOrchestratorRun,
	useClaudeOrchestratorInfo,
	useClaudeOrchestratorLive,
	useClaudeOrchestratorPlan,
	useClaudeOrchestratorRuns,
} from "../../hooks/useClaudeOrchestrator";
import { x00VoiceLevel } from "../../hooks/useX00";
import { crewMember } from "../../lib/x00-crew";
import {
	crewMarks,
	crewMarksFromSteps,
	deckLightForX00,
	deckRuns,
	isLiveRun,
	runLight,
	runMinutes,
	runProgress,
	runStage,
	RUN_STAGE_COUNT,
	type DeckLight,
} from "../../lib/x-robot-deck";
import { isLinuxPlatform, isMacPlatform } from "../../lib/platform";
import { usesPreviewWorkspaceData } from "../../lib/preview-mode";
import { useShell } from "../../lib/shell-context";
import { useLocaleStore } from "../../stores/locale-store";
import { useX00Store } from "../../stores/x00-store";
import { openX00, useX00PortraitState } from "../x00/X00Commander";
import { AgentMap } from "./AgentMap";
import { CodeStream } from "./CodeStream";
import { DeckMedia } from "./DeckMedia";
import { DeckPortrait } from "./DeckPortrait";
import { LivePreview } from "./LivePreview";
import "./deck.css";

const BRAND = "X-ROBOT-AI";
/** macOS and Linux keep the window's titlebar buttons over the top-left corner; the deck's header sits below them. */
const TITLEBAR_CLUSTER = isMacPlatform() || isLinuxPlatform();
const KILL_ARM_MS = 4_000;
/** The deck plays its arrival once per app session, as the trading deck does per browser session. */
let arrived = false;

const DIGITS = "0123456789";

/** Numbers that roll into place (the deck's numeral.js): each digit a column of 0–9 that slides to its value. */
function Roll({ text, className }: { text: string; className?: string }) {
	return (
		<span className={["n", "roll", className].filter(Boolean).join(" ")}>
			<span className="sr-only">{text}</span>
			{[...text].map((ch, index) =>
				/\d/.test(ch) ? (
					// A digit's column is its place in the number, so the index is its key.
					<span className="col" key={`d${index}`} aria-hidden="true">
						<span style={{ transform: `translateY(-${DIGITS.indexOf(ch)}em)` }}>
							{DIGITS.split("").map((digit) => (
								<i key={digit}>{digit}</i>
							))}
						</span>
					</span>
				) : (
					<span className="ch" key={`c${index}`} aria-hidden="true">
						{ch}
					</span>
				),
			)}
		</span>
	);
}

/** The decision colour on the deck: ion while live, long when done, short when held, dim when it failed. */
function decisionColour(run: ClaudeOrchestratorRun | undefined): string {
	if (!run) return "var(--ivory-2)";
	switch (runLight(run)) {
		case "long":
			return "var(--long)";
		case "short":
			return "var(--short)";
		case "veto":
			return "var(--ivory-3)";
		case "deciding":
			return "var(--ion)";
		default:
			return run.state === "canceled" ? "var(--ivory-3)" : "var(--ivory-2)";
	}
}

function money(value: number | undefined): string {
	return value === undefined ? "" : `${value.toFixed(2)}$`;
}

function useRunText() {
	const { t } = useTranslation();
	const stateLabel = (run: ClaudeOrchestratorRun) =>
		run.interrupted ? t("orchestrator.state.interrupted") : t(`orchestrator.state.${run.state}`);
	const blocker = (run: ClaudeOrchestratorRun): string => {
		if (run.interrupted) return t("deck.blocker.interrupted");
		switch (run.state) {
			case "pending":
				return t("deck.blocker.pending");
			case "planning":
				return t("deck.blocker.next", { stage: t("orchestrator.stage.executing") });
			case "awaiting_approval":
				return t("deck.blocker.awaiting");
			case "executing":
				return t("deck.blocker.next", { stage: t("orchestrator.stage.validating") });
			case "validating":
				return t("deck.blocker.next", { stage: t("orchestrator.stage.reviewing") });
			case "reviewing":
				return t("deck.blocker.reviewing");
			case "completed":
				return run.pullRequestUrl ? t("deck.blocker.published") : t("deck.blocker.completed");
			case "held":
				return t("deck.blocker.held");
			case "failed":
				return t("deck.blocker.failed");
			default:
				return t("deck.blocker.canceled");
		}
	};
	return { stateLabel, blocker };
}

/** What X-00 said last (his greeting until he has spoken), and the question it answered. */
function useSayLine(): { line: string; question: string } {
	const { t } = useTranslation();
	const entries = useX00Store((store) => store.entries);
	const { line, question } = useMemo(() => {
		for (let i = entries.length - 1; i >= 0; i -= 1) {
			const entry = entries[i];
			if (entry.kind === "alert" || (entry.kind === "turn" && entry.role === "assistant")) {
				const before = entries[i - 1];
				const asked = before?.kind === "turn" && before.role === "user" ? before.content : "";
				return { line: entry.content, question: asked };
			}
		}
		return { line: "", question: "" };
	}, [entries]);
	return { line: line || t("x00.greeting"), question };
}

/** The header: one line, the kill switch at the edge, always. */
function DeckTop({ liveRuns }: { liveRuns: ClaudeOrchestratorRun[] }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const { daemonStatus } = useShell();
	const orchestrator = useClaudeOrchestratorInfo();
	const locale = useLocaleStore((store) => store.locale);
	const setLocale = useLocaleStore((store) => store.setLocale);
	const cancel = useCancelClaudeOrchestratorRun();
	const [menuOpen, setMenuOpen] = useState(false);
	const [armed, setArmed] = useState(false);
	const menu = useRef<HTMLDivElement>(null);

	useEffect(() => {
		if (!menuOpen) return;
		const close = (event: PointerEvent | KeyboardEvent) => {
			if (event instanceof KeyboardEvent ? event.key === "Escape" : !menu.current?.contains(event.target as Node)) setMenuOpen(false);
		};
		document.addEventListener("pointerdown", close);
		document.addEventListener("keydown", close);
		return () => {
			document.removeEventListener("pointerdown", close);
			document.removeEventListener("keydown", close);
		};
	}, [menuOpen]);

	useEffect(() => {
		if (!armed) return;
		const timer = window.setTimeout(() => setArmed(false), KILL_ARM_MS);
		return () => window.clearTimeout(timer);
	}, [armed]);

	const connected = usesPreviewWorkspaceData || daemonStatus.state === "ready";
	const enabled = orchestrator.data?.enabled === true;
	const model = orchestrator.data?.plannerProvider === "local" ? orchestrator.data.plannerModel : "Claude";
	const live = !connected ? t("deck.live.offline") : !enabled ? t("deck.live.off") : t("deck.live.on", { model });

	const go = (to: "/orchestrator" | "/automations" | "/stores" | "/settings") => {
		setMenuOpen(false);
		void navigate({ to });
	};
	const stopAll = () => {
		if (liveRuns.length === 0) return;
		if (!armed) {
			setArmed(true);
			return;
		}
		setArmed(false);
		for (const run of liveRuns) cancel.mutate(run.runId);
	};

	return (
		<header className="top">
			<span className="brand">{BRAND}</span>
			<span className="live" data-testid="deck-live">
				<i className={connected && enabled ? undefined : "off"} />
				<span>{live}</span>
			</span>
			<span className="grow" />
			<button className="btn btn-primary" type="button" onClick={() => go("/orchestrator")} data-testid="deck-new-run">
				{t("deck.newRun")}
			</button>
			<button className="btn btn-text" type="button" onClick={openX00} data-testid="home-talk">
				<svg className="ico" viewBox="0 0 24 24" aria-hidden="true">
					<rect x="9" y="3" width="6" height="11" rx="3" />
					<path d="M5 11a7 7 0 0 0 14 0" />
					<path d="M12 18v3" />
				</svg>
				<span>{t("deck.talk")}</span>
			</button>
			<button
				className="btn btn-text"
				type="button"
				onClick={() => void setLocale(locale === "ar" ? "en" : "ar")}
				aria-label={t("deck.langSwitch")}
				title={t("deck.langSwitch")}
			>
				{t("deck.langOther")}
			</button>
			<div className={menuOpen ? "menu open" : "menu"} ref={menu}>
				<button
					className="btn btn-icon"
					type="button"
					aria-expanded={menuOpen}
					aria-label={t("deck.menu")}
					title={t("deck.menu")}
					onClick={() => setMenuOpen((open) => !open)}
				>
					<svg viewBox="0 0 24 24" aria-hidden="true">
						<path d="M4 7h10M18 7h2M4 17h4M12 17h8" />
						<circle cx="16" cy="7" r="2" />
						<circle cx="10" cy="17" r="2" />
					</svg>
				</button>
				<div className="menu-list floating" role="menu">
					<button type="button" role="menuitem" onClick={() => { setMenuOpen(false); openX00(); }}>{t("deck.menuVoice")}</button>
					<button type="button" role="menuitem" onClick={() => go("/orchestrator")}>{t("orchestrator.title")}</button>
					<button type="button" role="menuitem" onClick={() => go("/automations")}>{t("automations.title")}</button>
					<button type="button" role="menuitem" onClick={() => go("/stores")}>{t("stores.title")}</button>
					<button type="button" role="menuitem" onClick={() => go("/settings")}>{t("shell.settings")}</button>
				</div>
			</div>
			<button
				className={armed ? "btn btn-kill edge armed" : "btn btn-kill edge"}
				type="button"
				onClick={stopAll}
				disabled={liveRuns.length === 0}
				title={t("deck.stopAllTip")}
				data-testid="deck-stop-all"
			>
				{armed ? t("deck.stopArmed", { count: liveRuns.length }) : t("deck.stopAll")}
			</button>
		</header>
	);
}

/** The run in focus: its name, a big numeral for how far it has come, its stage, its decision, and what comes next. */
function DeckFocus({ lead, second }: { lead?: ClaudeOrchestratorRun; second?: ClaudeOrchestratorRun }) {
	const { t } = useTranslation();
	const { stateLabel, blocker } = useRunText();
	if (!lead) {
		return (
			<section className="focus" aria-live="polite" data-testid="deck-focus">
				<span className="d2 t-2">{t("deck.noRun")}</span>
				<span className="n n-price">—</span>
				<p className="small t-2 value">{t("deck.noRunHint")}</p>
			</section>
		);
	}
	const progress = runProgress(lead);
	const minutes = runMinutes(lead);
	const stage = runStage(lead);
	const colour = decisionColour(lead);
	const secondProgress = second ? runProgress(second) : null;
	return (
		<section className="focus" aria-live="polite" data-testid="deck-focus">
			<span className="d2 t-2" dir="auto" data-testid="deck-lead-name">{lead.title}</span>
			<Roll className="n-price" text={progress === null ? "—" : `${progress}%`} />
			<div className="pair small t-2">
				{stage > 0 ? <span>{t("deck.stage", { n: stage, total: RUN_STAGE_COUNT })}</span> : null}
				{minutes !== null ? <span>{t("deck.minutes", { n: minutes })}</span> : null}
				{lead.usage?.costUsd !== undefined ? <span className="n">{money(lead.usage.costUsd)}</span> : null}
			</div>
			{lead.branch ? (
				<p className="small t-2 value">
					{t("deck.branch")} <bdi className="n">{lead.branch}</bdi>
				</p>
			) : null}
			<div className="gap" />
			<div className="decision" style={{ "--light": colour } as CSSProperties} data-testid="deck-decision">
				<span>{stateLabel(lead)}</span>
				{progress !== null ? <span className="n">{progress}%</span> : null}
			</div>
			<div className="conf" style={{ "--light": colour } as CSSProperties}>
				<i style={{ "--w": `${progress ?? 0}%` } as CSSProperties} />
			</div>
			<p className={lead.state === "failed" ? "blocker veto" : "blocker"}>{blocker(lead)}</p>
			{second ? (
				<div className="second" data-testid="deck-second">
					<span className="small t-2" dir="auto">{second.title}</span>
					<Roll className="n-second" text={secondProgress === null ? "—" : `${secondProgress}%`} />
					<div className="decision small" style={{ "--light": decisionColour(second) } as CSSProperties}>
						<span>{stateLabel(second)}</span>
					</div>
				</div>
			) : null}
		</section>
	);
}

/** The crew rail: X-01 to X-16, each lit by its step of the plan in focus. */
function DeckCrew({ lead }: { lead?: ClaudeOrchestratorRun }) {
	const { t } = useTranslation();
	const plan = useClaudeOrchestratorPlan(lead?.runId ?? "", Boolean(lead) && lead?.state !== "pending");
	const steps = plan.data?.runId === lead?.runId ? (plan.data?.steps ?? []) : [];
	const board = useClaudeOrchestratorLive(lead?.runId ?? "", Boolean(lead) && isLiveRun(lead as ClaudeOrchestratorRun));
	const liveSteps = board.data?.runId === lead?.runId ? (board.data?.steps ?? []) : [];
	const marks = liveSteps.length > 0 ? crewMarksFromSteps(liveSteps) : crewMarks(lead, steps.length);
	return (
		<section className="crew" aria-label={t("deck.crewRail")}>
			<div className="crew-rail" data-testid="deck-crew">
				{marks.map((tone, index) => {
					const member = crewMember(index);
					const step = steps[index];
					return (
						<button
							className={tone === "idle" ? "mark" : `mark ${tone}`}
							key={member.callSign}
							type="button"
							title={step ? `${member.callSign} · ${step.title}` : member.callSign}
							onClick={openX00}
							data-tone={tone}
						>
							<i />
							{member.callSign}
						</button>
					);
				})}
			</div>
		</section>
	);
}

/** His sentence, under the commander. */
function DeckSay() {
	const { t } = useTranslation();
	const { line, question } = useSayLine();
	const busy = useX00Store((store) => store.thinking || store.speaking);
	return (
		<section className="say" aria-live="polite">
			<p className="small t-2" hidden={!busy || !question} dir="auto">{question}</p>
			<p className="line" dir="auto" data-testid="home-say">{line}</p>
			<p className="small t-3">{t("deck.hint")}</p>
		</section>
	);
}

/** Every run, newest first, as the deck lists its records. */
function DeckRuns({ runs }: { runs: ClaudeOrchestratorRun[] }) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const { stateLabel } = useRunText();
	return (
		<section className="sec" id="deck-runs" data-testid="deck-runs">
			<h2 className="d1">{t("deck.runs.title")}</h2>
			<p className="lead-line">{t("deck.runs.lead")}</p>
			{runs.length === 0 ? (
				<p className="small t-2">{t("deck.runs.empty")}</p>
			) : (
				<div className="tbl-wrap">
					<table className="tbl">
						<thead>
							<tr>
								<th>{t("deck.runs.task")}</th>
								<th>{t("deck.runs.state")}</th>
								<th className="n">{t("deck.runs.progress")}</th>
								<th className="n">{t("deck.runs.cost")}</th>
							</tr>
						</thead>
						<tbody>
							{runs.slice(0, 12).map((run) => {
								const progress = runProgress(run);
								return (
									<tr key={run.runId} className="deck-row" onClick={() => void navigate({ to: "/orchestrator" })}>
										<td dir="auto">{run.title}</td>
										<td style={{ color: decisionColour(run) }}>{stateLabel(run)}</td>
										<td className="n">{progress === null ? "—" : `${progress}%`}</td>
										<td className="n">{money(run.usage?.costUsd) || "—"}</td>
									</tr>
								);
							})}
						</tbody>
					</table>
				</div>
			)}
		</section>
	);
}

/** The crew: X-00 and the sixteen writers, with what each holds in the plan in focus. */
function DeckCrewList({ lead }: { lead?: ClaudeOrchestratorRun }) {
	const { t } = useTranslation();
	const plan = useClaudeOrchestratorPlan(lead?.runId ?? "", Boolean(lead) && lead?.state !== "pending");
	const steps = plan.data?.runId === lead?.runId ? (plan.data?.steps ?? []) : [];
	return (
		<section className="sec" id="deck-crew" data-testid="deck-crew-list">
			<h2 className="d1">{t("deck.crew.title")}</h2>
			<p className="lead-line">{t("deck.crew.lead")}</p>
			<div className="crew-list">
				<button className="crew-row" type="button" onClick={openX00}>
					<span className="n">X-00</span>
					<span>{t("deck.crew.commander")}</span>
					<span className="t-2">{t("deck.crew.plansReviews")}</span>
					<span className="n t-2">—</span>
				</button>
				{Array.from({ length: 16 }, (_, index) => {
					const member = crewMember(index);
					const step = steps[index];
					return (
						<div className="crew-row deck-crew-row" key={member.callSign}>
							<span className="n">{member.callSign}</span>
							<span dir="auto">{step ? step.title : t("deck.crew.free")}</span>
							<span className="t-2">{step ? t("deck.crew.step", { n: index + 1 }) : ""}</span>
							<span className="deck-crew-face" aria-hidden="true" style={member.portrait ? { backgroundImage: `url(${member.portrait})` } : undefined} />
						</div>
					);
				})}
			</div>
		</section>
	);
}

/**
 * Under X-00, the crew at work on the run in focus: the code as it lands, the
 * map of who does what, and the page they are building, live.
 */
function DeckLiveWork({ lead }: { lead: ClaudeOrchestratorRun }) {
	const live = isLiveRun(lead);
	const board = useClaudeOrchestratorLive(lead.runId, live);
	const plan = useClaudeOrchestratorPlan(lead.runId, lead.state !== "pending");
	const work = board.data?.runId === lead.runId ? board.data : undefined;
	const steps = work?.steps ?? [];
	const files = work?.files ?? [];
	const planSteps = plan.data?.runId === lead.runId ? (plan.data?.steps ?? []) : [];
	const writing = live && (lead.state === "executing" || lead.state === "validating" || steps.some((step) => step.state === "running"));
	return (
		<div className="live-work" data-testid="deck-live-work">
			<CodeStream files={files} live={writing} />
			<AgentMap steps={steps} plan={planSteps} files={files} live={writing} />
			<LivePreview runId={lead.runId} files={files} live={live} />
		</div>
	);
}

/** The sections bar scrolls the deck to a section without touching the router's location. */
function jump(event: MouseEvent<HTMLAnchorElement>) {
	const id = event.currentTarget.hash.slice(1);
	const target = id ? document.getElementById(id) : null;
	if (!target) return;
	event.preventDefault();
	target.scrollIntoView({ behavior: "smooth", block: "start" });
}

/**
 * Home as the X-ROBOT observation deck, carried over from the trading
 * dashboard (x00.html): X-00 at full size, the run in focus beside him, the
 * crew on a rail, his last words, then the sections under the first screen.
 */
export function HomeDeck({ projects }: { projects: ReactNode }) {
	const { t } = useTranslation();
	const orchestrator = useClaudeOrchestratorInfo();
	const runsQuery = useClaudeOrchestratorRuns(orchestrator.data?.enabled === true);
	const runs = useMemo(() => runsQuery.data ?? [], [runsQuery.data]);
	const { lead, second } = useMemo(() => deckRuns(runs), [runs]);
	const liveRuns = useMemo(() => runs.filter(isLiveRun), [runs]);
	const { state, speaking } = useX00PortraitState();
	const thinking = useX00Store((store) => store.thinking);
	// The light in the room: the run in focus decides it; X-00's own work (thinking, speaking) takes over.
	const light: DeckLight = thinking || speaking ? deckLightForX00(state) : lead ? runLight(lead) : deckLightForX00(state);
	const [boot, setBoot] = useState<"boot" | "booting" | "">(() => (arrived ? "" : "boot"));

	useEffect(() => {
		if (arrived) return;
		arrived = true;
		const reduced = typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
		if (reduced) {
			setBoot("");
			return;
		}
		const start = window.setTimeout(() => setBoot("booting"), 50);
		const end = window.setTimeout(() => setBoot(""), 3_200);
		return () => {
			window.clearTimeout(start);
			window.clearTimeout(end);
		};
	}, []);

	return (
		<div className={["xrd", boot, TITLEBAR_CLUSTER && "xrd--cluster"].filter(Boolean).join(" ")} data-testid="home-deck">
			<DeckTop liveRuns={liveRuns} />
			<main className="screen">
				<button type="button" className="deck-portrait-hit" onClick={openX00} aria-label={t("x00.open")} title={t("x00.open")}>
					<DeckPortrait light={light} speaking={speaking} voice={x00VoiceLevel} />
				</button>
				<DeckFocus lead={lead} second={second} />
				<DeckCrew lead={lead} />
				<DeckSay />
			</main>
			{lead ? <DeckLiveWork lead={lead} /> : null}
			<nav className="sections small" aria-label={t("deck.sections")}>
				<a href="#deck-runs" onClick={jump}>{t("deck.runs.title")}</a>
				<a href="#deck-media" onClick={jump}>{t("deck.media.title")}</a>
				<a href="#deck-projects" onClick={jump}>{t("deck.projects")}</a>
				<a href="#deck-crew" onClick={jump}>{t("deck.crew.title")}</a>
			</nav>
			<div className="below">
				<DeckRuns runs={runs} />
				<DeckMedia />
				<section className="sec" id="deck-projects" data-testid="deck-projects">
					{projects}
				</section>
				<DeckCrewList lead={lead} />
			</div>
		</div>
	);
}
