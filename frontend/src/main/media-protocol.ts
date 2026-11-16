import { createReadStream, promises as fs } from "node:fs";
import path from "node:path";
import { Readable } from "node:stream";
import { MEDIA_CONTENT_TYPES, MEDIA_SCHEME, parseMediaFileUrl } from "../shared/media";

/**
 * Serves the media library to the renderer on xrobot-media://library/<id>/<n>.<ext>.
 * Only a library file is ever served: the item id and file name are checked
 * by pattern, the path is resolved with every link followed and must stay
 * inside the library folder, and the type comes from the extension. Videos
 * and sound are served in ranges, so the player can seek.
 */

/** Registered before app ready, beside the renderer's own scheme. */
export const MEDIA_PRIVILEGES = {
	scheme: MEDIA_SCHEME,
	privileges: { standard: true, secure: true, supportFetchAPI: true, stream: true },
} as const;

const MAX_FILE_BYTES = 512 * 1024 * 1024;

function notFound(): Response {
	return new Response("Not found", { status: 404, headers: { "content-type": "text/plain; charset=utf-8" } });
}

function within(root: string, target: string): boolean {
	const relative = path.relative(root, target);
	return relative !== "" && !relative.startsWith("..") && !path.isAbsolute(relative);
}

/** Reads "bytes=a-b" against a file size; null when it is not one range we serve. */
export function parseRange(header: string | null, size: number): { start: number; end: number } | null | "invalid" {
	if (!header) return null;
	const match = /^bytes=(\d*)-(\d*)$/.exec(header.trim());
	if (!match || (!match[1] && !match[2])) return "invalid";
	let start: number;
	let end: number;
	if (!match[1]) {
		const suffix = Number(match[2]);
		start = Math.max(0, size - suffix);
		end = size - 1;
	} else {
		start = Number(match[1]);
		end = match[2] ? Math.min(Number(match[2]), size - 1) : size - 1;
	}
	if (!Number.isSafeInteger(start) || !Number.isSafeInteger(end) || start > end || start >= size) return "invalid";
	return { start, end };
}

/** `rootFor` is the library folder from the daemon (main process only), or null. */
export function createMediaHandler(rootFor: () => Promise<string | null>) {
	return async (request: Request): Promise<Response> => {
		if (request.method !== "GET" && request.method !== "HEAD") return new Response("Method not allowed", { status: 405 });
		const file = parseMediaFileUrl(request.url);
		if (!file) return notFound();
		const type = MEDIA_CONTENT_TYPES[file.name.split(".").pop()?.toLowerCase() ?? ""];
		const root = await rootFor();
		if (!type || !root || !path.isAbsolute(root)) return notFound();
		try {
			const realRoot = await fs.realpath(path.join(root, "items"));
			const target = await fs.realpath(path.join(realRoot, file.itemId, file.name));
			if (!within(realRoot, target)) return notFound();
			const stat = await fs.stat(target);
			if (!stat.isFile() || stat.size === 0 || stat.size > MAX_FILE_BYTES) return notFound();
			const headers: Record<string, string> = {
				"content-type": type,
				"accept-ranges": "bytes",
				"cache-control": "no-store",
				"x-content-type-options": "nosniff",
				"content-security-policy": "default-src 'none'; sandbox",
			};
			const range = parseRange(request.headers.get("range"), stat.size);
			if (range === "invalid") return new Response(null, { status: 416, headers: { "content-range": `bytes */${stat.size}` } });
			const start = range?.start ?? 0;
			const end = range?.end ?? stat.size - 1;
			headers["content-length"] = String(end - start + 1);
			if (range) headers["content-range"] = `bytes ${start}-${end}/${stat.size}`;
			const status = range ? 206 : 200;
			if (request.method === "HEAD") return new Response(null, { status, headers });
			const stream = Readable.toWeb(createReadStream(target, { start, end })) as ReadableStream<Uint8Array>;
			return new Response(stream, { status, headers });
		} catch {
			return notFound();
		}
	};
}
