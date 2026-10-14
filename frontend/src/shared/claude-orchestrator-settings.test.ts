import { describe, expect, it } from "vitest";
import { commandsFromText, commandsToText, parseClaudeOrchestratorSettingsUpdate, splitCommandLine } from "./claude-orchestrator-settings";

describe("Mission control settings helpers", () => {
	it("splits command lines like a person types them", () => {
		expect(splitCommandLine("go test ./...")).toEqual(["go", "test", "./..."]);
		expect(splitCommandLine(`npm run "test:unit" -- --grep 'two words'`)).toEqual(["npm", "run", "test:unit", "--", "--grep", "two words"]);
		expect(splitCommandLine(`echo ""`)).toEqual(["echo", ""]);
		expect(splitCommandLine(`echo "open`)).toBeNull();
	});

	it("turns one command per line into the daemon's argv list and back", () => {
		const { json } = commandsFromText("npm ci\n\n  npm test -- --grep 'two words'  \n");
		expect(JSON.parse(json as string)).toEqual([
			{ name: "npm-1", argv: ["npm", "ci"] },
			{ name: "npm-2", argv: ["npm", "test", "--", "--grep", "two words"] },
		]);
		expect(commandsToText(json as string)).toBe(`npm ci\nnpm test -- --grep "two words"`);
		expect(commandsFromText("   ")).toEqual({ json: "", error: null });
		expect(commandsFromText(`go test "x`)).toEqual({ json: null, error: "unterminated_quote" });
		expect(commandsToText("not json")).toBe("");
	});

	it("accepts only allowlisted keys and well-formed values", () => {
		expect(parseClaudeOrchestratorSettingsUpdate({
			values: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: " claude-opus-5-5 " },
			secrets: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: "key", AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY: null },
		})).toEqual({
			values: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "claude-opus-5-5" },
			secrets: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: "key", AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY: null },
		});
		for (const bad of [
			null,
			{ values: { AO_DATA_DIR: "/tmp" } },
			{ values: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: "key" } },
			{ values: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "a\nb" } },
			{ values: {}, secrets: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL: "x" } },
			{ values: {}, secrets: { AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY: "   " } },
			{ values: {}, extra: true },
		]) {
			expect(parseClaudeOrchestratorSettingsUpdate(bad)).toBeNull();
		}
		expect(parseClaudeOrchestratorSettingsUpdate({ values: { AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS: "[]\n" } })).not.toBeNull();
	});
});
