// Shared contract for the media gateway: the person's own GPU server makes
// pictures, video and sound, and the library under the data dir keeps them.
// The renderer reaches the daemon's loopback-only /internal/media routes
// through the main process, which rebuilds every request and reply from
// allowlisted fields; the GPU server's address and token never reach it.
// Files are shown from xrobot-media://library/<id>/<name>, served by the
// main process from the library folder only.

export type MediaKind = "image" | "video" | "audio";
export const MEDIA_KINDS: readonly MediaKind[] = ["image", "video", "audio"];

export type MediaState = "scheduled" | "queued" | "running" | "done" | "failed";
const MEDIA_STATES: readonly MediaState[] = ["scheduled", "queued", "running", "done", "failed"];

export type MediaFailure = "unreachable" | "unauthorized" | "rejected" | "failed" | "timeout" | "no_output" | "too_large" | "interrupted";
const MEDIA_FAILURES: readonly MediaFailure[] = ["unreachable", "unauthorized", "rejected", "failed", "timeout", "no_output", "too_large", "interrupted"];

export type MediaSource = "app" | "x00" | "upload";

export type MediaFile = { name: string; type: string; bytes: number };

export type MediaItem = {
	id: string;
	capability: string;
	kind: MediaKind;
	prompt: string;
	width?: number;
	height?: number;
	seconds?: number;
	input?: string;
	/** When a scheduled item goes to the server (ISO time). */
	runAt?: string;
	source: MediaSource;
	state: MediaState;
	failure?: MediaFailure;
	files: MediaFile[];
	createdAt: string;
	updatedAt: string;
};

export type MediaCapability = { id: string; kind: MediaKind; title?: string; builtIn: boolean; input?: "image" };

/** Why the gateway is off; names a problem, never a value. */
export type MediaOffReason = "no_server" | "invalid_url" | "no_token" | "invalid_token";

export type MediaInfo = { enabled: boolean; reason?: MediaOffReason; voice: boolean; capabilities: MediaCapability[] };

export type MediaServerStatus = {
	reachable: boolean;
	device?: string;
	vramBytes?: number;
	queue?: number;
	onSecondsToday?: number;
	onSecondsMonth?: number;
	pricePerHour?: number;
	costToday?: number;
	costMonth?: number;
	autoPower?: boolean;
	idleMinutes?: number;
	scheduled?: number;
};

export type MediaCreateInput = {
	capability: string;
	prompt: string;
	negative?: string;
	width?: number;
	height?: number;
	seconds?: number;
	input?: string;
	source?: "app" | "x00";
	/** Variants, each with its own seed (up to MEDIA_MAX_BATCH). */
	count?: number;
	/** Hold the work until then (ISO time), up to a week ahead. */
	runAt?: string;
};

export type MediaUploadInput = { name: string; data: Uint8Array };

export type MediaErrorCode = "daemon_unavailable" | "no_server" | "invalid" | "unknown_capability" | "busy" | "not_found" | "timeout" | "failed";

export type MediaResult<T> = { ok: true; value: T } | { ok: false; error: MediaErrorCode };

// Mirrors backend/internal/service/media.
export const MEDIA_MAX_PROMPT_CHARS = 2000;
export const MEDIA_MAX_NEGATIVE_CHARS = 1000;
export const MEDIA_MAX_UPLOAD_BYTES = 20 * 1024 * 1024;
export const MEDIA_MIN_SIDE = 256;
export const MEDIA_MAX_IMAGE_SIDE = 2048;
export const MEDIA_MAX_VIDEO_SIDE = 1280;
export const MEDIA_MAX_VIDEO_SECONDS = 10;
export const MEDIA_MAX_AUDIO_SECONDS = 47;
export const MEDIA_MAX_BATCH = 30;
const MAX_LISTED = 200;

export const MEDIA_SCHEME = "xrobot-media";

const ITEM_ID = /^[0-9a-f]{32}$/;
const FILE_NAME = /^[0-9]{1,2}\.(png|jpg|jpeg|webp|gif|mp4|webm|flac|mp3|wav|opus|ogg)$/i;
const CAPABILITY_ID = /^[a-z0-9][a-z0-9-]{0,39}$/;

export function isMediaItemId(value: unknown): value is string {
	return typeof value === "string" && ITEM_ID.test(value);
}

