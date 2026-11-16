// What the X-ROBOT observation deck shows on Home, read from the Claude
// orchestrator's runs: the run in focus (the deck's "lead"), the one after it
// (its "second"), how far each has come, and the light in X-00's room.

import { CLAUDE_ORCHESTRATOR_ACTIVE_STAGES, type ClaudeOrchestratorRun, type ClaudeOrchestratorRunState } from "../../shared/claude-orchestrator";
import type { X00State } from "./x00-portrait";

/** X-00's light on the deck (the trading deck's portrait states). */
export type DeckLight = "watching" | "deciding" | "long" | "short" | "veto";

const STAGES: readonly ClaudeOrchestratorRunState[] = CLAUDE_ORCHESTRATOR_ACTIVE_STAGES;

/** A run that has not finished yet: queued, planning, waiting for you, or at work. */
export function isLiveRun(run: ClaudeOrchestratorRun): boolean {
	return run.state === "pending" || run.state === "awaiting_approval" || STAGES.includes(run.state);
}

function timestamp(run: ClaudeOrchestratorRun): number {
	const value = Date.parse(run.updatedAt ?? run.createdAt ?? "");
	return Number.isNaN(value) ? 0 : value;
}

/**
 * The run in focus and the one after it: live runs first, newest first, then
 * the most recently finished. No runs, no focus.
 */
export function deckRuns(runs: readonly ClaudeOrchestratorRun[]): { lead?: ClaudeOrchestratorRun; second?: ClaudeOrchestratorRun } {
	const ordered = [...runs].sort((a, b) => {
		const live = Number(isLiveRun(b)) - Number(isLiveRun(a));
		return live !== 0 ? live : timestamp(b) - timestamp(a);
	});
	return { lead: ordered[0], second: ordered[1] };
}

/**
 * Which of the four stages (plan, write, check, review) a run has reached,
 * 1-based; 0 before planning starts. Finished runs have been through all four.
 */
export function runStage(run: ClaudeOrchestratorRun): number {
	const index = STAGES.indexOf(run.state);
	if (index >= 0) return index + 1;
	if (run.state === "awaiting_approval") return 1;
	if (run.state === "completed" || run.state === "held") return STAGES.length;
	return 0;
}

export const RUN_STAGE_COUNT = STAGES.length;

/**
 * How far a run has come, in percent: the stages it has finished, and half of
 * the one it is in. A run that failed or was canceled has no progress to show.
 */
export function runProgress(run: ClaudeOrchestratorRun): number | null {
	if (run.state === "failed" || run.state === "canceled") return null;
	if (run.state === "completed" || run.state === "held") return 100;
	if (run.state === "pending") return 0;
	if (run.state === "awaiting_approval") return Math.round((0.9 / STAGES.length) * 100);
	const stage = runStage(run);
	return Math.round(((stage - 0.5) / STAGES.length) * 100);
}

/** The decision colour for a run: ion while it works, long when done, short when held, veto when it failed. */
export function runLight(run: ClaudeOrchestratorRun | undefined): DeckLight {
	if (!run) return "watching";
	if (run.state === "completed") return "long";
	if (run.state === "held") return "short";
	if (run.state === "failed") return "veto";
	if (STAGES.includes(run.state)) return "deciding";
	return "watching";
}

/** X-00's own state (the commander's portrait states) as the deck's light. */
export function deckLightForX00(state: X00State): DeckLight {
	switch (state) {
		case "working":
			return "deciding";
		case "done":
			return "long";
		case "held":
			return "short";
		case "failed":
			return "veto";
		default:
			return "watching";
	}
}

/** The colour behind each light, as the deck paints it. */
export const DECK_LIGHT_COLOUR: Record<DeckLight, string> = {
	watching: "#C6E3F0",
	deciding: "#C6E3F0",
	long: "#9FE3BD",
	short: "#F0A58F",
	veto: "#E5484D",
};

/** How a crew member's mark on the rail is lit. */
export type DeckMark = "idle" | "thinking" | "ready" | "veto";

/**
 * The rail's sixteen marks for the run in focus: the crew on its plan's steps
 * work while it is writing or checking, are lit once it has been reviewed or
 * finished, and go red when it failed. Members with no step stay dark.
 */
export function crewMarks(run: ClaudeOrchestratorRun | undefined, steps: number, size = 16): DeckMark[] {
	const on = Math.min(Math.max(0, Math.trunc(steps)), size);
	const tone: DeckMark = !run
		? "idle"
		: run.state === "failed"
			? "veto"
			: run.state === "executing" || run.state === "validating"
				? "thinking"
				: run.state === "reviewing" || run.state === "completed" || run.state === "held" || run.state === "awaiting_approval"
					? "ready"
					: "idle";
	return Array.from({ length: size }, (_, index) => (index < on ? tone : "idle"));
}

/** Minutes since a run started, for the deck's pair line; null when unknown. */
export function runMinutes(run: ClaudeOrchestratorRun, now = Date.now()): number | null {
	const started = Date.parse(run.createdAt ?? "");
	if (Number.isNaN(started)) return null;
	const end = isLiveRun(run) ? now : Date.parse(run.updatedAt ?? "") || now;
	return Math.max(0, Math.floor((end - started) / 60_000));
}

/** The rail's marks from the live board: each step lights its crew member by how it is going. */
export function crewMarksFromSteps(steps: readonly { index: number; state: string }[], size = 16): DeckMark[] {
	const marks: DeckMark[] = Array.from({ length: size }, () => "idle");
	for (const step of steps) {
		if (step.index < 0 || step.index >= size) continue;
		marks[step.index] = step.state === "running" ? "thinking" : step.state === "done" ? "ready" : step.state === "failed" ? "veto" : "idle";
	}
	return marks;
}
