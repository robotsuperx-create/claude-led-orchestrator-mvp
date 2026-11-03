import { create } from "zustand";
import type { X00Action, X00Mood, X00Turn } from "../../shared/x00";
import { X00_MAX_TOTAL_CHARS, X00_MAX_TURN_CHARS, X00_MAX_TURNS } from "../../shared/x00";

/** One proposal on screen, and what became of it. */
export type X00Proposal = { id: string; action: X00Action; status: "proposed" | "running" | "done" | "failed" | "dismissed" };

/** A line in X-00's panel. Alerts and errors are local: they are never sent to Claude. */
export type X00Entry =
	| { id: string; kind: "turn"; role: X00Turn["role"]; content: string; mood?: X00Mood; proposals?: X00Proposal[]; at: number }
	| { id: string; kind: "alert"; content: string; tone: X00Mood; at: number }
	| { id: string; kind: "error"; code: string; at: number };

export type X00Prefs = { voice: boolean; alerts: boolean; voiceURI: string | null };

type X00Store = {
	open: boolean;
	entries: X00Entry[];
	thinking: boolean;
	speaking: boolean;
	/** Listening to the person through the microphone, or turning it into text. */
	hearing: "idle" | "listening" | "transcribing";
	mood: X00Mood;
	unseen: number;
	prefs: X00Prefs;
	setOpen: (open: boolean) => void;
	toggle: () => void;
	add: (entry: X00Entry) => void;
	setProposal: (entryId: string, proposalId: string, status: X00Proposal["status"]) => void;
	setThinking: (thinking: boolean) => void;
	setSpeaking: (speaking: boolean, mood?: X00Mood) => void;
	setHearing: (hearing: "idle" | "listening" | "transcribing") => void;
	setPrefs: (prefs: Partial<X00Prefs>) => void;
	clear: () => void;
};

const STORAGE_KEY = "x-robot.x00.v1";
const MAX_ENTRIES = 60;
const DEFAULT_PREFS: X00Prefs = { voice: true, alerts: true, voiceURI: null };

type Saved = { entries?: unknown; prefs?: unknown };

// Per-viewer convenience only; the panel works the same when storage is unavailable.
function readSaved(): { entries: X00Entry[]; prefs: X00Prefs } {
	try {
		const raw = window.localStorage.getItem(STORAGE_KEY);
		if (!raw) return { entries: [], prefs: DEFAULT_PREFS };
		const saved = JSON.parse(raw) as Saved;
		const prefs = saved.prefs && typeof saved.prefs === "object" ? (saved.prefs as Partial<X00Prefs>) : {};
		const entries = Array.isArray(saved.entries) ? (saved.entries as X00Entry[]).filter(isEntry).slice(-MAX_ENTRIES) : [];
		return {
			entries,
			prefs: {
				voice: typeof prefs.voice === "boolean" ? prefs.voice : DEFAULT_PREFS.voice,
				alerts: typeof prefs.alerts === "boolean" ? prefs.alerts : DEFAULT_PREFS.alerts,
				voiceURI: typeof prefs.voiceURI === "string" ? prefs.voiceURI : null,
			},
		};
	} catch {
		return { entries: [], prefs: DEFAULT_PREFS };
	}
}

function isEntry(value: unknown): value is X00Entry {
	if (!value || typeof value !== "object") return false;
	const entry = value as Record<string, unknown>;
	if (typeof entry.id !== "string" || typeof entry.at !== "number") return false;
	if (entry.kind === "turn") return (entry.role === "user" || entry.role === "assistant") && typeof entry.content === "string";
	if (entry.kind === "alert") return typeof entry.content === "string";
	return entry.kind === "error" && typeof entry.code === "string";
}

function save(entries: X00Entry[], prefs: X00Prefs) {
	try {
		// A proposal left mid-run when the app closed is shown as not done.
		const settled = entries.slice(-MAX_ENTRIES).map((entry) =>
			entry.kind === "turn" && entry.proposals
				? { ...entry, proposals: entry.proposals.map((proposal) => (proposal.status === "running" ? { ...proposal, status: "failed" as const } : proposal)) }
				: entry,
		);
		window.localStorage.setItem(STORAGE_KEY, JSON.stringify({ entries: settled, prefs }));
	} catch {
		// Storage can be unavailable; the conversation simply is not kept.
	}
}

const initial = typeof window === "undefined" ? { entries: [], prefs: DEFAULT_PREFS } : readSaved();

export const useX00Store = create<X00Store>((set, get) => ({
	open: false,
	entries: initial.entries,
	thinking: false,
	speaking: false,
	hearing: "idle",
	mood: "neutral",
	unseen: 0,
	prefs: initial.prefs,
	setOpen: (open) => set({ open, unseen: open ? 0 : get().unseen }),
	toggle: () => {
		const open = !get().open;
		set({ open, unseen: open ? 0 : get().unseen });
	},
	add: (entry) => {
		const entries = [...get().entries, entry].slice(-MAX_ENTRIES);
		const counts = entry.kind === "alert" || (entry.kind === "turn" && entry.role === "assistant");
		set({ entries, unseen: !get().open && counts ? get().unseen + 1 : get().unseen });
		save(entries, get().prefs);
	},
	setProposal: (entryId, proposalId, status) => {
		const entries = get().entries.map((entry) =>
			entry.kind === "turn" && entry.id === entryId && entry.proposals
				? { ...entry, proposals: entry.proposals.map((proposal) => (proposal.id === proposalId ? { ...proposal, status } : proposal)) }
				: entry,
		);
		set({ entries });
		save(entries, get().prefs);
	},
	setThinking: (thinking) => set({ thinking }),
	setSpeaking: (speaking, mood) => set({ speaking, ...(mood ? { mood } : {}) }),
	setHearing: (hearing) => set({ hearing }),
	setPrefs: (prefs) => {
		const next = { ...get().prefs, ...prefs };
		set({ prefs: next });
		save(get().entries, next);
	},
	clear: () => {
		set({ entries: [], unseen: 0 });
		save([], get().prefs);
	},
}));

/**
 * The conversation Claude sees: the person's and X-00's own lines only, newest
 * last, trimmed to the daemon's limits, always ending with the person.
 */
export function conversationForClaude(entries: readonly X00Entry[]): X00Turn[] {
	const turns: X00Turn[] = [];
	for (const entry of entries) {
		if (entry.kind !== "turn") continue;
		const content = Array.from(entry.content.trim()).slice(0, X00_MAX_TURN_CHARS).join("");
		if (content) turns.push({ role: entry.role, content });
	}
	while (turns.length > 0 && turns[turns.length - 1].role !== "user") turns.pop();
	let total = turns.reduce((sum, turn) => sum + Array.from(turn.content).length, 0);
	while (turns.length > X00_MAX_TURNS || total > X00_MAX_TOTAL_CHARS) {
		const dropped = turns.shift();
		if (!dropped) break;
		total -= Array.from(dropped.content).length;
	}
	return turns;
}

let counter = 0;
/** A short unique id for panel entries. */
export function x00Id(prefix: string): string {
	counter += 1;
	return `${prefix}-${Date.now().toString(36)}-${counter.toString(36)}`;
}
