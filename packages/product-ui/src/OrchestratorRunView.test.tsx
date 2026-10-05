import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { OrchestratorRunView, type OrchestratorRunViewLabels } from "./OrchestratorRunView";
import type { OrchestratorRunViewModel } from "./orchestrator-run-model";

const labels: OrchestratorRunViewLabels = {
	runDetails: "Run details",
	runId: "Run ID",
	statuses: { queued: "Queued", running: "Running", awaiting_consent: "Awaiting consent", on_hold: "On hold", succeeded: "Succeeded", failed: "Failed", cancelled: "Cancelled" },
	provider: "Provider",
	model: "Model",
	tokenBudget: "Token budget",
	costBudget: "Cost budget",
	consent: "Consent",
	consentStates: { required: "Consent required", granted: "Consent granted", declined: "Consent declined", not_required: "Not required" },
	merge: "Merge",
	mergeStates: { not_requested: "Not requested", waiting: "Waiting", ready: "Ready to merge", merged: "Merged", blocked: "Blocked" },
	grantConsent: "Grant consent",
	hold: "Hold",
	resume: "Resume",
	mergeAction: "Merge",
	cancel: "Cancel run",
};
const run: OrchestratorRunViewModel = {
	runId: "run-42",
	status: "running",
	provider: "claude-code",
	model: "sonnet",
	tokenBudget: { used: 1250, limit: 5000 },
	costBudgetUsd: { used: 0.4, limit: 2 },
	consent: "required",
	held: false,
	merge: "ready",
	cancelAvailable: true,
};

describe("OrchestratorRunView", () => {
	it("does not render unless a host explicitly enables the feature", () => {
		const { container } = render(<OrchestratorRunView labels={labels} run={run} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("renders only the allowlisted run summary and never surfaces credential-shaped extras", () => {
		const untrusted = { ...run, apiKey: "sk-secret-must-not-render", accessToken: "token-must-not-render" } as OrchestratorRunViewModel;
		render(<OrchestratorRunView enabled labels={labels} run={untrusted} />);
		expect(screen.getByRole("region", { name: "Run details" })).toBeInTheDocument();
		expect(screen.getByRole("status")).toHaveTextContent("Running");
		expect(screen.getByText("claude-code")).toBeInTheDocument();
		expect(screen.getByText("sonnet")).toBeInTheDocument();
		expect(screen.getByText("1,250 / 5,000")).toBeInTheDocument();
		expect(screen.getByText("$0.40 / $2.00")).toBeInTheDocument();
		expect(screen.getByText("Consent required")).toBeInTheDocument();
		expect(screen.getByText("Ready to merge")).toBeInTheDocument();
		expect(screen.queryByText(/sk-secret|token-must-not-render/)).not.toBeInTheDocument();
		expect(screen.queryByText(/apiKey|accessToken/)).not.toBeInTheDocument();
	});

	it("offers only applicable host callbacks and hides cancel unless advertised", () => {
		const onGrantConsent = vi.fn();
		const onHold = vi.fn();
		const onResume = vi.fn();
		const onMerge = vi.fn();
		const onCancel = vi.fn();
		const actions = { onGrantConsent, onHold, onResume, onMerge, onCancel };
		const { rerender } = render(<OrchestratorRunView actions={actions} enabled labels={labels} run={{ ...run, cancelAvailable: false }} />);
		fireEvent.click(screen.getByRole("button", { name: "Grant consent" }));
		fireEvent.click(screen.getByRole("button", { name: "Hold" }));
		fireEvent.click(screen.getByRole("button", { name: "Merge" }));
		expect(onGrantConsent).toHaveBeenCalledOnce();
		expect(onHold).toHaveBeenCalledOnce();
		expect(onMerge).toHaveBeenCalledOnce();
		expect(screen.queryByRole("button", { name: "Cancel run" })).not.toBeInTheDocument();
		rerender(<OrchestratorRunView actions={actions} enabled labels={labels} run={{ ...run, held: true }} />);
		fireEvent.click(screen.getByRole("button", { name: "Resume" }));
		expect(onResume).toHaveBeenCalledOnce();
		fireEvent.click(screen.getByRole("button", { name: "Cancel run" }));
		expect(onCancel).toHaveBeenCalledOnce();
	});

	it("does not offer cancel for terminal runs", () => {
		render(<OrchestratorRunView actions={{ onCancel: vi.fn() }} enabled labels={labels} run={{ ...run, status: "succeeded" }} />);
		expect(screen.queryByRole("button", { name: "Cancel run" })).not.toBeInTheDocument();
	});
});
