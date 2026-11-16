import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ClaudeOrchestratorLiveFile, ClaudeOrchestratorLiveStep, ClaudeOrchestratorRun } from "../../../shared/claude-orchestrator";
import { useX00Store } from "../../stores/x00-store";
import { HomeDeck } from "./HomeDeck";

const mocks = vi.hoisted(() => ({
	navigate: vi.fn(),
	openX00: vi.fn(),
	cancel: vi.fn(),
	runs: [] as ClaudeOrchestratorRun[],
	steps: [] as Array<{ id: string; title: string; instructions: string; dependsOn: string[] }>,
	enabled: true,
	live: null as null | { steps: ClaudeOrchestratorLiveStep[]; files: ClaudeOrchestratorLiveFile[] },
	pages: [] as string[],
}));

vi.mock("@tanstack/react-router", async (importOriginal) => ({
	...(await importOriginal<typeof import("@tanstack/react-router")>()),
	useNavigate: () => mocks.navigate,
}));
vi.mock("../x00/X00Commander", () => ({
	openX00: mocks.openX00,
	useX00PortraitState: () => ({ state: "watching", speaking: false }),
}));
vi.mock("../../hooks/useMedia", () => ({
	useMediaInfo: () => ({ data: undefined }),
	useMediaServerStatus: () => ({ data: undefined }),
	useMediaItems: () => ({ data: [] }),
	useCreateMedia: () => ({ mutate: vi.fn(), isPending: false, error: null }),
	useUploadMedia: () => ({ mutate: vi.fn(), isPending: false, error: null }),
	useRemoveMedia: () => ({ mutate: vi.fn() }),
	mediaErrorCode: () => null,
	mediaUrl: () => "",
}));
vi.mock("../../hooks/useClaudeOrchestrator", () => ({
	useClaudeOrchestratorInfo: () => ({ data: { enabled: mocks.enabled, plannerProvider: "claude", plannerModel: "claude-opus" } }),
	useClaudeOrchestratorRuns: () => ({ data: mocks.runs }),
	useClaudeOrchestratorPlan: (runId: string) => ({ data: { runId, summary: "", steps: mocks.steps } }),
	useCancelClaudeOrchestratorRun: () => ({ mutate: mocks.cancel }),
	useClaudeOrchestratorLive: (runId: string) => ({ data: mocks.live ? { ...mocks.live, runId } : undefined }),
	useClaudeOrchestratorSitePages: (runId: string) => ({ data: { runId, pages: mocks.pages } }),
	useClaudeOrchestratorVisual: () => ({ data: undefined }),
	useClaudeOrchestratorVisualShot: () => ({ data: undefined }),
}));
vi.mock("../../lib/shell-context", () => ({ useShell: () => ({ daemonStatus: { state: "ready" } }) }));
vi.mock("./DeckPortrait", () => ({
	DeckPortrait: ({ light }: { light: string }) => <div data-testid="deck-portrait" data-light={light} />,
}));

const run = (overrides: Partial<ClaudeOrchestratorRun>): ClaudeOrchestratorRun => ({
	runId: `claude-run-${"a".repeat(32)}`,
	state: "executing",
	title: "Fix the CSV parser",
	createdAt: new Date(Date.now() - 12 * 60_000).toISOString(),
	updatedAt: new Date().toISOString(),
	...overrides,
});

beforeEach(() => {
	mocks.navigate.mockReset();
	mocks.openX00.mockReset();
	mocks.cancel.mockReset();
	mocks.runs = [];
	mocks.steps = [];
	mocks.enabled = true;
	mocks.live = null;
	mocks.pages = [];
	useX00Store.setState({ entries: [], speaking: false, thinking: false });
});

