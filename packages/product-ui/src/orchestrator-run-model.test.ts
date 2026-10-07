import { describe, expect, it } from "vitest";
import { isTerminalOrchestratorRunState, toOrchestratorRunViewModel } from "./orchestrator-run-model";

describe("orchestrator run view-model adapter", () => {
	it("maps lifecycle states into the display-safe allowlist", () => {
		const model = toOrchestratorRunViewModel({ runId: "run-1", state: "executing" }, { cancelAvailable: true });
		expect(model).toEqual({
			runId: "run-1",
			status: "running",
			consent: "not_required",
			held: false,
			merge: "not_requested",
			cancelAvailable: true,
		});
		expect("credentials" in model).toBe(false);
	});

	it("marks terminal runs and never advertises their cancel action", () => {
		expect(isTerminalOrchestratorRunState("completed")).toBe(true);
		expect(toOrchestratorRunViewModel({ runId: "run-2", state: "failed" }, { cancelAvailable: true }).cancelAvailable).toBe(false);
		expect(isTerminalOrchestratorRunState("held")).toBe(true);
		// The daemon spells the wire state "canceled".
		expect(isTerminalOrchestratorRunState("canceled")).toBe(true);
		expect(toOrchestratorRunViewModel({ runId: "run-4", state: "canceled" })).toMatchObject({ status: "cancelled", cancelAvailable: false });
		expect(toOrchestratorRunViewModel({ runId: "run-3", state: "held" }, { cancelAvailable: true })).toMatchObject({
			status: "on_hold",
			cancelAvailable: false,
		});
	});
});
