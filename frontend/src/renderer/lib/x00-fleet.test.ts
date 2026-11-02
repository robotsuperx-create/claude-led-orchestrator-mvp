import { describe, expect, it } from "vitest";
import type { ClaudeOrchestratorRun, ClaudeOrchestratorRunState } from "../../shared/claude-orchestrator";
import { crewMarkTone, x00StateForRuns } from "./x00-fleet";

function run(state: ClaudeOrchestratorRunState, updatedAt = "2026-10-01T00:00:00Z", runId = `run-${state}-${updatedAt}`): ClaudeOrchestratorRun {
	return { runId, state, title: state, updatedAt };
}

describe("x00StateForRuns", () => {
	it("watches when there are no runs", () => {
		expect(x00StateForRuns([])).toBe("watching");
	});

	it("works while any run is in an active stage", () => {
		for (const state of ["planning", "executing", "validating", "reviewing"] as const) {
			expect(x00StateForRuns([run("failed"), run(state)])).toBe("working");
		}
	});

	it("watches while a plan waits for approval", () => {
		expect(x00StateForRuns([run("completed"), run("awaiting_approval")])).toBe("watching");
	});

	it("takes the light of the latest finished run", () => {
		expect(x00StateForRuns([run("failed", "2026-10-01T00:00:00Z"), run("completed", "2026-10-02T00:00:00Z")])).toBe("done");
		expect(x00StateForRuns([run("completed", "2026-10-01T00:00:00Z"), run("held", "2026-10-03T00:00:00Z")])).toBe("held");
		expect(x00StateForRuns([run("held", "2026-10-01T00:00:00Z"), run("failed", "2026-10-04T00:00:00Z")])).toBe("failed");
	});

	it("ignores cancelled runs", () => {
		expect(x00StateForRuns([run("canceled")])).toBe("watching");
	});
});

describe("crewMarkTone", () => {
	it("lights active runs, marks approvals as ready, and leaves the rest idle", () => {
		expect(crewMarkTone("executing")).toBe("thinking");
		expect(crewMarkTone("awaiting_approval")).toBe("ready");
		expect(crewMarkTone("pending")).toBe("idle");
	});
});
