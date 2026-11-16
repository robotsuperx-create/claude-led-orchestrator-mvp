import { mkdtempSync, mkdirSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { createMediaHandler, parseRange } from "./media-protocol";

const id = "b".repeat(32);

function library() {
	const root = mkdtempSync(path.join(tmpdir(), "xrobot-media-"));
	mkdirSync(path.join(root, "items", id), { recursive: true });
	writeFileSync(path.join(root, "items", id, "1.png"), Buffer.from("\x89PNG\r\n\x1a\n0123456789", "binary"));
	writeFileSync(path.join(root, "items", id, "item.json"), "{}");
	writeFileSync(path.join(root, "secret.png"), "secret");
	return root;
}

describe("the media library scheme", () => {
	it("serves a library file with its type, and ranges for the player", async () => {
		const root = library();
		const handler = createMediaHandler(async () => root);
		const whole = await handler(new Request(`xrobot-media://library/${id}/1.png`));
		expect(whole.status).toBe(200);
		expect(whole.headers.get("content-type")).toBe("image/png");
		expect(whole.headers.get("x-content-type-options")).toBe("nosniff");
		expect((await whole.arrayBuffer()).byteLength).toBe(18);
		const part = await handler(new Request(`xrobot-media://library/${id}/1.png`, { headers: { range: "bytes=8-11" } }));
		expect(part.status).toBe(206);
		expect(part.headers.get("content-range")).toBe("bytes 8-11/18");
		expect(new TextDecoder().decode(await part.arrayBuffer())).toBe("0123");
		expect((await handler(new Request(`xrobot-media://library/${id}/1.png`, { headers: { range: "bytes=99-" } }))).status).toBe(416);
	});

	it("never serves anything outside the library's items", async () => {
		const root = library();
		const other = mkdtempSync(path.join(tmpdir(), "xrobot-outside-"));
		writeFileSync(path.join(other, "1.png"), "outside");
		symlinkSync(other, path.join(root, "items", "c".repeat(32)), "dir");
		const handler = createMediaHandler(async () => root);
		for (const url of [`xrobot-media://library/${id}/item.json`, `xrobot-media://library/${id}/..%2F..%2Fsecret.png`, `xrobot-media://library/${"c".repeat(32)}/1.png`, `xrobot-media://elsewhere/${id}/1.png`]) {
			expect((await handler(new Request(url))).status, url).toBe(404);
		}
		expect((await createMediaHandler(async () => null)(new Request(`xrobot-media://library/${id}/1.png`))).status).toBe(404);
		expect((await handler(new Request(`xrobot-media://library/${id}/1.png`, { method: "POST" }))).status).toBe(405);
	});

	it("reads one byte range", () => {
		expect(parseRange(null, 10)).toBeNull();
		expect(parseRange("bytes=0-", 10)).toEqual({ start: 0, end: 9 });
		expect(parseRange("bytes=-4", 10)).toEqual({ start: 6, end: 9 });
		expect(parseRange("bytes=2-100", 10)).toEqual({ start: 2, end: 9 });
		expect(parseRange("bytes=5-2", 10)).toBe("invalid");
		expect(parseRange("bytes=0-1,4-5", 10)).toBe("invalid");
	});
});