export function isMediaFileName(value: unknown): value is string {
	return typeof value === "string" && FILE_NAME.test(value);
}

/** Where the renderer shows a library file. */
export function mediaFileUrl(itemId: string, name: string): string {
	return `${MEDIA_SCHEME}://library/${itemId}/${name}`;
}

/** Reads a library URL back into its item and file, or null. */
export function parseMediaFileUrl(raw: string): { itemId: string; name: string } | null {
	let url: URL;
	try {
		url = new URL(raw);
	} catch {
		return null;
	}
	if (url.protocol !== `${MEDIA_SCHEME}:` || url.hostname !== "library" || url.search || url.hash) return null;
	const parts = url.pathname.split("/").filter(Boolean);
	if (parts.length !== 2 || !isMediaItemId(parts[0]) || !isMediaFileName(parts[1])) return null;
	return { itemId: parts[0], name: parts[1] };
}

export const MEDIA_CONTENT_TYPES: Record<string, string> = {
	png: "image/png",
	jpg: "image/jpeg",
	jpeg: "image/jpeg",
	webp: "image/webp",
	gif: "image/gif",
	mp4: "video/mp4",
	webm: "video/webm",
	flac: "audio/flac",
	mp3: "audio/mpeg",
	wav: "audio/wav",
	opus: "audio/ogg",
	ogg: "audio/ogg",
};

function record(value: unknown): Record<string, unknown> | null {
	return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
}

function text(value: unknown, limit: number): string {
	return typeof value === "string" ? Array.from(value).slice(0, limit).join("") : "";
}

function count(value: unknown, most: number): number | undefined {
	return typeof value === "number" && Number.isInteger(value) && value > 0 && value <= most ? value : undefined;
}

function isKind(value: unknown): value is MediaKind {
	return MEDIA_KINDS.includes(value as MediaKind);
}

export function parseMediaInfo(value: unknown): MediaInfo | null {
	const body = record(value);
	if (!body || typeof body.enabled !== "boolean") return null;
	const reasons: MediaOffReason[] = ["no_server", "invalid_url", "no_token", "invalid_token"];
	const capabilities: MediaCapability[] = [];
	for (const raw of Array.isArray(body.capabilities) ? body.capabilities.slice(0, 64) : []) {
		const item = record(raw);
		if (!item || typeof item.id !== "string" || !CAPABILITY_ID.test(item.id) || !isKind(item.kind)) continue;
		const capability: MediaCapability = { id: item.id, kind: item.kind, builtIn: item.builtIn === true };
		const title = text(item.title, 80).trim();
		if (title) capability.title = title;
		if (item.input === "image") capability.input = "image";
		capabilities.push(capability);
	}
	const info: MediaInfo = { enabled: body.enabled, voice: body.voice === true, capabilities };
	if (reasons.includes(body.reason as MediaOffReason)) info.reason = body.reason as MediaOffReason;
	return info;
}

export function parseMediaStatus(value: unknown): MediaServerStatus | null {
	const body = record(value);
	if (!body || typeof body.reachable !== "boolean") return null;
	const status: MediaServerStatus = { reachable: body.reachable };
	const device = text(body.device, 80).replace(/[\u0000-\u001f\u007f]/g, "").trim();
	if (device) status.device = device;
	if (typeof body.vramBytes === "number" && Number.isFinite(body.vramBytes) && body.vramBytes > 0) status.vramBytes = body.vramBytes;
	const queue = count(body.queue, 10_000);
	if (queue) status.queue = queue;
	for (const key of ["onSecondsToday", "onSecondsMonth", "pricePerHour", "costToday", "costMonth"] as const) {
		const value = body[key];
		if (typeof value === "number" && Number.isFinite(value) && value > 0) status[key] = value;
	}
	if (body.autoPower === true) status.autoPower = true;
	const idle = count(body.idleMinutes, 240);
	if (idle) status.idleMinutes = idle;
	const scheduled = count(body.scheduled, 1_000);
	if (scheduled) status.scheduled = scheduled;
	return status;
}

