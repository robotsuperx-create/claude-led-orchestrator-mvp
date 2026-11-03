import { describe, expect, it } from "vitest";
import { conversationForClaude, type X00Entry } from "./x00-store";

const turn = (role: "user" | "assistant", content: string, id = `${role}-${content}`): X00Entry => ({ id, kind: "turn", role, content, at: 0 });

describe("conversationForClaude", () => {
	it("sends only the person's and X-00's lines, ending with the person", () => {
		const entries: X00Entry[] = [
			turn("user", "hello"),
			{ id: "a", kind: "alert", content: "Run failed", tone: "concerned", at: 0 },
			turn("assistant", "Greetings."),
			{ id: "e", kind: "error", code: "timeout", at: 0 },
			turn("user", "status?"),
		];
		expect(conversationForClaude(entries)).toEqual([
			{ role: "user", content: "hello" },
			{ role: "assistant", content: "Greetings." },
			{ role: "user", content: "status?" },
		]);
	});

	it("drops a trailing answer so the last turn is the person's", () => {
		expect(conversationForClaude([turn("user", "a"), turn("assistant", "b")])).toEqual([{ role: "user", content: "a" }]);
	});

	it("keeps within the daemon's limits by dropping the oldest turns", () => {
		const entries = Array.from({ length: 40 }, (_, index) => turn(index % 2 ? "assistant" : "user", `line ${index}`, String(index)));
		entries.push(turn("user", "x".repeat(5000), "long"));
		const turns = conversationForClaude(entries);
		expect(turns.length).toBeLessThanOrEqual(24);
		expect(turns.at(-1)?.role).toBe("user");
		expect(turns.at(-1)?.content.length).toBe(4000);
		expect(turns.reduce((sum, item) => sum + item.content.length, 0)).toBeLessThanOrEqual(24000);
	});
});