describe("HomeDeck", () => {
	it("waits for a first run, with X-00's greeting and the stop switch off", () => {
		render(<HomeDeck projects={null} />);
		expect(screen.getByText("No run yet")).toBeInTheDocument();
		expect(screen.getByTestId("home-say")).toHaveTextContent("At your command, commander.");
		expect(screen.getByTestId("deck-stop-all")).toBeDisabled();
		expect(screen.getByTestId("deck-live")).toHaveTextContent("Connected · Claude");
		expect(screen.getByTestId("deck-portrait")).toHaveAttribute("data-light", "watching");
	});

	it("puts the run in focus beside X-00: how far it has come, its state, and what comes next", () => {
		mocks.runs = [
			run({ runId: "second", state: "reviewing", title: "Pricing page", usage: { inputTokens: 1, outputTokens: 1, calls: 1, costUsd: 0.42 } }),
			run({ runId: "lead", state: "executing", title: "Fix the CSV parser", branch: "x-robot/fix-csv", usage: { inputTokens: 1, outputTokens: 1, calls: 1, costUsd: 1.12 } }),
		];
		mocks.runs[0].updatedAt = new Date(Date.now() - 60_000).toISOString();
		render(<HomeDeck projects={null} />);
		const focus = screen.getByTestId("deck-focus");
		expect(within(focus).getByTestId("deck-lead-name")).toHaveTextContent("Fix the CSV parser");
		expect(within(focus).getAllByText("38%").length).toBeGreaterThan(0);
		expect(within(focus).getByText("Stage 2 of 4")).toBeInTheDocument();
		expect(within(focus).getByText("12 min")).toBeInTheDocument();
		expect(within(focus).getByText("1.12$")).toBeInTheDocument();
		expect(within(focus).getByText("x-robot/fix-csv")).toBeInTheDocument();
		expect(within(screen.getByTestId("deck-decision")).getByText("Writing code")).toBeInTheDocument();
		expect(within(focus).getByText("Next: Check")).toBeInTheDocument();
		expect(within(screen.getByTestId("deck-second")).getByText("Pricing page")).toBeInTheDocument();
		expect(screen.getByTestId("deck-portrait")).toHaveAttribute("data-light", "deciding");
	});

	it("lights the crew on the plan's steps while they write", () => {
		mocks.runs = [run({ state: "executing" })];
		mocks.steps = [
			{ id: "s1", title: "Read the parser", instructions: "", dependsOn: [] },
			{ id: "s2", title: "Fix the quoting", instructions: "", dependsOn: ["s1"] },
		];
		render(<HomeDeck projects={null} />);
		const rail = screen.getByTestId("deck-crew");
		expect(within(rail).getByRole("button", { name: /X-01/ })).toHaveAttribute("data-tone", "thinking");
		expect(within(rail).getByRole("button", { name: /X-02/ })).toHaveAttribute("title", "X-02 · Fix the quoting");
		expect(within(rail).getByRole("button", { name: /X-03/ })).toHaveAttribute("data-tone", "idle");
		expect(within(rail).getAllByRole("button")).toHaveLength(16);
		expect(within(screen.getByTestId("deck-crew-list")).getByText("Fix the quoting")).toBeInTheDocument();
	});

	it("goes green when the run in focus is done and red-dark when it failed", () => {
		mocks.runs = [run({ state: "completed", pullRequestUrl: "https://github.com/o/r/pull/1" })];
		const { unmount } = render(<HomeDeck projects={null} />);
		expect(screen.getByTestId("deck-portrait")).toHaveAttribute("data-light", "long");
		expect(screen.getByText("Done; the pull request is open.")).toBeInTheDocument();
		unmount();
		mocks.runs = [run({ state: "failed" })];
		render(<HomeDeck projects={null} />);
		expect(screen.getByTestId("deck-portrait")).toHaveAttribute("data-light", "veto");
		expect(screen.getByText("It failed. Open it in Mission control to see why.")).toBeInTheDocument();
	});

	it("says what X-00 said last", () => {
		render(<HomeDeck projects={null} />);
		act(() => {
			useX00Store.setState({
				entries: [
					{ id: "q", kind: "turn", role: "user", content: "How is the parser run?", at: Date.now() },
					{ id: "a", kind: "turn", role: "assistant", content: "X-03 is writing its tests.", mood: "neutral", at: Date.now() },
				],
			});
		});
		expect(screen.getByTestId("home-say")).toHaveTextContent("X-03 is writing its tests.");
	});

	it("stops every live run only on a second press", () => {
		mocks.runs = [run({ runId: "one", state: "executing" }), run({ runId: "two", state: "awaiting_approval" }), run({ runId: "three", state: "completed" })];
		render(<HomeDeck projects={null} />);
		const stop = screen.getByTestId("deck-stop-all");
		fireEvent.click(stop);
		expect(mocks.cancel).not.toHaveBeenCalled();
		expect(stop).toHaveTextContent("Press again to stop 2");
		fireEvent.click(stop);
		expect(mocks.cancel.mock.calls.map(([id]) => id).sort()).toEqual(["one", "two"]);
		expect(stop).toHaveTextContent("Stop all runs");
	});

	it("opens a new run in Mission control and talks to X-00 from the header", () => {
		render(<HomeDeck projects={<p>projects here</p>} />);
		fireEvent.click(screen.getByTestId("deck-new-run"));
		expect(mocks.navigate).toHaveBeenCalledWith({ to: "/orchestrator" });
		fireEvent.click(screen.getByTestId("home-talk"));
		expect(mocks.openX00).toHaveBeenCalled();
		expect(screen.getByTestId("deck-projects")).toHaveTextContent("projects here");
	});

	it("shows the orchestrator off when it is not enabled", () => {
		mocks.enabled = false;
		render(<HomeDeck projects={null} />);
		expect(screen.getByTestId("deck-live")).toHaveTextContent("Mission control is off");
	});

	it("shows the crew at work under X-00: the code as it lands, the map, and the page they build", () => {
		mocks.runs = [run({ state: "executing" })];
		mocks.steps = [
			{ id: "read", title: "Read the parser", instructions: "", dependsOn: [] },
			{ id: "fix", title: "Fix the quoting", instructions: "", dependsOn: ["read"] },
		];
		mocks.live = {
			steps: [
				{ index: 0, id: "read", title: "Read the parser", state: "done", attempt: 1 },
				{ index: 1, id: "fix", title: "Fix the quoting", state: "running", attempt: 2 },
			],
			files: [
				{ path: "src/parser.ts", added: 3, removed: 1, created: false, lines: ["if (quoted) {", "  return field;", "}"] },
				{ path: "src/parser.test.ts", added: 1, removed: 0, created: true, lines: ["it(\"keeps quotes\", () => {});"] },
			],
		};
		mocks.pages = ["index.html", "docs/about.html"];
		render(<HomeDeck projects={null} />);

		const code = screen.getByTestId("deck-code-stream");
		expect(code).toHaveAttribute("data-live", "true");
		expect(within(screen.getByTestId("deck-code-lines")).getByText("return field;", { exact: false })).toBeInTheDocument();
		fireEvent.click(within(code).getByRole("tab", { name: /src\/parser\.test\.ts/ }));
		expect(within(screen.getByTestId("deck-code-lines")).getByText("it(\"keeps quotes\", () => {});")).toBeInTheDocument();

		const map = screen.getByTestId("deck-crew-map");
		expect(within(map).getByTestId("deck-map-node-1")).toHaveAttribute("data-state", "running");
		expect(within(screen.getByTestId("deck-map-card")).getByText("Fix the quoting")).toBeInTheDocument();
		expect(within(screen.getByTestId("deck-map-card")).getByText(/attempt 2/)).toBeInTheDocument();

		const frame = screen.getByTestId("deck-preview-frame");
		expect(frame).toHaveAttribute("src", "xrobot-site://run-" + "a".repeat(32) + "/index.html");
		expect(frame).toHaveAttribute("sandbox", "allow-scripts allow-same-origin");
		fireEvent.click(within(screen.getByTestId("deck-live-preview")).getByRole("tab", { name: "docs/about.html" }));
		expect(screen.getByTestId("deck-preview-frame")).toHaveAttribute("src", "xrobot-site://run-" + "a".repeat(32) + "/docs/about.html");

		// The rail follows the live board: X-01 is done, X-02 is at work.
		const rail = screen.getByTestId("deck-crew");
		expect(within(rail).getByRole("button", { name: /X-01/ })).toHaveAttribute("data-tone", "ready");
		expect(within(rail).getByRole("button", { name: /X-02/ })).toHaveAttribute("data-tone", "thinking");
	});
});
