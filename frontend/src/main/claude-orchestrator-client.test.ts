import { describe, expect, it, vi } from "vitest";
import { ClaudeOrchestratorClient } from "./claude-orchestrator-client";

const RUN_ID = "claude-run-0123456789abcdef0123456789abcdef";
const ORIGIN = "http://127.0.0.1:3001";

function jsonResponse(status: number, body: unknown): Response {
	return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function clientWith(handler: (url: string, init: RequestInit) => Response | Promise<Response>, origin: string | null = ORIGIN) {
	const fetcher = vi.fn(async (url: string, init: RequestInit) => handler(url, init));
	return { client: new ClaudeOrchestratorClient(() => origin, fetcher), fetcher };
}

describe("ClaudeOrchestratorClient", () => {
	it("starts a run with an allowlisted body and explicit opt-in", async () => {
		const { client, fetcher } = clientWith(() => jsonResponse(202, { runId: RUN_ID, state: "pending" }));
		const result = await client.start({ task: "Add a test", maxRetries: 2 });
		expect(result).toEqual({ ok: true, value: { runId: RUN_ID, state: "pending", title: "" } });
		const [url, init] = fetcher.mock.calls[0];
		expect(url).toBe(`${ORIGIN}/internal/claude-orchestrator/runs`);
		expect(init.method).toBe("POST");
		expect(JSON.parse(String(init.body))).toEqual({ task: "Add a test", maxRetries: 2, explicitOptIn: true });
		// No Origin or credential header is ever added; the daemon rejects Origin.
		expect(JSON.stringify(init.headers ?? {})).not.toMatch(/origin|authorization|cookie/i);
	});

	it("rejects invalid start input without calling the daemon", async () => {
		const { client, fetcher } = clientWith(() => jsonResponse(202, {}));
		for (const input of [
			{ task: "", maxRetries: 0 },
			{ task: "   ", maxRetries: 0 },
			{ task: "x", maxRetries: 4 },
			{ task: "x", maxRetries: 1.5 },
			{ task: "x".repeat(8 * 1024 + 1), maxRetries: 0 },
			{ task: "x", maxRetries: 0, worktreePath: "/etc" },
			{ task: "x", maxRetries: 0, explicitOptIn: false },
			null,
		]) {
			expect(await client.start(input)).toEqual({ ok: false, error: "invalid_task" });
		}
		expect(fetcher).not.toHaveBeenCalled();
	});

	it("maps daemon failures to codes and never forwards daemon text", async () => {
		const cases: Array<[number, string]> = [[429, "too_many_runs"], [400, "invalid_task"], [403, "disabled"], [409, "already_finished"], [500, "failed"]];
		for (const [status, code] of cases) {
			const { client } = clientWith(() => jsonResponse(status, { error: { message: "secret provider detail sk-123" } }));
			const result = await client.start({ task: "x", maxRetries: 0 });
			expect(result).toEqual({ ok: false, error: code });
		}
	});

	it("reports a missing feature as disabled rather than an error", async () => {
		const { client } = clientWith(() => jsonResponse(404, {}));
		expect(await client.info()).toEqual({ ok: true, value: expect.objectContaining({ enabled: false }) });
		expect(await client.list()).toEqual({ ok: false, error: "disabled" });
	});

	it("reports an unavailable or non-loopback daemon without calling it", async () => {
		for (const origin of [null, "http://example.com:3001", "https://127.0.0.1:3001", "http://user:pw@127.0.0.1:3001", "not a url"]) {
			const { client, fetcher } = clientWith(() => jsonResponse(200, {}), origin);
			expect(await client.list()).toEqual({ ok: false, error: "daemon_unavailable" });
			expect(fetcher).not.toHaveBeenCalled();
		}
		const { client } = clientWith(() => { throw new Error("ECONNREFUSED 127.0.0.1"); });
		expect(await client.list()).toEqual({ ok: false, error: "daemon_unavailable" });
	});

	it("validates run IDs before building a URL", async () => {
		const { client, fetcher } = clientWith(() => jsonResponse(200, {}));
		for (const runId of ["../shutdown", "claude-run-xyz", RUN_ID + "/cancel", 42]) {
			expect(await client.status(runId)).toEqual({ ok: false, error: "not_found" });
			expect(await client.cancel(runId)).toEqual({ ok: false, error: "not_found" });
		}
		expect(fetcher).not.toHaveBeenCalled();
	});

	it("keeps only allowlisted run fields and rejects a mismatched run", async () => {
		const { client } = clientWith((url) => url.endsWith("/runs")
			? jsonResponse(200, { runs: [{ runId: RUN_ID, state: "held", title: "Fix parser", branch: "ao/claude-orchestrator/x", commit: "abc1234", recommendation: "hold", createdAt: "2026-10-07T03:00:00Z", plan: "private", output: "private" }] })
			: jsonResponse(200, { runId: "claude-run-ffffffffffffffffffffffffffffffff", state: "completed" }));
		const list = await client.list();
		expect(list).toEqual({ ok: true, value: [{ runId: RUN_ID, state: "held", title: "Fix parser", branch: "ao/claude-orchestrator/x", commit: "abc1234", recommendation: "hold", createdAt: "2026-10-07T03:00:00Z" }] });
		expect(await client.status(RUN_ID)).toEqual({ ok: false, error: "failed" });
	});

	it("rejects malformed run lists as a whole", async () => {
		const { client } = clientWith(() => jsonResponse(200, { runs: [{ runId: RUN_ID, state: "exploded" }] }));
		expect(await client.list()).toEqual({ ok: false, error: "failed" });
	});
});
