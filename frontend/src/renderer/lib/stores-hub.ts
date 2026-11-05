// The owner's storefronts are built by a separate static-site repo and served
// on this computer as one preview hub. The Stores page frames that hub, so
// only loopback http(s) addresses are accepted: the renderer CSP allows frames
// from localhost and 127.0.0.1 only (vite.renderer.config.ts, frame-src).

export const STORES_HUB_DEFAULT_URL = "http://localhost:4400/";
export const STORES_HUB_STORAGE_KEY = "xr-stores-hub-url";
export const STORES_HUB_PROBE_TIMEOUT_MS = 2500;

const LOOPBACK_HOSTS = new Set(["localhost", "127.0.0.1"]);

export type StoresHubUrlResult =
	| { ok: true; url: string }
	| { ok: false; reason: "invalid" | "notLocal" };

export function parseStoresHubUrl(raw: string, appOrigin = currentOrigin()): StoresHubUrlResult {
	let url: URL;
	try {
		url = new URL(raw.trim());
	} catch {
		return { ok: false, reason: "invalid" };
	}
	if ((url.protocol !== "http:" && url.protocol !== "https:") || url.username || url.password) {
		return { ok: false, reason: "invalid" };
	}
	if (!LOOPBACK_HOSTS.has(url.hostname)) return { ok: false, reason: "notLocal" };
	// In dev the renderer itself is served from localhost. Framing it with
	// allow-scripts + allow-same-origin would let the frame drop its sandbox.
	if (url.origin === appOrigin) return { ok: false, reason: "invalid" };
	return { ok: true, url: url.href };
}

function currentOrigin(): string | undefined {
	try {
		return window.location.origin;
	} catch {
		return undefined;
	}
}

export function readStoredStoresHubUrl(): string {
	try {
		const stored = window.localStorage.getItem(STORES_HUB_STORAGE_KEY);
		if (stored) {
			const parsed = parseStoresHubUrl(stored);
			if (parsed.ok) return parsed.url;
		}
	} catch {
		// Storage can be blocked or unavailable; the default address still works.
	}
	return STORES_HUB_DEFAULT_URL;
}

export function writeStoredStoresHubUrl(url: string): void {
	try {
		window.localStorage.setItem(STORES_HUB_STORAGE_KEY, url);
	} catch {
		// Keep the address for this visit even when it cannot be remembered.
	}
}

/**
 * Resolves true when anything answers at the address. An opaque (no-cors)
 * response is enough: the frame, not this probe, reads the page.
 */
export async function probeStoresHub(
	url: string,
	signal?: AbortSignal,
	timeoutMs = STORES_HUB_PROBE_TIMEOUT_MS,
): Promise<boolean> {
	const controller = new AbortController();
	const abort = () => controller.abort();
	signal?.addEventListener("abort", abort, { once: true });
	const timer = setTimeout(abort, timeoutMs);
	try {
		await fetch(url, { mode: "no-cors", cache: "no-store", credentials: "omit", signal: controller.signal });
		return true;
	} catch {
		return false;
	} finally {
		clearTimeout(timer);
		signal?.removeEventListener("abort", abort);
	}
}
