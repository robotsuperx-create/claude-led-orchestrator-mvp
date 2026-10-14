import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ClaudeOrchestratorSettingsView } from "../../../shared/claude-orchestrator-settings";
import { MissionControlSettingsSection } from "./MissionControlSettingsSection";

const mocks = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), chooseDirectory: vi.fn() }));
vi.mock("../../lib/bridge", () => ({
	aoBridge: {
		claudeOrchestratorSettings: { get: mocks.get, save: mocks.save },
		app: { chooseDirectory: mocks.chooseDirectory },
	},
}));

const empty: ClaudeOrchestratorSettingsView = { values: {}, secretsSet: {}, fromEnvironment: [] };

function renderSection() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(<QueryClientProvider client={client}><MissionControlSettingsSection /></QueryClientProvider>);
}

describe("MissionControlSettingsSection", () => {
	beforeEach(() => {
		mocks.get.mockReset();
		mocks.save.mockReset();
		mocks.chooseDirectory.mockReset();
	});

	it("refuses to turn on with missing settings and names what is missing", async () => {
		mocks.get.mockResolvedValue({ ok: true, value: empty });
		renderSection();
		await userEvent.click(await screen.findByRole("switch", { name: "Turn on Mission control" }));
		await userEvent.click(screen.getByRole("button", { name: "Save and restart" }));
		expect(mocks.save).not.toHaveBeenCalled();
		expect(screen.getByRole("alert")).toHaveTextContent("Fix the highlighted settings first.");
		expect(screen.getByLabelText("Repository")).toHaveAttribute("aria-invalid", "true");
		expect(screen.getByLabelText("Claude API key")).toHaveAttribute("aria-invalid", "true");
		expect(screen.getByLabelText("After each change")).toHaveAttribute("aria-invalid", "true");
	});

	it("saves a complete setup, turning command lines into argv and sending a new key once", async () => {
		mocks.get.mockResolvedValue({ ok: true, value: empty });
		mocks.chooseDirectory.mockResolvedValue("/work/shop");
		mocks.save.mockImplementation(async (update) => ({ ok: true, value: { values: update.values, secretsSet: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: true, AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY: true }, fromEnvironment: [] } }));
		renderSection();
		const user = userEvent.setup();
		await user.click(await screen.findByRole("switch", { name: "Turn on Mission control" }));
		await user.click(screen.getByRole("button", { name: "Choose folder" }));
		await user.type(screen.getByLabelText("Claude API address"), "https://api.anthropic.com/v1");
		await user.type(screen.getByLabelText("Claude model"), "claude-opus-5-5");
		await user.type(screen.getByLabelText("Claude API key"), "sk-claude");
		await user.type(screen.getByLabelText("DeepSeek API address"), "https://api.deepseek.com/v1");
		await user.type(screen.getByLabelText("DeepSeek model"), "deepseek-chat");
		await user.type(screen.getByLabelText("DeepSeek API key"), "sk-deepseek");
		await user.type(screen.getByLabelText("After each change"), "npm test");
		await user.type(screen.getByLabelText("Final checks"), "npm run lint{enter}npm test");
		await user.click(screen.getByRole("button", { name: "Save and restart" }));

		expect(mocks.save).toHaveBeenCalledTimes(1);
		const update = mocks.save.mock.calls[0][0];
		expect(update.values.AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT).toBe("/work/shop");
		expect(update.values.AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED).toBe("on");
		expect(JSON.parse(update.values.AO_CLAUDE_ORCHESTRATOR_VALIDATOR_COMMANDS)).toEqual([
			{ name: "npm-1", argv: ["npm", "run", "lint"] },
			{ name: "npm-2", argv: ["npm", "test"] },
		]);
		expect(update.secrets).toEqual({ AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: "sk-claude", AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY: "sk-deepseek" });
		expect(await screen.findByRole("status")).toHaveTextContent("Saved.");
		// The key field empties and keeps the saved key unless changed.
		expect(screen.getByLabelText("Claude API key")).toHaveValue("");
		expect(screen.getByLabelText("Claude API key")).toHaveAttribute("placeholder", "Leave empty to keep the saved key");
	});

	it("shows environment overrides read-only and leaves them out of a save", async () => {
		mocks.get.mockResolvedValue({ ok: true, value: {
			values: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "saved-model" },
			secretsSet: {},
			fromEnvironment: ["AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL"],
		} });
		mocks.save.mockResolvedValue({ ok: true, value: empty });
		renderSection();
		const model = await screen.findByLabelText("Claude model");
		expect(model).toBeDisabled();
		const field = model.closest(".settings-row-bar") as HTMLElement;
		expect(within(field).getByText("Set by an environment variable, which takes priority.")).toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Save and restart" }));
		expect(mocks.save.mock.calls[0][0].values).not.toHaveProperty("AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL");
	});
});
