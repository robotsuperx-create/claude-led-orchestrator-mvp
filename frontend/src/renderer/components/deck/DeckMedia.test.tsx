import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { MediaInfo, MediaItem } from "../../../shared/media";
import { DeckMedia } from "./DeckMedia";

const mocks = vi.hoisted(() => ({
	info: undefined as MediaInfo | undefined,
	items: [] as MediaItem[],
	create: vi.fn(),
	remove: vi.fn(),
	openSettings: vi.fn(),
}));

vi.mock("../../hooks/useMedia", () => ({
	useMediaInfo: () => ({ data: mocks.info }),
	useMediaServerStatus: () => ({ data: { reachable: true, device: "NVIDIA GeForce RTX 4090", vramBytes: 25_757_220_864 } }),
	useMediaItems: () => ({ data: mocks.items }),
	useCreateMedia: () => ({ mutate: mocks.create, isPending: false, error: null }),
	useUploadMedia: () => ({ mutate: vi.fn(), isPending: false, error: null }),
	useRemoveMedia: () => ({ mutate: mocks.remove }),
	mediaErrorCode: () => null,
	mediaUrl: (itemId: string, name: string) => `xrobot-media://library/${itemId}/${name}`,
}));
vi.mock("../../stores/ui-store", () => ({ useUiStore: (pick: (state: { openGlobalSettings: () => void }) => unknown) => pick({ openGlobalSettings: mocks.openSettings }) }));

const item = (n: number, overrides: Partial<MediaItem>): MediaItem => ({
	id: String(n).repeat(32).slice(0, 32),
	capability: "image",
	kind: "image",
	prompt: "a robot on a black bridge",
	source: "app",
	state: "done",
	files: [{ name: "1.png", type: "image/png", bytes: 10 }],
	createdAt: "",
	updatedAt: "",
	...overrides,
});

beforeEach(() => {
	mocks.info = { enabled: true, voice: true, capabilities: [{ id: "image", kind: "image", builtIn: true }, { id: "video", kind: "video", builtIn: true }, { id: "music", kind: "audio", builtIn: true }, { id: "animate", kind: "video", builtIn: false, title: "Animate", input: "image" }, { id: "restyle", kind: "image", builtIn: true, input: "image" }] };
	mocks.items = [];
	mocks.create.mockReset();
	mocks.remove.mockReset();
	mocks.openSettings.mockReset();
});

describe("DeckMedia", () => {
	it("says what is missing when no GPU server is set, and opens settings", () => {
		mocks.info = { enabled: false, reason: "no_token", voice: false, capabilities: [] };
		render(<DeckMedia />);
		expect(screen.getByTestId("media-server")).toHaveTextContent("The GPU server needs its token.");
		expect(screen.queryByTestId("media-composer")).toBeNull();
		fireEvent.click(screen.getByRole("button", { name: "Open settings" }));
		expect(mocks.openSettings).toHaveBeenCalledWith("missionControl");
	});

	it("makes a portrait picture from words and shows the card", () => {
		mocks.items = [item(1, {}), item(2, { kind: "video", capability: "video", state: "running", files: [] }), item(3, { state: "failed", failure: "rejected", files: [] })];
		render(<DeckMedia />);
		expect(screen.getByTestId("media-server")).toHaveTextContent("NVIDIA GeForce RTX 4090 · 24 GB");
		expect(within(screen.getByRole("tablist", { name: "What to make" })).getAllByRole("tab")).toHaveLength(3);
		fireEvent.click(screen.getByRole("button", { name: "Portrait 9:16" }));
		fireEvent.change(screen.getByTestId("media-prompt"), { target: { value: "  hero shot for the tint store  " } });
		fireEvent.click(screen.getByTestId("media-make"));
		expect(mocks.create).toHaveBeenCalledWith({ capability: "image", prompt: "hero shot for the tint store", width: 768, height: 1344 }, expect.anything());
		expect(screen.getByRole("img", { name: "a robot on a black bridge" })).toHaveAttribute("src", `xrobot-media://library/${"1".repeat(32)}/1.png`);
		expect(screen.getByTestId(`media-item-${"2".repeat(32)}`)).toHaveTextContent("Being made");
		expect(screen.getByTestId(`media-item-${"3".repeat(32)}`)).toHaveTextContent("The server lacks a model or node");
	});

	it("works on a picture from the library with a capability that takes one, and deletes on a second press", () => {
		mocks.items = [item(1, {})];
		render(<DeckMedia />);
		fireEvent.click(screen.getByRole("button", { name: "Use" }));
		expect(within(screen.getByRole("tablist", { name: "What to make" })).getByRole("tab", { name: "Animate" })).toHaveAttribute("aria-selected", "true");
		fireEvent.change(screen.getByTestId("media-prompt"), { target: { value: "slow push in" } });
		fireEvent.click(screen.getByTestId("media-make"));
		expect(mocks.create).toHaveBeenCalledWith(expect.objectContaining({ capability: "animate", input: "1".repeat(32), seconds: 4 }), expect.anything());
		const remove = screen.getByTestId(`media-delete-${"1".repeat(32)}`);
		fireEvent.click(remove);
		expect(mocks.remove).not.toHaveBeenCalled();
		fireEvent.click(remove);
		expect(mocks.remove).toHaveBeenCalledWith("1".repeat(32));
	});

	it("turns one product picture into an ad pack: three shapes and a video", () => {
		mocks.items = [item(1, { source: "upload", capability: "upload", prompt: "handbag.png" })];
		render(<DeckMedia />);
		fireEvent.click(screen.getByRole("button", { name: "Use" }));
		const pack = screen.getByTestId("media-ad-pack");
		expect(pack).toBeDisabled();
		fireEvent.change(screen.getByTestId("media-prompt"), { target: { value: "on black marble in a luxury boutique" } });
		fireEvent.click(pack);
		const asked = mocks.create.mock.calls.map(([input]) => input);
		expect(asked).toEqual([
			{ capability: "restyle", width: 768, height: 1344, prompt: "on black marble in a luxury boutique", input: "1".repeat(32) },
			{ capability: "restyle", width: 1024, height: 1024, prompt: "on black marble in a luxury boutique", input: "1".repeat(32) },
			{ capability: "restyle", width: 1344, height: 768, prompt: "on black marble in a luxury boutique", input: "1".repeat(32) },
			{ capability: "animate", seconds: 4, prompt: "on black marble in a luxury boutique", input: "1".repeat(32) },
		]);
	});

	it("asks for a batch tonight, and shows the meter and what waits", () => {
		mocks.items = [item(4, { state: "scheduled", files: [], runAt: "2026-11-17T02:00:00.000Z" })];
		render(<DeckMedia />);
		fireEvent.click(screen.getByRole("button", { name: "×10" }));
		fireEvent.click(screen.getByTestId("media-tonight"));
		fireEvent.change(screen.getByTestId("media-prompt"), { target: { value: "coloring pages, friendly robots" } });
		fireEvent.click(screen.getByTestId("media-make"));
		const asked = mocks.create.mock.calls[0][0];
		expect(asked).toMatchObject({ capability: "image", count: 10 });
		expect(new Date(asked.runAt).getHours()).toBe(2);
		expect(new Date(asked.runAt).getTime()).toBeGreaterThan(Date.now());
		expect(screen.getByTestId(`media-item-${"4".repeat(32)}`)).toHaveTextContent("Scheduled for");
		expect(screen.getByTestId("media-meter")).toHaveTextContent("On today 0.0 h");
	});
});
