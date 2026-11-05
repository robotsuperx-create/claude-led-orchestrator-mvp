import { afterEach, describe, expect, it, vi } from "vitest";
import { parseStoresHubUrl, probeStoresHub } from "./stores-hub";

const APP_ORIGIN = "app://renderer";

describe("parseStoresHubUrl", () => {
	it.each([
		["http://localhost:4400", "http://localhost:4400/"],
		["  http://127.0.0.1:4400/ar-store/ ", "http://127.0.0.1:4400/ar-store/"],
		["https://localhost:4443/", "https://localhost:4443/"],
	])("accepts the loopback address %s", (raw, url) => {
		expect(parseStoresHubUrl(raw, APP_ORIGIN)).toEqual({ ok: true, url });
	});

	it.each([
		"localhost:4400",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"ftp://localhost/",
		"http://user:secret@localhost:4400/",
		"",
	])("rejects %j as not an http(s) address", (raw) => {
		expect(parseStoresHubUrl(raw, APP_ORIGIN)).toEqual({ ok: false, reason: "invalid" });
	});

	it.each(["https://example.com/", "http://192.168.1.20:4400/", "http://localhost.example.com:4400/"])(
		"rejects %s as not on this computer",
		(raw) => {
			expect(parseStoresHubUrl(raw, APP_ORIGIN)).toEqual({ ok: false, reason: "notLocal" });
		},
	);

	it("never frames the app's own origin", () => {
		expect(parseStoresHubUrl("http://localhost:5173/stores", "http://localhost:5173")).toEqual({
			ok: false,
			reason: "invalid",
		});
	});
});

describe("probeStoresHub", () => {
	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it("treats any answer, even an opaque one, as reachable", async () => {
		const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 200 }));
		vi.stubGlobal("fetch", fetchMock);

		await expect(probeStoresHub("http://localhost:4400/")).resolves.toBe(true);
		expect(fetchMock).toHaveBeenCalledWith(
			"http://localhost:4400/",
			expect.objectContaining({ mode: "no-cors", cache: "no-store", credentials: "omit" }),
		);
	});

	it("reports unreachable on a network error", async () => {
		vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
		await expect(probeStoresHub("http://localhost:4400/")).resolves.toBe(false);
	});

	it("gives up after the timeout", async () => {
		vi.stubGlobal(
			"fetch",
			vi.fn((_url: string, init: RequestInit) => new Promise((_resolve, reject) => {
				init.signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")));
			})),
		);
		await expect(probeStoresHub("http://localhost:4400/", undefined, 5)).resolves.toBe(false);
	});
});
