import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { OrchestratorRunClient } from "../hooks/useOrchestratorRun";
import { OrchestratorRunPanel, type OrchestratorRunPanelLabels } from "./OrchestratorRunPanel";

const labels: OrchestratorRunPanelLabels = {
	run: {
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
	},
	task: "Task",
	taskPlaceholder: "Describe a task",
	consentStatement: "Review and explicitly consent before starting this run.",
	consentCheckbox: "I consent to start this task",
	start: "Start run",
	starting: "Starting…",
	errorMessages: {
		disabled: "Runs are disabled.",
		consent_required: "Consent is required.",
		invalid_task: "Enter a valid task.",
		request_failed: "The run request failed.",
	},
};

function makeClient(): OrchestratorRunClient {
	return {
		startRun: vi.fn().mockResolvedValue({ runId: "run-1", state: "executing", apiKey: "secret-start" }),
		getRunStatus: vi.fn().mockResolvedValue({ runId: "run-1", state: "executing", accessToken: "secret-status" }),
		cancelRun: vi.fn().mockResolvedValue({ runId: "run-1", state: "cancelled", credential: "secret-cancel" }),
	};
}

describe("OrchestratorRunPanel", () => {
	it("is hidden by default and requires an explicit feature gate", () => {
		const client = makeClient();
		const { container } = render(<OrchestratorRunPanel client={client} labels={labels} />);
		expect(container).toBeEmptyDOMElement();
		expect(client.startRun).not.toHaveBeenCalled();
	});

	it("requires per-run consent before starting, polls through the injected client, and strips response extras", async () => {
		const client = makeClient();
		render(<OrchestratorRunPanel client={client} enabled labels={labels} pollIntervalMs={5} />);
		const start = screen.getByRole("button", { name: "Start run" });
		fireEvent.change(screen.getByPlaceholderText("Describe a task"), { target: { value: "Build tests" } });
		expect(start).toBeDisabled();
		fireEvent.click(screen.getByRole("checkbox", { name: "I consent to start this task" }));
		expect(start).toBeEnabled();
		fireEvent.click(start);

		await waitFor(() => expect(client.startRun).toHaveBeenCalledOnce());
		const [request, requestOptions] = vi.mocked(client.startRun).mock.calls[0]!;
		expect(request).toEqual({ task: "Build tests", explicitOptIn: true });
		expect(request).not.toHaveProperty("apiKey");
		expect(request).not.toHaveProperty("credentials");
		expect(requestOptions.signal).toBeInstanceOf(AbortSignal);
		await waitFor(() => expect(client.getRunStatus).toHaveBeenCalled());
		expect(await screen.findByText("Consent granted")).toBeInTheDocument();
		expect(screen.getByRole("status")).toHaveTextContent("Running");
		expect(screen.queryByText(/secret-start|secret-status|accessToken|apiKey/)).not.toBeInTheDocument();
		expect(screen.getByRole("checkbox", { name: "I consent to start this task" })).not.toBeChecked();
		expect(screen.getByRole("button", { name: "Start run" })).toBeDisabled();
	});

	it("forwards host hold/merge actions and uses hook cancellation only when enabled", async () => {
		const client = makeClient();
		const onHold = vi.fn();
		const onMerge = vi.fn();
		render(
			<OrchestratorRunPanel
				actions={{ onHold, onMerge }}
				cancelEnabled
				client={client}
				enabled
				labels={labels}
				pollIntervalMs={60_000}
				presentation={{ merge: "ready" }}
			/>,
		);
		fireEvent.change(screen.getByPlaceholderText("Describe a task"), { target: { value: "Build tests" } });
		fireEvent.click(screen.getByRole("checkbox", { name: "I consent to start this task" }));
		fireEvent.click(screen.getByRole("button", { name: "Start run" }));
		await screen.findByRole("region", { name: "Run details" });

		fireEvent.click(screen.getByRole("button", { name: "Hold" }));
		fireEvent.click(screen.getByRole("button", { name: "Merge" }));
		expect(onHold).toHaveBeenCalledOnce();
		expect(onMerge).toHaveBeenCalledOnce();
		fireEvent.click(screen.getByRole("button", { name: "Cancel run" }));
		await waitFor(() => expect(client.cancelRun).toHaveBeenCalledWith("run-1", expect.objectContaining({ signal: expect.any(AbortSignal) })));
	});
});
