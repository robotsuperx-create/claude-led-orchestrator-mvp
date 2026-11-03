import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ClaudeOrchestratorInfo, ClaudeOrchestratorRun } from "../../../shared/claude-orchestrator";
import { useUiStore } from "../../stores/ui-store";
import { useX00Store } from "../../stores/x00-store";
import { X00Commander, X00SidebarRow } from "./X00Commander";

const RUN = `claude-run-${"a".repeat(32)}`;

const mocks = vi.hoisted(() => ({
	x00Info: vi.fn(),
	x00Chat: vi.fn(),
	info: vi.fn(),
	list: vi.fn(),
	start: vi.fn(),
	approve: vi.fn(),
	reject: vi.fn(),
	cancel: vi.fn(),
	publish: vi.fn(),
	navigate: vi.fn(),
}));

vi.mock("../../lib/bridge", () => ({
	aoBridge: {
		x00: { info: mocks.x00Info, chat: mocks.x00Chat },
		claudeOrchestrator: { info: mocks.info, list: mocks.list, start: mocks.start, approve: mocks.approve, reject: mocks.reject, cancel: mocks.cancel, publish: mocks.publish },
	},
}));
vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => mocks.navigate,
	useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) => select({ location: { pathname: "/" } }),
}));

const enabledInfo: ClaudeOrchestratorInfo = {
	enabled: true, repository: "app", plannerModel: "claude-opus-5-5", workerProvider: "deepseek", workerModel: "deepseek-chat", workerMode: "model", sandboxed: false, maxActiveRuns: 2,
	plannerProvider: "claude", budgetTokens: 0, budgetUsd: 0, maxReplans: 1, canPublish: true,
};

function renderCommander() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<X00SidebarRow />
			<X00Commander />
		</QueryClientProvider>,
	);
}

describe("X00Commander", () => {
	beforeEach(() => {
		for (const mock of Object.values(mocks)) mock.mockReset();
		useX00Store.setState({ open: false, entries: [], thinking: false, speaking: false, unseen: 0, mood: "neutral", prefs: { voice: false, alerts: true, voiceURI: null } });
		useUiStore.setState({ settingsModal: null });
		mocks.x00Info.mockResolvedValue({ ok: true, value: { enabled: true, model: "claude-haiku-5-5" } });
		mocks.info.mockResolvedValue({ ok: true, value: enabledInfo });
		mocks.list.mockResolvedValue({ ok: true, value: [] });
	});

	it("asks for a Claude key before he can talk", async () => {
		mocks.x00Info.mockResolvedValue({ ok: true, value: { enabled: false, reason: "no_key" } });
		renderCommander();
		await userEvent.click(await screen.findByRole("button", { name: "Talk to X-00" }));
		expect(await screen.findByTestId("x00-setup")).toHaveTextContent("X-00 needs your Claude key");
		expect(screen.getByTestId("x00-input")).toBeDisabled();
		await userEvent.click(screen.getByRole("button", { name: "Open settings" }));
		expect(useUiStore.getState().settingsModal).toMatchObject({ scope: "global", section: "missionControl" });
	});

	it("talks through Claude and runs a proposal only after the person confirms", async () => {
		mocks.x00Chat.mockResolvedValue({
			ok: true,
			value: { say: "I have a run ready for you.", mood: "neutral", actions: [{ type: "start_run", task: "Add a health check endpoint.", approvePlan: true }] },
		});
		mocks.start.mockResolvedValue({ ok: true, value: { runId: RUN, state: "planning", title: "Add a health check endpoint." } });
		renderCommander();
		await userEvent.click(await screen.findByRole("button", { name: "Talk to X-00" }));
		const input = await screen.findByTestId("x00-input");
		await waitFor(() => expect(input).toBeEnabled());
		await userEvent.type(input, "Add a health check{Enter}");

		expect(await screen.findByText("I have a run ready for you.")).toBeInTheDocument();
		expect(mocks.x00Chat).toHaveBeenCalledWith({ turns: [{ role: "user", content: "Add a health check" }], locale: "en", screen: "home" });
		const proposal = screen.getByTestId("x00-proposal");
		expect(proposal).toHaveTextContent("Start a run");
		expect(proposal).toHaveTextContent("The plan will wait for your approval.");
		expect(mocks.start).not.toHaveBeenCalled();

		await userEvent.click(screen.getByTestId("x00-proposal-run"));
		await waitFor(() => expect(proposal).toHaveTextContent("Done"));
		expect(mocks.start).toHaveBeenCalledWith({ task: "Add a health check endpoint.", maxRetries: 1, approvePlan: true });
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/orchestrator" });
	});

	it("shows a plain error when Claude does not answer", async () => {
		mocks.x00Chat.mockResolvedValue({ ok: false, error: "provider_failed" });
		renderCommander();
		await userEvent.click(await screen.findByRole("button", { name: "Talk to X-00" }));
		const input = await screen.findByTestId("x00-input");
		await waitFor(() => expect(input).toBeEnabled());
		await userEvent.type(input, "hello{Enter}");
		expect(await screen.findByRole("alert")).toHaveTextContent("Claude did not answer.");
	});

	it("alerts when a run changes to a state that needs the person", async () => {
		const run = (state: ClaudeOrchestratorRun["state"]): ClaudeOrchestratorRun => ({ runId: RUN, state, title: "Add retries" });
		mocks.list.mockResolvedValueOnce({ ok: true, value: [run("executing")] }).mockResolvedValue({ ok: true, value: [run("completed")] });
		vi.useFakeTimers({ shouldAdvanceTime: true });
		try {
			renderCommander();
			await waitFor(() => expect(mocks.list).toHaveBeenCalledTimes(1));
			await act(async () => {
				await vi.advanceTimersByTimeAsync(2_000);
			});
			await waitFor(() => expect(useX00Store.getState().entries.some((entry) => entry.kind === "alert")).toBe(true));
		} finally {
			vi.useRealTimers();
		}
		const alert = useX00Store.getState().entries.find((entry) => entry.kind === "alert");
		expect(alert).toMatchObject({ kind: "alert", tone: "pleased", content: "“Add retries” is finished and ready for your review." });
		expect(await screen.findByTestId("x00-unseen")).toHaveTextContent("1");
	});
});
