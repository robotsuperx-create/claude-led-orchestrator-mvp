import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { CLAUDE_ORCHESTRATOR_SITE_POLICY, createClaudeOrchestratorSiteHandler } from "./claude-orchestrator-site";

const RUN = "0123456789abcdef0123456789abcdef";
const RUN_ID = `claude-run-${RUN}`;
const dirs: string[] = [];

function site() {
	const base = mkdtempSync(path.join(tmpdir(), "xrobot-site-"));
	dirs.push(base);
	const root = path.join(base, "run");
	mkdirSync(path.join(root, "docs"), { recursive: true });
	writeFileSync(path.join(root, "index.html"), "<h1>Signup</h1>");
	writeFileSync(path.join(root, "docs", "about page.html"), "<p>About</p>");
	writeFileSync(path.join(root, "app.js"), "console.log(1)");
	writeFileSync(path.join(root, "secret.env"), "KEY=1");
	writeFileSync(path.join(base, "outside.html"), "<p>outside</p>");
	return { root, base };
}

afterEach(() => {
	for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
});

describe("the live preview's page server", () => {
	it("serves a run's pages from its worktree, on its own origin, under a strict policy", async () => {
		const { root } = site();
		const handler = createClaudeOrchestratorSiteHandler(async (runId) => (runId === RUN_ID ? root : null));
		const page = await handler(new Request(`xrobot-site://run-${RUN}/index.html`));
		expect(page.status).toBe(200);
		expect(page.headers.get("content-type")).toBe("text/html; charset=utf-8");
		expect(page.headers.get("content-security-policy")).toBe(CLAUDE_ORCHESTRATOR_SITE_POLICY);
		expect(CLAUDE_ORCHESTRATOR_SITE_POLICY).not.toMatch(/http:/);
		expect(await page.text()).toBe("<h1>Signup</h1>");
		expect((await handler(new Request(`xrobot-site://run-${RUN}/`))).status).toBe(200);
		expect((await handler(new Request(`xrobot-site://run-${RUN}/docs/about%20page.html`))).status).toBe(200);
		expect((await handler(new Request(`xrobot-site://run-${RUN}/app.js`))).headers.get("content-type")).toBe("text/javascript; charset=utf-8");
	});

	it("never leaves the worktree, never lists it, and serves only known file types", async () => {
		const { root, base } = site();
		symlinkSync(path.join(base, "outside.html"), path.join(root, "link.html"));
		const handler = createClaudeOrchestratorSiteHandler(async (runId) => (runId === RUN_ID ? root : null));
		for (const url of [
			`xrobot-site://run-${RUN}/../outside.html`,
			`xrobot-site://run-${RUN}/%2e%2e/outside.html`,
			`xrobot-site://run-${RUN}/docs/`,
			`xrobot-site://run-${RUN}/secret.env`,
			`xrobot-site://run-${RUN}/link.html`,
			`xrobot-site://run-${RUN}/missing.html`,
			`xrobot-site://run-ffffffffffffffffffffffffffffffff/index.html`,
			`xrobot-site://somewhere/index.html`,
		]) {
			expect((await handler(new Request(url))).status, url).toBe(404);
		}
		expect((await handler(new Request(`xrobot-site://run-${RUN}/index.html`, { method: "POST", body: "x" }))).status).toBe(405);
	});
});
