import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it } from "vitest";
import { useX00Store } from "../../stores/x00-store";
import { X00TitlebarButton } from "./X00TitlebarButton";

describe("X00TitlebarButton", () => {
	beforeEach(() => {
		useX00Store.setState({ open: false, unseen: 0, speaking: false });
	});

	it("opens and closes X-00's panel", async () => {
		render(<X00TitlebarButton />);
		const button = screen.getByRole("button", { name: "Talk to X-00" });
		expect(button).toHaveAttribute("aria-expanded", "false");
		await userEvent.click(button);
		expect(useX00Store.getState().open).toBe(true);
		expect(screen.getByTestId("x00-titlebar-button")).toHaveAttribute("aria-expanded", "true");
		await userEvent.click(screen.getByTestId("x00-titlebar-button"));
		expect(useX00Store.getState().open).toBe(false);
	});

	it("shows how many alerts are waiting", () => {
		useX00Store.setState({ unseen: 12 });
		render(<X00TitlebarButton />);
		expect(screen.getByTestId("x00-titlebar-unseen")).toHaveTextContent("9+");
	});
});
