import { describe, expect, it } from "vitest";
import { mediaFileUrl, parseMediaCreateInput, parseMediaFileUrl, parseMediaInfo, parseMediaItem, parseMediaItems, parseMediaStatus } from "./media";

const id = "a".repeat(32);

describe("media contract", () => {
	it("builds and reads library file URLs, and nothing else", () => {
		expect(mediaFileUrl(id, "1.png")).toBe(`xrobot-media://library/${id}/1.png`);
		expect(parseMediaFileUrl(mediaFileUrl(id, "2.webm"))).toEqual({ itemId: id, name: "2.webm" });
		for (const bad of [`xrobot-media://library/${id}/item.json`, `xrobot-media://library/${id}/../1.png`, `xrobot-media://other/${id}/1.png`, `xrobot-media://library/nope/1.png`, `https://library/${id}/1.png`, `xrobot-media://library/${id}/1.png?x=1`]) {
			expect(parseMediaFileUrl(bad)).toBeNull();
		}
	});

	it("keeps only allowlisted fields from the daemon", () => {
		const info = parseMediaInfo({ enabled: true, root: "/home/me/.ao/media", voice: true, capabilities: [{ id: "image", kind: "image", builtIn: true, title: "Picture" }, { id: "../x", kind: "image" }, { id: "animate", kind: "video", input: "image" }] });
		expect(info).toEqual({ enabled: true, voice: true, capabilities: [{ id: "image", kind: "image", builtIn: true, title: "Picture" }, { id: "animate", kind: "video", builtIn: false, input: "image" }] });
		expect(JSON.stringify(info)).not.toContain(".ao");
		expect(parseMediaStatus({ reachable: true, device: "RTX\u00074090", vramBytes: 24e9, queue: 2 })).toEqual({ reachable: true, device: "RTX4090", vramBytes: 24e9, queue: 2 });
		const item = parseMediaItem({ id, capability: "image", kind: "image", prompt: "robot", source: "x00", state: "done", failure: "nonsense", files: [{ name: "1.png", type: "image/png", bytes: 5 }, { name: "1.png", type: "text/html", bytes: 1 }, { name: "../x.png", type: "image/png" }], width: 1024, createdAt: "t", updatedAt: "t" });
		expect(item).toMatchObject({ id, source: "x00", files: [{ name: "1.png", type: "image/png", bytes: 5 }], width: 1024 });
		expect(item?.failure).toBeUndefined();
		expect(parseMediaItems({ items: [{ id: "bad" }, { id, capability: "upload", kind: "image", state: "done", files: [] }] })).toHaveLength(1);
	});

	it("rebuilds a request or refuses it", () => {
		expect(parseMediaCreateInput({ capability: "image", prompt: "  a robot  ", width: 1024, source: "x00", extra: "dropped" })).toEqual({ capability: "image", prompt: "a robot", width: 1024, source: "x00" });
		expect(parseMediaCreateInput({ capability: "image", prompt: "" })).toBeNull();
		expect(parseMediaCreateInput({ capability: "../etc", prompt: "x" })).toBeNull();
		expect(parseMediaCreateInput({ capability: "image", prompt: "x", width: -1 })).toBeNull();
		expect(parseMediaCreateInput({ capability: "image", prompt: "x", input: "../../secret" })).toBeNull();
	});
});
