import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StoresView } from "./StoresView";
import { STORES_HUB_STORAGE_KEY } from "../lib/stores-hub";

const mocks = vi.hoisted(() => ({ hideShellTopbar: false }));

vi.mock("../lib/platform", async (importOriginal) => ({
	...await importOriginal<typeof import("../lib/platform")>(),
	hidesShellTopbar: () => mocks.hideShellTopbar,
}));

const fetchMock = vi.fn();

function hubFrame() {
	return screen.findByTitle("Stores preview");
}

describe("StoresView", () => {
	beforeEach(() => {
		mocks.hideShellTopbar = false;
		window.localStorage.clear();
		fetchMock.mockReset().mockResolvedValue(new Response(null, { status: 200 }));
		vi.stubGlobal("fetch", fetchMock);
	});

	afterEach(() => {
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});

	it("frames the default hub in a sandbox once it answers", async () => {
		render(<StoresView />);

		expect(screen.getByRole("status")).toHaveTextContent("Checking the stores preview…");
		const frame = await hubFrame();
		expect(frame.tagName).toBe("IFRAME");
		expect(frame).toHaveAttribute("src", "http://localhost:4400/");
		expect(frame.getAttribute("sandbox")?.split(" ").sort()).toEqual(
			["allow-forms", "allow-popups", "allow-same-origin", "allow-scripts"],
		);
		expect(screen.getByRole("textbox", { name: "Stores preview address" })).toHaveValue("http://localhost:4400/");
		expect(fetchMock).toHaveBeenCalledWith("http://localhost:4400/", expect.objectContaining({ mode: "no-cors" }));
	});

	it("explains how to start the preview when nothing answers, and retries", async () => {
		fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
		render(<StoresView />);

		expect(await screen.findByRole("heading", { name: "The stores preview isn't running" })).toBeInTheDocument();
		expect(screen.getByText("In the x-robot-stores folder, run this command, then retry.")).toBeInTheDocument();
		expect(screen.getByText("npm run preview:hub")).toBeInTheDocument();
		expect(screen.queryByTitle("Stores preview")).not.toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Retry" }));

		expect(await hubFrame()).toHaveAttribute("src", "http://localhost:4400/");
		expect(fetchMock).toHaveBeenCalledTimes(2);
	});

	it.each([
		["file:///etc/passwd", "Enter the preview address, starting with http:// or https://."],
		["javascript:alert(1)", "Enter the preview address, starting with http:// or https://."],
		["https://example.com/", "Only addresses on this computer (localhost or 127.0.0.1) open here."],
	])("rejects %s without framing or remembering it", async (address, message) => {
		render(<StoresView />);
		await hubFrame();
		fetchMock.mockClear();

		const field = screen.getByRole("textbox", { name: "Stores preview address" });
		await userEvent.clear(field);
		await userEvent.type(field, address);
		await userEvent.click(screen.getByRole("button", { name: "Open" }));

		expect(screen.getByRole("alert")).toHaveTextContent(message);
		expect(field).toHaveAttribute("aria-invalid", "true");
		expect(await hubFrame()).toHaveAttribute("src", "http://localhost:4400/");
		expect(fetchMock).not.toHaveBeenCalled();
		expect(window.localStorage.getItem(STORES_HUB_STORAGE_KEY)).toBeNull();

		await userEvent.type(field, " ");
		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
	});

	it("opens and remembers a new local address", async () => {
		const { unmount } = render(<StoresView />);
		await hubFrame();

		const field = screen.getByRole("textbox", { name: "Stores preview address" });
		await userEvent.clear(field);
		await userEvent.type(field, "http://127.0.0.1:4401{Enter}");

		await waitFor(async () => expect(await hubFrame()).toHaveAttribute("src", "http://127.0.0.1:4401/"));
		expect(field).toHaveValue("http://127.0.0.1:4401/");
		expect(window.localStorage.getItem(STORES_HUB_STORAGE_KEY)).toBe("http://127.0.0.1:4401/");

		unmount();
		fetchMock.mockClear();
		render(<StoresView />);
		expect(screen.getByRole("textbox", { name: "Stores preview address" })).toHaveValue("http://127.0.0.1:4401/");
		expect(await hubFrame()).toHaveAttribute("src", "http://127.0.0.1:4401/");
		expect(fetchMock).toHaveBeenCalledWith("http://127.0.0.1:4401/", expect.anything());
	});

	it("ignores a remembered address that is no longer allowed", async () => {
		window.localStorage.setItem(STORES_HUB_STORAGE_KEY, "https://example.com/");
		render(<StoresView />);

		expect(await hubFrame()).toHaveAttribute("src", "http://localhost:4400/");
	});

	it("keeps working when storage throws", async () => {
		vi.spyOn(window.localStorage, "getItem").mockImplementation(() => {
			throw new Error("storage blocked");
		});
		vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
			throw new Error("storage blocked");
		});
		render(<StoresView />);
		expect(await hubFrame()).toHaveAttribute("src", "http://localhost:4400/");

		const field = screen.getByRole("textbox", { name: "Stores preview address" });
		await userEvent.clear(field);
		await userEvent.type(field, "http://localhost:4402/{Enter}");

		await waitFor(async () => expect(await hubFrame()).toHaveAttribute("src", "http://localhost:4402/"));
	});

	it("reloads the frame and checks the hub again", async () => {
		render(<StoresView />);
		const first = await hubFrame();

		await userEvent.click(screen.getByRole("button", { name: "Reload" }));

		await waitFor(async () => expect(await hubFrame()).not.toBe(first));
		expect(fetchMock).toHaveBeenCalledTimes(2);
	});

	it.each([true, false])("shows the page title only when the shell title is hidden (%s)", (hidden) => {
		mocks.hideShellTopbar = hidden;
		render(<StoresView />);
		const title = screen.getByRole("heading", { name: "Stores" });
		if (hidden) expect(title).not.toHaveClass("sr-only");
		else expect(title).toHaveClass("sr-only");
	});
});
