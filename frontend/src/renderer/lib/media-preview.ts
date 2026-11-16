import { mediaFileUrl, type MediaCreateInput, type MediaInfo, type MediaItem, type MediaResult, type MediaServerStatus, type MediaUploadInput } from "../../shared/media";

/** The bridge surface the renderer depends on (matches preload's shape). */
export type MediaBridge = {
	info: () => Promise<MediaResult<MediaInfo>>;
	status: () => Promise<MediaResult<MediaServerStatus>>;
	list: () => Promise<MediaResult<MediaItem[]>>;
	create: (input: MediaCreateInput) => Promise<MediaResult<MediaItem>>;
	upload: (input: MediaUploadInput) => Promise<MediaResult<MediaItem>>;
	remove: (id: string) => Promise<MediaResult<true>>;
	fileUrl: (itemId: string, name: string) => string;
};

/** Used outside Electron when no preview data is wanted (tests, plain web). */
export const unavailableMediaBridge: MediaBridge = {
	info: async () => ({ ok: true, value: { enabled: false, reason: "no_server", voice: false, capabilities: [] } }),
	status: async () => ({ ok: true, value: { reachable: false } }),
	list: async () => ({ ok: true, value: [] }),
	create: async () => ({ ok: false, error: "no_server" }),
	upload: async () => ({ ok: false, error: "daemon_unavailable" }),
	remove: async () => ({ ok: false, error: "daemon_unavailable" }),
	fileUrl: mediaFileUrl,
};

const PREVIEW_CAPABILITIES: MediaInfo["capabilities"] = [
	{ id: "image", kind: "image", builtIn: true, title: "Picture (FLUX.1 schnell)" },
	{ id: "video", kind: "video", builtIn: true, title: "Short video (Wan 2.1)" },
	{ id: "music", kind: "audio", builtIn: true, title: "Music and sound effects (Stable Audio Open)" },
];

/** A picture for the preview: the deck's own dark gradient, a different hue per item. */
function previewPicture(seed: number): string {
	const hue = 170 + (seed % 7) * 22;
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><defs><radialGradient id="g" cx="${30 + (seed % 5) * 10}%" cy="35%" r="80%"><stop offset="0" stop-color="hsl(${hue} 45% 62%)"/><stop offset=".55" stop-color="hsl(${hue + 20} 30% 18%)"/><stop offset="1" stop-color="#050607"/></radialGradient></defs><rect width="512" height="512" fill="url(#g)"/><circle cx="256" cy="300" r="${90 + (seed % 3) * 20}" fill="none" stroke="hsl(${hue} 50% 80% / .35)" stroke-width="2"/></svg>`;
	return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
}

/**
 * A scripted gateway for the browser preview build (dev:web), which has no
 * daemon or GPU server. The packaged app never uses it.
 */
export function createMediaPreview(now: () => number = Date.now): MediaBridge {
	const at = (minutes: number) => new Date(now() - minutes * 60_000).toISOString();
	const item = (index: number, overrides: Partial<MediaItem>): MediaItem => ({
		id: index.toString(16).padStart(32, "0"),
		capability: "image",
		kind: "image",
		prompt: "",
		source: "app",
		state: "done",
		files: [{ name: "1.png", type: "image/png", bytes: 1_200_000 }],
		createdAt: at(index * 7),
		updatedAt: at(index * 7),
		...overrides,
	});
	let items: MediaItem[] = [
		item(1, { capability: "video", kind: "video", prompt: "Slow orbit around a black robot commander on a starship bridge, cinematic light", state: "running", files: [], seconds: 4 }),
		item(2, { prompt: "Hero image for a smart window tint store: a car at dusk, glass turning dark", source: "x00", width: 1024, height: 1024 }),
		item(3, { prompt: "Minimal product shot of a leather handbag on graphite stone, soft rim light" }),
		item(4, { capability: "music", kind: "audio", prompt: "Calm ambient pulse for a tech reel, 90 bpm", files: [{ name: "1.flac", type: "audio/flac", bytes: 900_000 }], seconds: 20 }),
		item(5, { prompt: "Oujda medina at golden hour, wide angle" }),
		item(6, { prompt: "Kids coloring page, friendly robot astronaut", state: "failed", failure: "rejected", files: [] }),
	];
	const pictures = new Map(items.map((entry, index) => [entry.id, previewPicture(index)]));
	return {
		info: async () => ({ ok: true, value: { enabled: true, voice: true, capabilities: PREVIEW_CAPABILITIES } }),
		status: async () => ({ ok: true, value: { reachable: true, device: "NVIDIA GeForce RTX 4090", vramBytes: 25_757_220_864, queue: 1 } }),
		list: async () => ({ ok: true, value: items }),
		create: async (input) => {
			const capability = PREVIEW_CAPABILITIES.find((entry) => entry.id === input.capability);
			if (!capability) return { ok: false, error: "unknown_capability" };
			const created = item(items.length + 20, { capability: capability.id, kind: capability.kind, prompt: input.prompt, state: "queued", files: [], createdAt: new Date(now()).toISOString() });
			pictures.set(created.id, previewPicture(items.length));
			items = [created, ...items];
			return { ok: true, value: created };
		},
		upload: async (input) => {
			const created = item(items.length + 40, { capability: "upload", source: "upload", prompt: input.name, createdAt: new Date(now()).toISOString() });
			pictures.set(created.id, previewPicture(items.length + 3));
			items = [created, ...items];
			return { ok: true, value: created };
		},
		remove: async (id) => {
			items = items.filter((entry) => entry.id !== id);
			return { ok: true, value: true };
		},
		fileUrl: (itemId) => pictures.get(itemId) ?? previewPicture(0),
	};
}
