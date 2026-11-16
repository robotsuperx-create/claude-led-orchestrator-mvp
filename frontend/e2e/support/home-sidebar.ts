import { expect, type Page } from "@playwright/test";

/**
 * Home is the X-ROBOT deck and hides the sidebar until it is opened there.
 * Tests that start on Home and use the sidebar open it first, as a person would.
 */
export async function openSidebarOnHome(page: Page): Promise<void> {
	await expect(page.getByTestId("home-deck")).toBeVisible();
	await page.getByRole("button", { name: "Expand sidebar", exact: true }).first().click();
	await expect(page.locator('[data-slot="sidebar"][data-state="expanded"]').first()).toBeAttached();
}