export function parseMediaItem(value: unknown): MediaItem | null {
	const body = record(value);
	if (!body || !isMediaItemId(body.id) || !isKind(body.kind) || !MEDIA_STATES.includes(body.state as MediaState)) return null;
	if (typeof body.capability !== "string" || (body.capability !== "upload" && !CAPABILITY_ID.test(body.capability))) return null;
	const files: MediaFile[] = [];
	for (const raw of Array.isArray(body.files) ? body.files.slice(0, 8) : []) {
		const file = record(raw);
		if (!file || !isMediaFileName(file.name) || typeof file.type !== "string") continue;
		const extension = file.name.split(".").pop()?.toLowerCase() ?? "";
		if (MEDIA_CONTENT_TYPES[extension] !== file.type) continue;
		files.push({ name: file.name, type: file.type, bytes: typeof file.bytes === "number" && file.bytes >= 0 ? file.bytes : 0 });
	}
	const source: MediaSource = body.source === "x00" ? "x00" : body.source === "upload" ? "upload" : "app";
	const item: MediaItem = {
		id: body.id,
		capability: body.capability,
		kind: body.kind,
		prompt: text(body.prompt, MEDIA_MAX_PROMPT_CHARS),
		source,
		state: body.state as MediaState,
		files,
		createdAt: typeof body.createdAt === "string" ? body.createdAt : "",
		updatedAt: typeof body.updatedAt === "string" ? body.updatedAt : "",
	};
	const width = count(body.width, MEDIA_MAX_IMAGE_SIDE);
	const height = count(body.height, MEDIA_MAX_IMAGE_SIDE);
	const seconds = count(body.seconds, 600);
	if (width) item.width = width;
	if (height) item.height = height;
	if (seconds) item.seconds = seconds;
	if (isMediaItemId(body.input)) item.input = body.input;
	if (typeof body.runAt === "string" && !Number.isNaN(Date.parse(body.runAt))) item.runAt = body.runAt;
	if (MEDIA_FAILURES.includes(body.failure as MediaFailure)) item.failure = body.failure as MediaFailure;
	return item;
}

export function parseMediaItems(value: unknown): MediaItem[] | null {
	const body = record(value);
	if (!body || !Array.isArray(body.items)) return null;
	return body.items
		.slice(0, MAX_LISTED)
		.map(parseMediaItem)
		.filter((item): item is MediaItem => item !== null);
}

/** Rebuilds a request from allowlisted fields; null when anything is off. */
export function parseMediaCreateInput(value: unknown): MediaCreateInput | null {
	const body = record(value);
	if (!body || typeof body.capability !== "string" || !CAPABILITY_ID.test(body.capability) || typeof body.prompt !== "string") return null;
	const prompt = body.prompt.trim();
	if (!prompt || Array.from(prompt).length > MEDIA_MAX_PROMPT_CHARS) return null;
	const input: MediaCreateInput = { capability: body.capability, prompt };
	if (body.negative !== undefined) {
		if (typeof body.negative !== "string" || Array.from(body.negative).length > MEDIA_MAX_NEGATIVE_CHARS) return null;
		if (body.negative.trim()) input.negative = body.negative.trim();
	}
	for (const key of ["width", "height", "seconds"] as const) {
		if (body[key] === undefined) continue;
		const number = count(body[key], MEDIA_MAX_IMAGE_SIDE);
		if (number === undefined) return null;
		input[key] = number;
	}
	if (body.input !== undefined) {
		if (!isMediaItemId(body.input)) return null;
		input.input = body.input;
	}
	if (body.source === "x00") input.source = "x00";
	if (body.count !== undefined) {
		const batch = count(body.count, MEDIA_MAX_BATCH);
		if (batch === undefined) return null;
		input.count = batch;
	}
	if (body.runAt !== undefined) {
		if (typeof body.runAt !== "string" || Number.isNaN(Date.parse(body.runAt))) return null;
		input.runAt = new Date(body.runAt).toISOString();
	}
	return input;
}

export function parseMediaUploadInput(value: unknown): MediaUploadInput | null {
	const body = record(value);
	if (!body || typeof body.name !== "string" || !(body.data instanceof Uint8Array)) return null;
	if (body.data.byteLength === 0 || body.data.byteLength > MEDIA_MAX_UPLOAD_BYTES) return null;
	return { name: Array.from(body.name.replace(/[\u0000-\u001f\u007f]/g, "")).slice(0, 120).join(""), data: body.data };
}

/** The first file an item can be shown with, if any. */
export function mediaPreviewFile(item: MediaItem): MediaFile | undefined {
	return item.files[0];
}
