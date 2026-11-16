import { describe, expect, it } from "vitest";
import type { ClaudeOrchestratorRun } from "../../shared/claude-orchestrator";
import { crewMarks, deckLightForX00, deckRuns, isLiveRun, runLight, runMinutes, runProgress, runStage } from "./x-robot-deck";

const run = (overrides: Partial<ClaudeOrchestratorRun>): ClaudeOrchestratorRun => ({
	runId: `claude-run-${"a".repeat(32)}`,
	state: "executing",
	title: "Fix the parser",
	createdAt: "2026-11-16T10:00:00Z",
	updatedAt: "2026-11-16T10:05:00Z",
	...overrides,
});

describe("the deck's run in focus", () => {
	it("puts live runs first, newest first, then the latest finished run", () => {
		const old = run({ runId: "a", state: "completed", updatedAt: "2026-11-16T12:00:00Z" });
		const queued = run({ runId: "b", state: "pending", updatedAt: "2026-11-16T09:00:00Z" });
		const working = run({ runId: "c", state: "executing", updatedAt: "2026-11-16T10:00:00Z" });
		expect(deckRuns([old, queued, working])).toEqual({ lead: working, second: queued });
		expect(deckRuns([old])).toEqual({ lead: old, second: undefined });
		expect(deckRuns([])).toEqual({ lead: undefined, second: undefined });
	});

	it("knows which runs are still going", () => {
		expect(["pending", "planning", "awaiting_approval", "executing", "validating", "reviewing"].every((state) => isLiveRun(run({ state: state as ClaudeOrchestratorRun["state"] })))).toBe(true);
		expect(["completed", "held", "failed", "canceled"].some((state) => isLiveRun(run({ state: state as ClaudeOrchestratorRun["state"] })))).toBe(false);
	});
});

describe("how far a run has come", () => {
	it("counts the four stages and half of the one in progress", () => {
		expect(runStage(run({ state: "planning" }))).toBe(1);
		expect(runStage(run({ state: "reviewing" }))).toBe(4);
		expect(runStage(run({ state: "pending" }))).toBe(0);
		expect(runProgress(run({ state: "pending" }))).toBe(0);
		expect(runProgress(run({ state: "planning" }))).toBe(13);
		expect(runProgress(run({ state: "executing" }))).toBe(38);
		expect(runProgress(run({ state: "validating" }))).toBe(63);
		expect(runProgress(run({ state: "reviewing" }))).toBe(88);
		expect(runProgress(run({ state: "completed" }))).toBe(100);
		expect(runProgress(run({ state: "held" }))).toBe(100);
	});

	it("shows no progress for a run that failed or was canceled", () => {
		expect(runProgress(run({ state: "failed" }))).toBeNull();
		expect(runProgress(run({ state: "canceled" }))).toBeNull();
	});

	it("measures minutes until now while live, until its last update once finished", () => {
		const now = Date.parse("2026-11-16T10:12:30Z");
		expect(runMinutes(run({ state: "executing" }), now)).toBe(12);
		expect(runMinutes(run({ state: "completed" }), now)).toBe(5);
		expect(runMinutes(run({ createdAt: undefined }), now)).toBeNull();
	});
});

describe("the light in X-00's room", () => {
	it("is the decision colour of the run in focus", () => {
		expect(runLight(undefined)).toBe("watching");
		expect(runLight(run({ state: "executing" }))).toBe("deciding");
		expect(runLight(run({ state: "completed" }))).toBe("long");
		expect(runLight(run({ state: "held" }))).toBe("short");
		expect(runLight(run({ state: "failed" }))).toBe("veto");
		expect(runLight(run({ state: "awaiting_approval" }))).toBe("watching");
	});

	it("follows X-00 himself while he thinks or speaks", () => {
		expect(deckLightForX00("working")).toBe("deciding");
		expect(deckLightForX00("done")).toBe("long");
		expect(deckLightForX00("held")).toBe("short");
		expect(deckLightForX00("failed")).toBe("veto");
		expect(deckLightForX00("watching")).toBe("watching");
	});
});

describe("the crew rail", () => {
	it("lights one mark per plan step, by what the run is doing", () => {
		expect(crewMarks(run({ state: "executing" }), 3).slice(0, 4)).toEqual(["thinking", "thinking", "thinking", "idle"]);
		expect(crewMarks(run({ state: "completed" }), 2).slice(0, 3)).toEqual(["ready", "ready", "idle"]);
		expect(crewMarks(run({ state: "failed" }), 1).slice(0, 2)).toEqual(["veto", "idle"]);
		expect(crewMarks(undefined, 5).every((mark) => mark === "idle")).toBe(true);
	});

	it("always has sixteen marks, whatever the plan's size", () => {
		expect(crewMarks(run({}), 40)).toHaveLength(16);
		expect(crewMarks(run({}), -2).every((mark) => mark === "idle")).toBe(true);
	});
});
