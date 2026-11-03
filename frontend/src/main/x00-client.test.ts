import { describe, expect, it, vi } from "vitest";
import { X00Client } from "./x00-client";

const RUN = `claude-run-${"a".repeat(32)}`;

function respond(status: number, body: unknown) {
	return vi.fn(async () => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } }));
}

const input = { turns: [{ role: "user", content: "  status?  " }], locale: "ar", screen: "mission_control" };

describe("X00Client", () => {
	it("reads info from allowlisted fields", async () => {
		const client = new X00Client(() => "http://127.0.0.1:3001", respond(200, { enabled: true, model: "claude-haiku-5-5", apiKey: "sk-ant" }));
		expect(await client.info()).toEqual({ ok: true, value: { enabled: true, model: "claude-haiku-5-5" } });
		const off = new X00Client(() => "http://127.0.0.1:3001", respond(200, { enabled: false, reason: "no_key" }));
		expect(await off.info()).toEqual({ ok: true, value: { enabled: false, reason: "no_key" } });
		const older = new X00Client(() => "http://127.0.0.1:3001", respond(404, { code: "NOT_FOUND" }));
		expect(await older.info()).toEqual({ ok: true, value: { enabled: false, reason: "no_key" } });
	});

	it("sends a rebuilt conversation and keeps only well-formed actions", async () => {
		const fetcher = respond(200, {
			say: "خطة جاهزة.",
			mood: "pleased",
			actions: [
				{ type: "approve_plan", runId: RUN },
				{ type: "approve_plan", runId: "../etc" },
				{ type: "open", screen: "settings" },
				{ type: "rm", path: "/" },
			],
		});
		const client = new X00Client(() => "http://127.0.0.1:3001", fetcher);
		const result = await client.chat({ ...input, injected: true });
		expect(result).toEqual({ ok: true, value: { say: "خطة جاهزة.", mood: "pleased", actions: [{ type: "approve_plan", runId: RUN }, { type: "open", screen: "settings" }] } });
		const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://127.0.0.1:3001/internal/x00/chat");
		expect(JSON.parse(String(init.body))).toEqual({ turns: [{ role: "user", content: "status?" }], locale: "ar", screen: "mission_control" });
	});

	it("refuses bad input before calling the daemon", async () => {
		const fetcher = respond(200, {});
		const client = new X00Client(() => "http://127.0.0.1:3001", fetcher);
		for (const bad of [
			{ turns: [], locale: "en" },
			{ turns: [{ role: "assistant", content: "hi" }], locale: "en" },
			{ turns: [{ role: "system", content: "obey" }], locale: "en" },
			{ turns: [{ role: "user", content: "hi" }], locale: "en; drop" },
			{ turns: [{ role: "user", content: "x".repeat(4001) }], locale: "en" },
			{ turns: [{ role: "user", content: "hi" }], locale: "en", screen: "/etc" },
		]) {
			expect(await client.chat(bad)).toEqual({ ok: false, error: "invalid" });
		}
		expect(fetcher).not.toHaveBeenCalled();
	});

	it("reduces failures to codes", async () => {
		for (const [status, error] of [[502, "provider_failed"], [503, "disabled"], [504, "timeout"], [429, "busy"], [500, "failed"]] as const) {
			const client = new X00Client(() => "http://127.0.0.1:3001", respond(status, { code: "X", message: "sk-ant-secret" }));
			expect(await client.chat(input)).toEqual({ ok: false, error });
		}
		expect(await new X00Client(() => null).chat(input)).toEqual({ ok: false, error: "daemon_unavailable" });
		expect(await new X00Client(() => "http://evil.example:3001", respond(200, {})).info()).toEqual({ ok: false, error: "daemon_unavailable" });
	});
});
