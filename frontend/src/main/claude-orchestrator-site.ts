import { promises as fs } from "node:fs";
import path from "node:path";
import { CLAUDE_ORCHESTRATOR_SITE_SCHEME, claudeOrchestratorSiteRunId } from "../shared/claude-orchestrator";

/**
 * Serves a run's own web pages to the deck's live preview on
 * xrobot-site://run-<id>/<page>. Each run is its own origin, distinct from
 * the renderer's and the daemon's, so a page the crew wrote can neither read
 * the app nor call the daemon: the daemon rejects any request that carries an
 * Origin, and the policy below forbids plain-http connections outright. Only
 * files inside the run's worktree are served, by extension, never a listing.
 */

/** Registered before app ready, beside the renderer's own scheme. */
export const CLAUDE_ORCHESTRATOR_SITE_PRIVILEGES = {
	scheme: CLAUDE_ORCHESTRATOR_SITE_SCHEME,
	privileges: { standard: true, secure: true, supportFetchAPI: true, stream: true },
} as const;

const MAX_FILE_BYTES = 20 * 1024 * 1024;

const CONTENT_TYPES: Record<string, string> = {
	".html": "text/html; charset=utf-8",
	".htm": "text/html; charset=utf-8",
	".css": "text/css; charset=utf-8",
	".js": "text/javascript; charset=utf-8",
	".mjs": "text/javascript; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".map": "application/json; charset=utf-8",
	".txt": "text/plain; charset=utf-8",
	".svg": "image/svg+xml",
	".png": "image/png",
	".jpg": "image/jpeg",
	".jpeg": "image/jpeg",
	".gif": "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".ico": "image/x-icon",
	".woff": "font/woff",
	".woff2": "font/woff2",
	".ttf": "font/ttf",
	".otf": "font/otf",
	".mp4": "video/mp4",
	".webm": "video/webm",
	".mp3": "audio/mpeg",
	".wav": "audio/wav",
};

/**
 * What a crew-written page may load: its own files, and https. Never plain
 * http, so never the daemon on loopback; never a form post; never a frame of
 * the app.
 */
export const CLAUDE_ORCHESTRATOR_SITE_POLICY = [
	"default-src 'self' https: data: blob:",
	"script-src 'self' 'unsafe-inline' 'unsafe-eval' https:",
	"style-src 'self' 'unsafe-inline' https:",
	"img-src 'self' https: data: blob:",
	"font-src 'self' https: data:",
	"connect-src 'self' https:",
	"media-src 'self' https: data: blob:",
	"form-action 'none'",
	"base-uri 'self'",
	"object-src 'none'",
].join("; ");

function notFound(): Response {
	return new Response("Not found", { status: 404, headers: { "content-type": "text/plain; charset=utf-8" } });
}

/** True when `target` (with every link resolved) is `root` itself or inside it. */
function within(root: string, target: string): boolean {
	const relative = path.relative(root, target);
	return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

/**
 * The handler for the scheme. `rootFor` tells where a run's worktree is (from
 * the daemon, in the main process only), or null when the run has no pages.
 */
export function createClaudeOrchestratorSiteHandler(rootFor: (runId: string) => Promise<string | null>) {
	return async (request: Request): Promise<Response> => {
		if (request.method !== "GET" && request.method !== "HEAD") return new Response("Method not allowed", { status: 405 });
		let url: URL;
		try {
			url = new URL(request.url);
		} catch {
			return notFound();
		}
		const runId = claudeOrchestratorSiteRunId(url.hostname);
		if (url.protocol !== `${CLAUDE_ORCHESTRATOR_SITE_SCHEME}:` || !runId) return notFound();
		let relative: string;
		try {
			relative = url.pathname.split("/").map(decodeURIComponent).join("/");
		} catch {
			return notFound();
		}
		if (relative.includes("\0") || relative.split(/[\\/]/).includes("..")) return notFound();
		if (relative === "/" || relative === "") relative = "/index.html";
		const type = CONTENT_TYPES[path.extname(relative).toLowerCase()];
		if (!type) return notFound();
		const root = await rootFor(runId);
		if (!root || !path.isAbsolute(root)) return notFound();
		try {
			const realRoot = await fs.realpath(root);
			const target = await fs.realpath(path.resolve(realRoot, `.${relative}`));
			if (!within(realRoot, target)) return notFound();
			const stat = await fs.stat(target);
			if (!stat.isFile() || stat.size > MAX_FILE_BYTES) return notFound();
			const headers = {
				"content-type": type,
				"content-length": String(stat.size),
				"cache-control": "no-store",
				"x-content-type-options": "nosniff",
				"content-security-policy": CLAUDE_ORCHESTRATOR_SITE_POLICY,
				"referrer-policy": "no-referrer",
			};
			if (request.method === "HEAD") return new Response(null, { status: 200, headers });
			return new Response(new Uint8Array(await fs.readFile(target)), { status: 200, headers });
		} catch {
			return notFound();
		}
	};
}
