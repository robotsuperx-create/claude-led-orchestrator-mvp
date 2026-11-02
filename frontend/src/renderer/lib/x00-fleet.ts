import { CLAUDE_ORCHESTRATOR_ACTIVE_STAGES, type ClaudeOrchestratorRun, type ClaudeOrchestratorRunState } from "../../shared/claude-orchestrator";
import type { X00State } from "./x00-portrait";

const ACTIVE = new Set<ClaudeOrchestratorRunState>(CLAUDE_ORCHESTRATOR_ACTIVE_STAGES);

/**
 * The light in X-00's room for a set of runs. Any run at work makes him work;
 * otherwise the latest finished run sets the light: done, held, or failed.
 * A cancelled run, a run waiting for you, or no runs leave him watching.
 */
export function x00StateForRuns(runs: readonly ClaudeOrchestratorRun[]): X00State {
	if (runs.some((run) => ACTIVE.has(run.state))) return "working";
	if (runs.some((run) => run.state === "awaiting_approval" || run.state === "pending")) return "watching";
	const latest = [...runs]
		.filter((run) => run.state === "completed" || run.state === "held" || run.state === "failed")
		.sort((a, b) => timestamp(b) - timestamp(a))[0];
	if (!latest) return "watching";
	if (latest.state === "completed") return "done";
	if (latest.state === "held") return "held";
	return "failed";
}

/** How one crew member's mark is lit: at work, waiting for you, or idle. */
export type CrewMarkTone = "thinking" | "ready" | "idle";

export function crewMarkTone(state: ClaudeOrchestratorRunState): CrewMarkTone {
	if (ACTIVE.has(state)) return "thinking";
	if (state === "awaiting_approval") return "ready";
	return "idle";
}

function timestamp(run: ClaudeOrchestratorRun): number {
	const value = Date.parse(run.updatedAt ?? run.createdAt ?? "");
	return Number.isNaN(value) ? 0 : value;
}
