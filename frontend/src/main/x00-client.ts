import {
	parseX00Heard,
	parseX00Info,
	parseX00Reply,
	validX00ChatInput,
	validX00ListenInput,
	validX00SpeakInput,
	x00SpeechLang,
	X00_MAX_SPEECH_BYTES,
	type X00ErrorCode,
	type X00Heard,
	type X00Info,
	type X00Reply,
	type X00Result,
	type X00Speech,
} from "../shared/x00";

type Fetcher = (input: string, init: RequestInit) => Promise<Response>;

const BASE_PATH = "/internal/x00";
const INFO_TIMEOUT_MS = 5_000;
// Claude answers in seconds; the daemon gives up after 70 s.
const CHAT_TIMEOUT_MS = 80_000;
// A local model on a slow CPU may take a while; the daemon gives up after 90 s.
const LISTEN_TIMEOUT_MS = 100_000;
const SPEAK_TIMEOUT_MS = 70_000;

/**
 * Main-process client for X-00's loopback-only daemon routes. The renderer
 * reaches it over IPC. Requests are rebuilt field by field and replies from
 * allowlisted fields; failures are reduced to codes, so no daemon or provider
 * text, and never the Claude key, reaches the renderer.
 */
export class X00Client {
	constructor(private readonly origin: () => string | null, private readonly fetcher: Fetcher = fetch) {}

	async info(): Promise<X00Result<X00Info>> {
		const response = await this.request("GET", BASE_PATH, undefined, INFO_TIMEOUT_MS);
		if (!response.ok) {
			// An older daemon has no X-00 routes: he simply cannot talk yet.
			return response.error === "failed" && response.status === 404 ? { ok: true, value: { enabled: false, reason: "no_key" } } : response;
		}
		const info = parseX00Info(response.body);
		return info ? { ok: true, value: info } : { ok: false, error: "failed" };
	}

	async chat(input: unknown): Promise<X00Result<X00Reply>> {
		if (!validX00ChatInput(input)) return { ok: false, error: "invalid" };
		const response = await this.request("POST", `${BASE_PATH}/chat`, {
			turns: input.turns.map((turn) => ({ role: turn.role, content: turn.content.trim() })),
			locale: input.locale,
			...(input.screen ? { screen: input.screen } : {}),
		}, CHAT_TIMEOUT_MS);
		if (!response.ok) return { ok: false, error: response.error };
		const reply = parseX00Reply(response.body);
		return reply ? { ok: true, value: reply } : { ok: false, error: "failed" };
	}

	/** Turns a recording into text with X-00's speech engine. */
	async listen(input: unknown): Promise<X00Result<X00Heard>> {
		if (!validX00ListenInput(input)) return { ok: false, error: "invalid" };
		const lang = x00SpeechLang(input.locale);
		const response = await this.request("POST", `${BASE_PATH}/listen${lang ? `?lang=${lang}` : ""}`, new Uint8Array(input.audio), LISTEN_TIMEOUT_MS);
		if (!response.ok) return { ok: false, error: response.error };
		const heard = parseX00Heard(response.body);
		return heard ? { ok: true, value: heard } : { ok: false, error: "failed" };
	}

	/** Voices one line; the audio comes back as bytes for the renderer to play. */
	async speak(input: unknown): Promise<X00Result<X00Speech>> {
		if (!validX00SpeakInput(input)) return { ok: false, error: "invalid" };
		const lang = x00SpeechLang(input.locale);
		const response = await this.request("POST", `${BASE_PATH}/speak`, { text: input.text.trim(), ...(lang ? { lang } : {}) }, SPEAK_TIMEOUT_MS, "audio");
		if (!response.ok) return { ok: false, error: response.error };
		return response.audio ? { ok: true, value: response.audio } : { ok: false, error: "failed" };
	}

	private async request(
		method: "GET" | "POST",
		pathname: string,
		body: object | Uint8Array<ArrayBuffer> | undefined,
		timeoutMs: number,
		expect: "json" | "audio" = "json",
	): Promise<{ ok: true; body?: unknown; audio?: X00Speech } | { ok: false; error: X00ErrorCode; status?: number }> {
		const base = this.origin();
		if (!base) return { ok: false, error: "daemon_unavailable" };
		let parsed: URL;
		try {
			parsed = new URL(base);
		} catch {
			return { ok: false, error: "daemon_unavailable" };
		}
		if (parsed.protocol !== "http:" || parsed.hostname !== "127.0.0.1" || parsed.username || parsed.password || parsed.pathname !== "/" || parsed.search || parsed.hash) {
			return { ok: false, error: "daemon_unavailable" };
		}
		const binary = body instanceof Uint8Array;
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), timeoutMs);
		try {
			const response = await this.fetcher(`${parsed.origin}${pathname}`, {
				method,
				signal: controller.signal,
				headers: body ? { "content-type": binary ? "audio/wav" : "application/json" } : undefined,
				body: body ? (binary ? (body as Uint8Array<ArrayBuffer>) : JSON.stringify(body)) : undefined,
			});
			if (!response.ok) return { ok: false, error: errorForStatus(response.status), status: response.status };
			if (expect === "audio") return { ok: true, audio: await readSpeech(response) };
			return { ok: true, body: await response.json() };
		} catch (error) {
			return { ok: false, error: controller.signal.aborted && error instanceof Error ? "timeout" : "daemon_unavailable" };
		} finally {
			clearTimeout(timer);
		}
	}
}

/** Reads spoken audio, refusing anything that is not WAV or MP3 within the limit. */
async function readSpeech(response: Response): Promise<X00Speech | undefined> {
	const contentType = (response.headers.get("content-type") ?? "").split(";")[0].trim().toLowerCase();
	const type = contentType === "audio/wav" || contentType === "audio/x-wav" ? "audio/wav" : contentType === "audio/mpeg" ? "audio/mpeg" : null;
	if (!type) return undefined;
	const declared = Number(response.headers.get("content-length") ?? "0");
	if (declared > X00_MAX_SPEECH_BYTES) return undefined;
	const data = new Uint8Array(await response.arrayBuffer());
	if (data.byteLength === 0 || data.byteLength > X00_MAX_SPEECH_BYTES) return undefined;
	return { audio: data, type };
}

function errorForStatus(status: number): X00ErrorCode {
	switch (status) {
		case 400:
		case 413:
			return "invalid";
		case 429:
			return "busy";
		case 502:
			return "provider_failed";
		case 503:
			return "disabled";
		case 504:
			return "timeout";
		default:
			return "failed";
	}
}
