import {
	isMediaItemId,
	parseMediaCreateInput,
	parseMediaInfo,
	parseMediaItem,
	parseMediaItems,
	parseMediaStatus,
	parseMediaUploadInput,
	type MediaErrorCode,
	type MediaInfo,
	type MediaItem,
	type MediaResult,
	type MediaServerStatus,
} from "../shared/media";

type Fetcher = (input: string, init: RequestInit) => Promise<Response>;

const BASE_PATH = "/internal/media";
const TIMEOUT_MS = 15_000;
const UPLOAD_TIMEOUT_MS = 60_000;
const ROOT_CACHE_MS = 15_000;

/**
 * Main-process client for the media gateway's loopback-only daemon routes.
 * Requests are rebuilt field by field and replies from allowlisted fields;
 * failures are reduced to codes. The library folder is kept here, for the
 * xrobot-media: scheme, and never passed to the renderer.
 */
export class MediaClient {
	private root: { value: string | null; at: number } | null = null;

	constructor(
		private readonly origin: () => string | null,
		private readonly fetcher: Fetcher = fetch,
		private readonly now: () => number = Date.now,
	) {}

	async info(): Promise<MediaResult<MediaInfo>> {
		const response = await this.request("GET", BASE_PATH);
		if (!response.ok) {
			// An older daemon has no media routes: the gateway is simply off.
			return response.status === 404 ? { ok: true, value: { enabled: false, reason: "no_server", voice: false, capabilities: [] } } : response;
		}
		this.keepRoot(response.body);
		const info = parseMediaInfo(response.body);
		return info ? { ok: true, value: info } : { ok: false, error: "failed" };
	}

	async status(): Promise<MediaResult<MediaServerStatus>> {
		const response = await this.request("GET", `${BASE_PATH}/status`);
		if (!response.ok) return response;
		const status = parseMediaStatus(response.body);
		return status ? { ok: true, value: status } : { ok: false, error: "failed" };
	}

	async list(): Promise<MediaResult<MediaItem[]>> {
		const response = await this.request("GET", `${BASE_PATH}/items`);
		if (!response.ok) return response;
		const items = parseMediaItems(response.body);
		return items ? { ok: true, value: items } : { ok: false, error: "failed" };
	}

	async create(input: unknown): Promise<MediaResult<MediaItem>> {
		const request = parseMediaCreateInput(input);
		if (!request) return { ok: false, error: "invalid" };
		const response = await this.request("POST", `${BASE_PATH}/items`, JSON.stringify(request), "application/json");
		if (!response.ok) return response.error === "not_found" ? { ok: false, error: "unknown_capability" } : response;
		const item = parseMediaItem(response.body);
		return item ? { ok: true, value: item } : { ok: false, error: "failed" };
	}

	async upload(input: unknown): Promise<MediaResult<MediaItem>> {
		const upload = parseMediaUploadInput(input);
		if (!upload) return { ok: false, error: "invalid" };
		const response = await this.request("POST", `${BASE_PATH}/uploads?name=${encodeURIComponent(upload.name)}`, new Uint8Array(upload.data), "application/octet-stream", UPLOAD_TIMEOUT_MS);
		if (!response.ok) return response;
		const item = parseMediaItem(response.body);
		return item ? { ok: true, value: item } : { ok: false, error: "failed" };
	}

	async remove(id: unknown): Promise<MediaResult<true>> {
		if (!isMediaItemId(id)) return { ok: false, error: "invalid" };
		const response = await this.request("DELETE", `${BASE_PATH}/items/${id}`);
		return response.ok ? { ok: true, value: true } : response;
	}

	/** The library folder, for the main process's file scheme only. */
	async libraryRoot(): Promise<string | null> {
		if (this.root && this.now() - this.root.at < ROOT_CACHE_MS) return this.root.value;
		const response = await this.request("GET", BASE_PATH);
		if (response.ok) this.keepRoot(response.body);
		return this.root?.value ?? null;
	}

	private keepRoot(body: unknown): void {
		const root = body && typeof body === "object" ? (body as { root?: unknown }).root : undefined;
		this.root = { value: typeof root === "string" && root ? root : null, at: this.now() };
	}

	private async request(
		method: "GET" | "POST" | "DELETE",
		pathname: string,
		body?: string | Uint8Array<ArrayBuffer>,
		contentType?: string,
		timeoutMs = TIMEOUT_MS,
	): Promise<{ ok: true; body?: unknown } | { ok: false; error: MediaErrorCode; status?: number }> {
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
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), timeoutMs);
		try {
			const response = await this.fetcher(`${parsed.origin}${pathname}`, {
				method,
				signal: controller.signal,
				headers: contentType ? { "content-type": contentType } : undefined,
				body,
			});
			if (!response.ok) return { ok: false, error: errorForStatus(response.status), status: response.status };
			if (response.status === 204) return { ok: true };
			return { ok: true, body: await response.json() };
		} catch (error) {
			return { ok: false, error: controller.signal.aborted && error instanceof Error ? "timeout" : "daemon_unavailable" };
		} finally {
			clearTimeout(timer);
		}
	}
}

function errorForStatus(status: number): MediaErrorCode {
	switch (status) {
		case 400:
		case 413:
			return "invalid";
		case 404:
			return "not_found";
		case 429:
			return "busy";
		case 503:
			return "no_server";
		default:
			return "failed";
	}
}
