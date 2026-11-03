import { useCallback, useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import type { ClaudeOrchestratorRunState } from "../../shared/claude-orchestrator";
import type { X00Action, X00Info, X00Mood, X00Screen } from "../../shared/x00";
import { aoBridge } from "../lib/bridge";
import { SpeechEnvelope, speak } from "../lib/x00-voice";
import { unavailableX00Bridge, type X00Bridge } from "../lib/x00-preview";
import { conversationForClaude, useX00Store, x00Id, type X00Proposal } from "../stores/x00-store";
import { useUiStore } from "../stores/ui-store";
import {
	useCancelClaudeOrchestratorRun,
	useClaudeOrchestratorInfo,
	useClaudeOrchestratorRuns,
	useDecideClaudeOrchestratorPlan,
	usePublishClaudeOrchestratorRun,
	useStartClaudeOrchestratorRun,
} from "./useClaudeOrchestrator";

export const x00QueryKeys = { info: ["x00", "info"] as const };

/** The Electron bridge, or the preview/unavailable stand-in outside Electron. */
function bridge(): X00Bridge {
	return (aoBridge as { x00?: X00Bridge }).x00 ?? unavailableX00Bridge;
}

/** Whether X-00 can talk, and if not, why. */
export function useX00Info() {
	return useQuery<X00Info>({
		queryKey: x00QueryKeys.info,
		queryFn: async () => {
			const result = await bridge().info();
			if (!result.ok) throw new Error(result.error);
			return result.value;
		},
		refetchInterval: (query) => (query.state.data?.enabled ? 60_000 : 10_000),
		retry: false,
	});
}

// One voice for the whole app: a new line cuts off the last.
const envelope = new SpeechEnvelope();
let stopCurrent: (() => void) | null = null;

/** How loud X-00 is right now (0..1), for his jaw. Stable across renders. */
export const x00VoiceLevel = envelope.level;

/** Reads a line aloud when the person has X-00's voice on. */
export async function x00Say(text: string, mood: X00Mood, appLocale: string): Promise<void> {
	const store = useX00Store.getState();
	if (!store.prefs.voice) return;
	stopCurrent?.();
	stopCurrent = await speak(text, {
		appLocale,
		voiceURI: store.prefs.voiceURI,
		envelope,
		onStart: () => useX00Store.getState().setSpeaking(true, mood),
		onEnd: () => useX00Store.getState().setSpeaking(false),
	});
}

/** Stops whatever X-00 is saying. */
export function x00Hush(): void {
	stopCurrent?.();
	stopCurrent = null;
	useX00Store.getState().setSpeaking(false);
}

function useCurrentScreen(): X00Screen | "other" {
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	const settingsOpen = useUiStore((state) => Boolean(state.settingsModal));
	if (settingsOpen) return "settings";
	if (pathname === "/") return "home";
	if (pathname.startsWith("/orchestrator")) return "mission_control";
	return "other";
}

/** Sends what the person said and adds X-00's answer, spoken and shown. */
export function useX00Conversation() {
	const { i18n } = useTranslation();
	const screen = useCurrentScreen();
	return useCallback(async (text: string) => {
		const content = text.trim();
		const store = useX00Store.getState();
		if (!content || store.thinking) return;
		x00Hush();
		store.add({ id: x00Id("you"), kind: "turn", role: "user", content, at: Date.now() });
		store.setThinking(true);
		const locale = i18n.resolvedLanguage ?? i18n.language ?? "en";
		const turns = conversationForClaude(useX00Store.getState().entries);
		const result = await bridge().chat({ turns, locale, screen });
		useX00Store.getState().setThinking(false);
		if (!result.ok) {
			useX00Store.getState().add({ id: x00Id("error"), kind: "error", code: result.error, at: Date.now() });
			return;
		}
		const reply = result.value;
		const proposals: X00Proposal[] = reply.actions.map((action) => ({ id: x00Id("action"), action, status: "proposed" }));
		useX00Store.getState().add({ id: x00Id("x00"), kind: "turn", role: "assistant", content: reply.say, mood: reply.mood, proposals, at: Date.now() });
		void x00Say(reply.say, reply.mood, locale);
	}, [i18n, screen]);
}

/** Runs a proposal the person confirmed, through the same calls the app's buttons use. */
export function useX00Actions() {
	const start = useStartClaudeOrchestratorRun();
	const decide = useDecideClaudeOrchestratorPlan();
	const cancel = useCancelClaudeOrchestratorRun();
	const publish = usePublishClaudeOrchestratorRun();
	const navigate = useNavigate();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	return useCallback(async (entryId: string, proposal: X00Proposal) => {
		const store = useX00Store.getState();
		store.setProposal(entryId, proposal.id, "running");
		try {
			await runAction(proposal.action);
			useX00Store.getState().setProposal(entryId, proposal.id, "done");
		} catch {
			useX00Store.getState().setProposal(entryId, proposal.id, "failed");
		}

		async function runAction(action: X00Action) {
			switch (action.type) {
				case "start_run":
					await start.mutateAsync({ task: action.task, maxRetries: 1, approvePlan: action.approvePlan });
					void navigate({ to: "/orchestrator" });
					return;
				case "approve_plan":
				case "reject_plan":
					await decide.mutateAsync({ runId: action.runId, approve: action.type === "approve_plan" });
					return;
				case "cancel_run":
					await cancel.mutateAsync(action.runId);
					return;
				case "publish_run":
					await publish.mutateAsync(action.runId);
					return;
				case "open":
					if (action.screen === "settings") openGlobalSettings("missionControl");
					else void navigate({ to: action.screen === "home" ? "/" : "/orchestrator" });
					return;
			}
		}
	}, [start, decide, cancel, publish, navigate, openGlobalSettings]);
}

type AlertKind = "awaiting" | "completed" | "held" | "failed";

const ALERT_KEYS = {
	awaiting: "x00.alert.awaiting",
	completed: "x00.alert.completed",
	held: "x00.alert.held",
	failed: "x00.alert.failed",
} as const satisfies Record<AlertKind, string>;

const ALERTS: Partial<Record<ClaudeOrchestratorRunState, { kind: AlertKind; tone: X00Mood }>> = {
	awaiting_approval: { kind: "awaiting", tone: "neutral" },
	completed: { kind: "completed", tone: "pleased" },
	held: { kind: "held", tone: "concerned" },
	failed: { kind: "failed", tone: "concerned" },
};

/**
 * X-00 keeps watch: when a run needs approval, finishes, is held, or fails, he
 * says so in the panel, aloud when alerts are on, and as a system
 * notification when the app is in the background.
 */
export function useX00Alerts() {
	const { t, i18n } = useTranslation();
	const info = useClaudeOrchestratorInfo();
	const runs = useClaudeOrchestratorRuns(info.data?.enabled === true);
	const previous = useRef<Map<string, ClaudeOrchestratorRunState> | null>(null);

	useEffect(() => {
		if (!runs.data) return;
		const before = previous.current;
		previous.current = new Map(runs.data.map((run) => [run.runId, run.state]));
		// The first list only sets the baseline: no alerts for what already happened.
		if (!before) return;
		for (const run of runs.data) {
			const was = before.get(run.runId);
			if (was === run.state) continue;
			const alert = ALERTS[run.state];
			if (!alert) continue;
			const title = (run.title || t("orchestrator.untitled")).slice(0, 120);
			const content = t(ALERT_KEYS[alert.kind], { title });
			const store = useX00Store.getState();
			store.add({ id: x00Id("alert"), kind: "alert", content, tone: alert.tone, at: Date.now() });
			if (!store.prefs.alerts) continue;
			void x00Say(content, alert.tone, i18n.resolvedLanguage ?? "en");
			notify(t("x00.alert.notificationTitle"), content);
		}
	}, [runs.data, t, i18n]);
}

function notify(title: string, body: string) {
	try {
		if (typeof document !== "undefined" && document.hasFocus() && !document.hidden) return;
		if (typeof Notification === "undefined" || Notification.permission === "denied") return;
		new Notification(title, { body, silent: true });
	} catch {
		// Notifications are a convenience; the panel already shows the alert.
	}
}
