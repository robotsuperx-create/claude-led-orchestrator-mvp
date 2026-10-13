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
	enabled: true, repository: "agent-orchestrator", plannerModel: "claude-opus-5-5", workerProvider: "deepseek", workerModel: "deepseek-chat", workerMode: "model", sandboxed: true, maxActiveRuns: 2,
	plannerProvider: "claude", budgetTokens: 2_000_000, budgetUsd: 0, maxReplans: 1, canPublish: true,
};

const mocks = vi.hoisted(() => ({
	info: vi.fn(),
	list: vi.fn(),
	start: vi.fn(),
	status: vi.fn(),
	cancel: vi.fn(),
	plan: vi.fn(),
	approve: vi.fn(),
	reject: vi.fn(),
	publish: vi.fn(),
	writeText: vi.fn(async () => undefined),
	openExternal: vi.fn(async () => undefined),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		claudeOrchestrator: { info: mocks.info, list: mocks.list, start: mocks.start, status: mocks.status, cancel: mocks.cancel, plan: mocks.plan, approve: mocks.approve, reject: mocks.reject, publish: mocks.publish },
		clipboard: { writeText: mocks.writeText },
		app: { openExternal: mocks.openExternal },
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
		mocks.openExternal.mockResolvedValue(undefined);
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
		await userEvent.click(within(dialog).getByRole("checkbox", { name: /Send this task/ }));
		expect(startButton).toBeEnabled();

		mocks.list.mockResolvedValue(ok([{ ...started, state: "planning", title: "Add input validation" }]));
		await userEvent.click(startButton);
		expect(mocks.start).toHaveBeenCalledWith({ task: "Add input validation", maxRetries: 1, approvePlan: true });
		await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
		// The new run is listed and opened, showing its live progress.
		const row = await screen.findByRole("button", { name: "Hide details for Add input validation" });
		expect(row).toHaveAttribute("aria-expanded", "true");
		expect(await screen.findByRole("list", { name: "Run progress" })).toBeInTheDocument();
	});

	it("says AO agents write the code in agents mode", async () => {
		mocks.info.mockResolvedValue(ok({ ...enabledInfo, workerMode: "agents", workerModel: "deepseek-harness" }));
		renderView();
		expect(await screen.findByTestId("claude-orchestrator-configuration")).toHaveTextContent("Agents write code with deepseek-harness");
		await userEvent.click(await screen.findByRole("button", { name: "Start a run" }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByText(/AO agents \(deepseek-harness by default\) write the code/)).toBeInTheDocument();
		expect(within(dialog).getByText(/start AO agent sessions/)).toBeInTheDocument();
	});

	it("shows a refused start next to the action without closing the dialog", async () => {
		mocks.start.mockResolvedValue({ ok: false, error: "too_many_runs" });
		renderView();
		await userEvent.click(await screen.findByRole("button", { name: "Start a run" }));
		const dialog = await screen.findByRole("dialog");
		await userEvent.type(within(dialog).getByLabelText("Task"), "Task");
		await userEvent.click(within(dialog).getByRole("checkbox", { name: /Send this task/ }));
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

	it("shows mission control with one satellite per live run and the run tally", async () => {
		mocks.list.mockResolvedValue(ok([
			{ runId: RUN_A, state: "executing", title: "One" },
			{ runId: RUN_B, state: "validating", title: "Two" },
			{ runId: RUN_NEW, state: "held", title: "Three" },
		]));
		renderView();
		const mission = await screen.findByTestId("claude-orchestrator-mission");
		await waitFor(() => expect(within(mission).getAllByTestId("orbit-satellite")).toHaveLength(2));
		expect(within(mission).getByRole("img", { name: "Active runs: 2" })).toBeInTheDocument();
		expect(within(mission).getByTestId("mission-active")).toHaveTextContent("2/2");
		expect(within(mission).getByTestId("mission-held")).toHaveTextContent("1");
		expect(within(mission).getByTestId("mission-done")).toHaveTextContent("0");
	});

	it("explains a run that stopped because the app closed", async () => {
		mocks.list.mockResolvedValue(ok([{ runId: RUN_A, state: "failed", interrupted: true, title: "Refactor the parser" }]));
		renderView();
		const row = await screen.findByTestId("claude-orchestrator-run");
		expect(within(row).getByText("Stopped when the app closed")).toBeInTheDocument();
		await userEvent.click(within(row).getByRole("button", { name: "Show details for Refactor the parser" }));
		expect(await screen.findByTestId("claude-orchestrator-outcome")).toHaveTextContent("The app closed while this run was active");
	});

	it("shows Claude's plan for approval and approves it", async () => {
		mocks.list.mockResolvedValue(ok([{ runId: RUN_A, state: "awaiting_approval", title: "Add validation" }]));
		mocks.plan.mockResolvedValue(ok({
			runId: RUN_A,
			summary: "Validate on both sides.",
			steps: [
				{ id: "schema", title: "Add a schema", instructions: "Shared rules.", provider: "claude", dependsOn: [] },
				{ id: "form", title: "Validate the form", instructions: "Field errors.", provider: "deepseek", dependsOn: ["schema"] },
			],
		}));
		mocks.approve.mockResolvedValue(ok({ runId: RUN_A, state: "planning", title: "" }));
		renderView();
		const row = await screen.findByTestId("claude-orchestrator-run");
		expect(within(row).getByText("Waiting for your approval")).toBeInTheDocument();
		await userEvent.click(within(row).getByRole("button", { name: "Show details for Add validation" }));
		const plan = await screen.findByTestId("claude-orchestrator-plan");
		expect(await within(plan).findByText("Validate the form")).toBeInTheDocument();
		expect(within(plan).getByText("After Add a schema")).toBeInTheDocument();
		expect(within(plan).getByText("Written by DeepSeek")).toBeInTheDocument();
		await userEvent.click(within(plan).getByRole("button", { name: "Approve and run" }));
		expect(mocks.approve).toHaveBeenCalledWith(RUN_A);
		expect(mocks.reject).not.toHaveBeenCalled();
	});

	it("rejects a plan", async () => {
		mocks.list.mockResolvedValue(ok([{ runId: RUN_A, state: "awaiting_approval", title: "Add validation" }]));
		mocks.plan.mockResolvedValue(ok({ runId: RUN_A, summary: "", steps: [{ id: "a", title: "Only step", instructions: "", dependsOn: [] }] }));
		mocks.reject.mockResolvedValue(ok({ runId: RUN_A, state: "canceled", title: "" }));
		renderView();
		await userEvent.click(await screen.findByRole("button", { name: "Show details for Add validation" }));
		const plan = await screen.findByTestId("claude-orchestrator-plan");
		await userEvent.click(await within(plan).findByRole("button", { name: "Reject plan" }));
		expect(mocks.reject).toHaveBeenCalledWith(RUN_A);
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

	it("shows a run's usage against its budget and why a spent budget stopped it", async () => {
		mocks.list.mockResolvedValue(ok([
			{ runId: RUN_A, state: "failed", title: "Big refactor", usage: { inputTokens: 1_950_000, outputTokens: 60_000, calls: 22, costUsd: 6.75, budgetTokens: 2_000_000, exhausted: true } },
		] satisfies ClaudeOrchestratorRun[]));
		renderView();
		expect(await screen.findByText("Budget 2M tokens per run")).toBeInTheDocument();
		await userEvent.click(await screen.findByRole("button", { name: /Big refactor/ }));
		const usage = await screen.findByTestId("claude-orchestrator-usage");
		expect(usage).toHaveTextContent("2M in · 60K out · 22 calls");
		expect(usage).toHaveTextContent("about $6.75");
		expect(within(usage).getByRole("meter")).toHaveAttribute("aria-valuenow", "100");
		expect(screen.getByTestId("claude-orchestrator-outcome")).toHaveTextContent("used up its model budget");
	});

	it("publishes a finished run only after confirmation and links the pull request", async () => {
		const branch = `ao/claude-orchestrator/${RUN_A}`;
		const finished: ClaudeOrchestratorRun = { runId: RUN_A, state: "completed", title: "Add retries", branch, commit: "4e7a1c2f90", recommendation: "merge", replans: 1 };
		mocks.list.mockResolvedValue(ok([finished]));
		mocks.publish.mockResolvedValue(ok({ ...finished, pullRequestUrl: "https://github.com/acme/app/pull/7" }));
		renderView();
		await userEvent.click(await screen.findByRole("button", { name: /Add retries/ }));
		expect(screen.getByText("Plan revisions")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Publish for review" }));
		expect(mocks.publish).not.toHaveBeenCalled();
		const dialog = await screen.findByRole("dialog");
		expect(dialog).toHaveTextContent(branch);
		expect(dialog).toHaveTextContent("This pushes the run's branch to your Git remote and opens a pull request.");
		mocks.list.mockResolvedValue(ok([{ ...finished, pullRequestUrl: "https://github.com/acme/app/pull/7" }]));
		await userEvent.click(within(dialog).getByRole("button", { name: "Push and open pull request" }));
		expect(mocks.publish).toHaveBeenCalledWith(RUN_A);
		await userEvent.click(await screen.findByRole("button", { name: "Open pull request" }));
		expect(mocks.openExternal).toHaveBeenCalledWith("https://github.com/acme/app/pull/7");
		expect(screen.queryByRole("button", { name: "Publish for review" })).not.toBeInTheDocument();
	});

	it("does not offer publishing when the daemon cannot publish or nothing changed", async () => {
		mocks.info.mockResolvedValue(ok({ ...enabledInfo, canPublish: false }));
		mocks.list.mockResolvedValue(ok([
			{ runId: RUN_A, state: "completed", title: "Add retries", branch: `ao/claude-orchestrator/${RUN_A}`, commit: "4e7a1c2f90", recommendation: "merge" },
		] satisfies ClaudeOrchestratorRun[]));
		renderView();
		await userEvent.click(await screen.findByRole("button", { name: /Add retries/ }));
		expect(screen.queryByRole("button", { name: "Publish for review" })).not.toBeInTheDocument();
	});
});
