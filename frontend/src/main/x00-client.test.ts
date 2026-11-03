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

	it("reads voice engines from the info", async () => {
		const client = new X00Client(() => "http://127.0.0.1:3001", respond(200, { enabled: false, reason: "no_key", listen: "local", speak: "cloud", extra: "x" }));
		expect(await client.info()).toEqual({ ok: true, value: { enabled: false, reason: "no_key", listen: "local", speak: "cloud" } });
		const odd = new X00Client(() => "http://127.0.0.1:3001", respond(200, { enabled: true, model: "claude-haiku-5-5", listen: "remote" }));
		expect(await odd.info()).toEqual({ ok: true, value: { enabled: true, model: "claude-haiku-5-5" } });
	});

	it("keeps the vowelled voice line of an answer", async () => {
		const client = new X00Client(() => "http://127.0.0.1:3001", respond(200, { say: "الأسطول هادئ.", voice: " الأُسْطُولُ هَادِئٌ. ", mood: "neutral", actions: [] }));
		expect(await client.chat(input)).toEqual({ ok: true, value: { say: "الأسطول هادئ.", voice: "الأُسْطُولُ هَادِئٌ.", mood: "neutral", actions: [] } });
	});

	it("sends a recording as WAV bytes and returns the transcript", async () => {
		const fetcher = respond(200, { text: "  ابدأ مهمة  ", extra: 1 });
		const client = new X00Client(() => "http://127.0.0.1:3001", fetcher);
		const audio = wav(1);
		expect(await client.listen({ audio, locale: "ar-MA" })).toEqual({ ok: true, value: { text: "ابدأ مهمة" } });
		const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://127.0.0.1:3001/internal/x00/listen?lang=ar");
		expect(init.headers).toEqual({ "content-type": "audio/wav" });
		expect(init.body).toBeInstanceOf(Uint8Array);
		expect((init.body as Uint8Array).byteLength).toBe(audio.byteLength);
		for (const bad of [{ audio: new Uint8Array(10), locale: "en" }, { audio: new TextEncoder().encode("x".repeat(100)), locale: "en" }, { audio: "RIFF", locale: "en" }, { audio, locale: "en; rm" }]) {
			expect(await client.listen(bad)).toEqual({ ok: false, error: "invalid" });
		}
		expect(fetcher).toHaveBeenCalledTimes(1);
	});

	it("returns spoken audio only when it is WAV or MP3", async () => {
		const speech = vi.fn(async () => new Response(new Uint8Array([73, 68, 51, 4]), { status: 200, headers: { "content-type": "audio/mpeg" } }));
		const client = new X00Client(() => "http://127.0.0.1:3001", speech);
		const result = await client.speak({ text: "  Hello commander  ", locale: "en" });
		expect(result.ok && result.value.type).toBe("audio/mpeg");
		expect(result.ok && Array.from(result.value.audio)).toEqual([73, 68, 51, 4]);
		const [, init] = speech.mock.calls[0] as unknown as [string, RequestInit];
		expect(JSON.parse(String(init.body))).toEqual({ text: "Hello commander", lang: "en" });
		const html = new X00Client(() => "http://127.0.0.1:3001", vi.fn(async () => new Response("<script>", { status: 200, headers: { "content-type": "text/html" } })));
		expect(await html.speak({ text: "Hi", locale: "en" })).toEqual({ ok: false, error: "failed" });
		const off = new X00Client(() => "http://127.0.0.1:3001", respond(503, { code: "X00_VOICE_UNAVAILABLE" }));
		expect(await off.speak({ text: "Hi", locale: "en" })).toEqual({ ok: false, error: "disabled" });
		expect(await client.speak({ text: "   ", locale: "en" })).toEqual({ ok: false, error: "invalid" });
		expect(await client.speak({ text: "x".repeat(3001), locale: "en" })).toEqual({ ok: false, error: "invalid" });
	});
});

/** A 16 kHz mono 16-bit WAV of the given length. */
function wav(seconds: number): Uint8Array {
	const samples = Math.round(seconds * 16000);
	const data = new Uint8Array(44 + samples * 2);
	const view = new DataView(data.buffer);
	const text = (offset: number, value: string) => [...value].forEach((char, index) => view.setUint8(offset + index, char.charCodeAt(0)));
	text(0, "RIFF");
	view.setUint32(4, 36 + samples * 2, true);
	text(8, "WAVEfmt ");
	view.setUint32(16, 16, true);
	view.setUint16(20, 1, true);
	view.setUint16(22, 1, true);
	view.setUint32(24, 16000, true);
	view.setUint32(28, 32000, true);
	view.setUint16(32, 2, true);
	view.setUint16(34, 16, true);
	text(36, "data");
	view.setUint32(40, samples * 2, true);
	return data;
}
