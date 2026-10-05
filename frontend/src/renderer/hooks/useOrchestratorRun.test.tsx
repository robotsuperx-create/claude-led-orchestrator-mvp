import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { OrchestratorRunClient } from "./useOrchestratorRun";
import { useOrchestratorRun } from "./useOrchestratorRun";

function makeClient(): OrchestratorRunClient {
	return {
		startRun: vi.fn().mockResolvedValue({ runId: "run-1", state: "pending", apiKey: "must-not-leak" }),
		getRunStatus: vi.fn().mockResolvedValue({ runId: "run-1", state: "executing", accessToken: "must-not-leak" }),
		cancelRun: vi.fn().mockResolvedValue({ runId: "run-1", state: "cancelled", credential: "must-not-leak" }),
	};
}

describe("useOrchestratorRun", () => {
	it("does not start unless both the feature and explicit consent are enabled", async () => {
		const client = makeClient();
		const { result, rerender } = renderHook(
			({ enabled, consentGranted }: { enabled: boolean; consentGranted: boolean }) =>
				useOrchestratorRun({ client, enabled, consentGranted }),
			{ initialProps: { enabled: false, consentGranted: true } },
		);

		await act(async () => { await result.current.startRun({ task: "build" }); });
		expect(client.startRun).not.toHaveBeenCalled();
		expect(result.current.error).toBe("disabled");

		rerender({ enabled: true, consentGranted: false });
		await act(async () => { await result.current.startRun({ task: "build" }); });
		expect(client.startRun).not.toHaveBeenCalled();
		expect(result.current.error).toBe("consent_required");
	});

	it("sends only explicit task fields, polls status, and strips response extras", async () => {
		const client = makeClient();
		const { result } = renderHook(() => useOrchestratorRun({ client, enabled: true, consentGranted: true, pollIntervalMs: 5 }));

		await act(async () => {
			await result.current.startRun({ task: "build", maxRetries: 1 });
		});
		expect(client.startRun).toHaveBeenCalledOnce();
		const [request, options] = vi.mocked(client.startRun).mock.calls[0]!;
		expect(request).toEqual({ task: "build", maxRetries: 1, explicitOptIn: true });
		expect(request).not.toHaveProperty("credentials");
		expect(request).not.toHaveProperty("apiKey");
		expect(options).toHaveProperty("signal");
		expect(result.current.run).toMatchObject({ runId: "run-1", status: "queued" });
		expect(result.current.run).not.toHaveProperty("apiKey");

		await waitFor(() => expect(client.getRunStatus).toHaveBeenCalled());
		expect(result.current.run).toMatchObject({ runId: "run-1", status: "running" });
		expect(result.current.run).not.toHaveProperty("accessToken");
	});

	it("sends cancel only when enabled and advertised, independently of start consent", async () => {
		const client = makeClient();
		const { result, rerender } = renderHook(
			({ consentGranted }: { consentGranted: boolean }) => useOrchestratorRun({
				client,
				enabled: true,
				consentGranted,
				cancelEnabled: true,
				pollIntervalMs: 60_000,
			}),
			{ initialProps: { consentGranted: true } },
		);
		await act(async () => { await result.current.startRun({ task: "build" }); });
		rerender({ consentGranted: false });

		let cancelled = false;
		await act(async () => { cancelled = await result.current.cancelRun(); });
		expect(cancelled).toBe(true);
		expect(client.cancelRun).toHaveBeenCalledOnce();
		expect(client.cancelRun).toHaveBeenCalledWith("run-1", expect.objectContaining({ signal: expect.any(AbortSignal) }));
		expect(result.current.run?.status).toBe("cancelled");
	});
});
