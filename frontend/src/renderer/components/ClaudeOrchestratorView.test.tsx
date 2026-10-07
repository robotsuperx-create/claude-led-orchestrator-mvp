import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ClaudeOrchestratorInfo, ClaudeOrchestratorResult, ClaudeOrchestratorRun } from "../../shared/claude-orchestrator";
import { ClaudeOrchestratorView } from "./ClaudeOrchestratorView";

const RUN_A = "claude-run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
const RUN_B = "claude-run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
const RUN_NEW = "claude-run-cccccccccccccccccccccccccccccccc";

const enabledInfo: ClaudeOrchestratorInfo = {
	enabled: true, repository: "agent-orchestrator", plannerModel: "claude-opus-5-5", workerProvider: "deepseek", workerModel: "deepseek-chat", sandboxed: true, maxActiveRuns: 2,
};

const mocks = vi.hoisted(() => ({
	info: vi.fn(),
	list: vi.fn(),
	start: vi.fn(),
	status: vi.fn(),
	cancel: vi.fn(),
	writeText: vi.fn(async () => undefined),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		claudeOrchestrator: { info: mocks.info, list: mocks.list, start: mocks.start, status: mocks.status, cancel: mocks.cancel },
		clipboard: { writeText: mocks.writeText },
	},
}));
vi.mock("../lib/platform", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/platform")>(),
	hidesShellTopbar: () => false,
}));

const ok = <T,>(value: T): ClaudeOrchestratorResult<T> => ({ ok: true, value });

function renderView() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><ClaudeOrchestratorView /></QueryClientProvider>);
}

describe("ClaudeOrchestratorView", () => {
	beforeEach(() => {
		for (const mock of Object.values(mocks)) mock.mockReset();
		mocks.writeText.mockResolvedValue(undefined);
		mocks.info.mockResolvedValue(ok(enabledInfo));
		mocks.list.mockResolvedValue(ok([]));
	});

	it("explains how to turn the orchestrator on when the daemon has it off", async () => {
		mocks.info.mockResolvedValue(ok({ ...enabledInfo, enabled: false }));
		renderView();
		expect(await screen.findByText("The orchestrator is off")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "New run" })).toBeDisabled();
		expect(mocks.list).not.toHaveBeenCalled();
	});

	it("reports an unreachable daemon in place with a retry", async () => {
		mocks.info.mockResolvedValueOnce({ ok: false, error: "daemon_unavailable" }).mockResolvedValue(ok(enabledInfo));
		renderView();
		const alert = await screen.findByRole("alert");
		expect(alert).toHaveTextContent("Can't reach the AO daemon.");
		await userEvent.click(within(alert).getByRole("button", { name: "Retry" }));
		expect(await screen.findByText("No runs yet")).toBeInTheDocument();
	});

	it("starts a run only after the task and explicit consent", async () => {
		const started: ClaudeOrchestratorRun = { runId: RUN_NEW, state: "pending", title: "" };
		mocks.start.mockResolvedValue(ok(started));
		renderView();
		await userEvent.click(await screen.findByRole("button", { name: "Start a run" }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByText(/claude-opus-5-5 plans and reviews, and deepseek-chat writes the code/)).toBeInTheDocument();

		const startButton = within(dialog).getByRole("button", { name: "Start run" });
		expect(startButton).toBeDisabled();
		await userEvent.type(within(dialog).getByLabelText("Task"), "Add input validation");
		expect(startButton).toBeDisabled();
		await userEvent.click(within(dialog).getByRole("checkbox"));
		expect(startButton).toBeEnabled();

		mocks.list.mockResolvedValue(ok([{ ...started, state: "planning", title: "Add input validation" }]));
		await userEvent.click(startButton);
		expect(mocks.start).toHaveBeenCalledWith({ task: "Add input validation", maxRetries: 1 });
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		// The new run is listed and opened, showing its live progress.
		const row = await screen.findByRole("button", { name: "Hide details for Add input validation" });
		expect(row).toHaveAttribute("aria-expanded", "true");
		expect(await screen.findByRole("list", { name: "Run progress" })).toBeInTheDocument();
	});

	it("shows a refused start next to the action without closing the dialog", async () => {
		mocks.start.mockResolvedValue({ ok: false, error: "too_many_runs" });
		renderView();
		await userEvent.click(await screen.findByRole("button", { name: "Start a run" }));
		const dialog = await screen.findByRole("dialog");
		await userEvent.type(within(dialog).getByLabelText("Task"), "Task");
		await userEvent.click(within(dialog).getByRole("checkbox"));
		await userEvent.click(within(dialog).getByRole("button", { name: "Start run" }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent("Too many runs are active.");
		expect(screen.getByRole("dialog")).toBeInTheDocument();
	});

	it("lists runs with their stage and shows where a finished run's changes live", async () => {
		mocks.list.mockResolvedValue(ok([
			{ runId: RUN_A, state: "executing", title: "Refactor the parser", createdAt: "2026-10-07T03:00:00Z" },
			{ runId: RUN_B, state: "completed", title: "Add retries", branch: `ao/claude-orchestrator/${RUN_B}`, commit: "4e7a1c2f90abcdef", recommendation: "merge", updatedAt: "2026-10-07T02:00:00Z" },
		]));
		renderView();
		const rows = await screen.findAllByTestId("claude-orchestrator-run");
		expect(rows).toHaveLength(2);
		expect(within(rows[0]).getByText("Writing code")).toBeInTheDocument();
		expect(within(rows[1]).getByText("Ready to merge")).toBeInTheDocument();

		await userEvent.click(within(rows[1]).getByRole("button", { name: "Show details for Add retries" }));
		expect(await screen.findByTestId("claude-orchestrator-outcome")).toHaveTextContent("Claude recommends merging");
		expect(screen.getByText("4e7a1c2f90")).toBeInTheDocument();
		expect(screen.getByText(`git diff HEAD...ao/claude-orchestrator/${RUN_B}`)).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Copy branch name" }));
		expect(mocks.writeText).toHaveBeenCalledWith(`ao/claude-orchestrator/${RUN_B}`);
		// A finished run offers no cancel action.
		expect(screen.queryByRole("button", { name: "Cancel run" })).not.toBeInTheDocument();
	});

	it("cancels an active run after confirmation", async () => {
		mocks.list.mockResolvedValue(ok([{ runId: RUN_A, state: "validating", title: "Refactor the parser" }]));
		mocks.cancel.mockResolvedValue(ok({ runId: RUN_A, state: "canceled", title: "" }));
		renderView();
		await userEvent.click(await screen.findByRole("button", { name: "Show details for Refactor the parser" }));
		await userEvent.click(await screen.findByRole("button", { name: "Cancel run" }));
		const confirm = await screen.findByRole("dialog");
		expect(within(confirm).getByText("Cancel this run?")).toBeInTheDocument();
		await userEvent.click(within(confirm).getByRole("button", { name: "Cancel run" }));
		expect(mocks.cancel).toHaveBeenCalledWith(RUN_A);
	});

	it("disables new runs while the active-run limit is reached", async () => {
		mocks.list.mockResolvedValue(ok([
			{ runId: RUN_A, state: "planning", title: "One" },
			{ runId: RUN_B, state: "reviewing", title: "Two" },
		]));
		renderView();
		await screen.findAllByTestId("claude-orchestrator-run");
		await waitFor(() => expect(screen.getByRole("button", { name: "New run" })).toBeDisabled());
		expect(screen.getByRole("button", { name: "New run" })).toHaveAttribute("title", "2 runs are active. Wait for one to finish.");
	});
});
