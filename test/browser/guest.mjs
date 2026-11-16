// Drives the guest web client in a real browser against a live share.
// Run by scripts/e2e.sh, which sets up an isolated herdr-core, relay and
// share, and passes everything through the environment:
//
//   E2E_LINK      the share link
//   E2E_RELAY     the relay's base URL
//   E2E_TERMINAL  the shared terminal (address or name)
//   E2E_SHARE     path to the herdr-share binary
//   E2E_ARTIFACTS folder for screenshots
//   PW_CHANNEL    optional Playwright browser channel ("chrome" uses the
//                 installed Chrome with a throwaway profile)
import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "playwright";

const env = (name) => {
	const v = process.env[name];
	if (!v) throw new Error(`${name} is not set`);
	return v;
};
const LINK = env("E2E_LINK");
const RELAY = env("E2E_RELAY");
const TERMINAL = env("E2E_TERMINAL");
const SHARE = env("E2E_SHARE");
const ARTIFACTS = process.env.E2E_ARTIFACTS ?? "test-results";
mkdirSync(ARTIFACTS, { recursive: true });

const sh = (cmd, args) => execFileSync(cmd, args, { encoding: "utf8" });
const screen = () => sh("hc", ["read", "--lines", "200", TERMINAL]);
const termGrid = () => {
	const t = JSON.parse(sh("hc", ["get", "--json", TERMINAL]));
	return { cols: t.cols, rows: t.rows };
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

let step = 0;
const log = (msg) => console.log(`  [${++step}] ${msg}`);
function check(cond, msg) {
	if (!cond) throw new Error(`check failed: ${msg}`);
}

const browser = await chromium.launch({ headless: true, channel: process.env.PW_CHANNEL || undefined });
const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, locale: "en-US" });
const page = await context.newPage();
const consoleErrors = [];
page.on("console", (m) => {
	if (m.type() === "error") consoleErrors.push(m.text());
});
page.on("pageerror", (e) => consoleErrors.push(String(e)));

const state = () => page.evaluate(() => window.herdrShare && { phase: window.herdrShare.phase, mode: window.herdrShare.mode, grid: window.herdrShare.grid });
const text = () => page.evaluate(() => window.herdrShare?.text() ?? "");
async function waitFor(what, fn, timeout = 15000) {
	const end = Date.now() + timeout;
	for (;;) {
		const v = await fn();
		if (v) return v;
		if (Date.now() > end) {
			await page.screenshot({ path: join(ARTIFACTS, `failure-${step}.png`) });
			throw new Error(`timed out waiting for ${what}`);
		}
		await sleep(150);
	}
}

try {
	await page.goto(LINK);
	await waitFor("the share to go live", async () => (await state())?.phase === "live");
	await waitFor("the terminal's earlier output", async () => (await text()).includes("READY-42"));
	const s = await state();
	check(s.mode === "view", `starts in view mode, got ${s.mode}`);
	const grid = termGrid();
	check(s.grid.cols === grid.cols && s.grid.rows === grid.rows, `guest grid ${JSON.stringify(s.grid)} matches ${JSON.stringify(grid)}`);
	check((await page.getByTestId("mode").getAttribute("data-mode")) === "view", "mode chip says view only");
	await page.getByText("e2e · browser").waitFor();
	const frame = await page.locator(".frame").boundingBox();
	check(frame && frame.y >= 44 && frame.x >= 0, `the terminal frame starts inside the page (${JSON.stringify(frame)})`);
	log("guest sees the live terminal in view-only mode, at the sharer's grid");
	await page.screenshot({ path: join(ARTIFACTS, "1-view-only.png") });

	await page.locator(".xterm").click();
	await page.keyboard.type("blocked-input");
	await page.keyboard.press("Enter");
	await sleep(1200);
	check(!screen().includes("blocked-input"), "view-only keystrokes never reach the terminal");
	log("keystrokes are dropped in view-only mode");

	sh(SHARE, ["mode", TERMINAL, "control"]);
	await waitFor("control mode", async () => (await state())?.mode === "control");
	await page.getByText("The sharer let you type.").waitFor();
	check((await page.getByTestId("mode").getAttribute("data-mode")) === "control", "mode chip says can type");
	log("switching to control mode reaches the guest at once");

	await page.locator(".xterm").click();
	await page.keyboard.type("hello from the browser");
	await page.keyboard.press("Enter");
	await waitFor("the echo in the page", async () => (await text()).includes("got:hello from the browser"));
	check(screen().includes("got:hello from the browser"), "the terminal received the guest's line");
	const after = termGrid();
	check(after.cols === grid.cols && after.rows === grid.rows, `typing kept the grid at ${grid.cols}x${grid.rows}, got ${after.cols}x${after.rows}`);
	log("the guest types into the terminal without resizing it");
	await page.screenshot({ path: join(ARTIFACTS, "2-control.png") });

	sh(SHARE, ["mode", TERMINAL, "view"]);
	await waitFor("view mode again", async () => (await state())?.mode === "view");
	await page.keyboard.type("revoked-input");
	await page.keyboard.press("Enter");
	await sleep(1200);
	check(!screen().includes("revoked-input"), "keystrokes after revoking control never reach the terminal");
	log("revoking control takes effect immediately");

	const second = await context.newPage();
	await second.goto(LINK);
	await second.waitForFunction(() => window.herdrShare?.phase === "live");
	await page.getByTestId("viewers").filter({ hasText: "2 viewers" }).waitFor();
	await second.close();
	await page.getByTestId("viewers").filter({ hasText: "1 viewer" }).waitFor();
	log("viewer counts follow guests joining and leaving");

	// While the share is live: a link with the wrong key cannot decrypt it.
	const [base, key] = LINK.split("#");
	const wrong = await context.newPage();
	await wrong.goto(`${base}#${key.slice(0, -4)}AAAA`);
	await wrong.getByText("This link does not work").waitFor();
	await wrong.getByText("key does not match").waitFor();
	await wrong.close();
	const nokey = await context.newPage();
	await nokey.goto(base);
	await nokey.getByText("missing its key").waitFor();
	await nokey.close();
	check((await state())?.phase === "live", "bad links do not disturb other viewers");
	log("broken links explain themselves");

	sh(SHARE, ["stop", TERMINAL]);
	await waitFor("the end of the share", async () => (await state())?.phase === "ended");
	await page.getByText("Sharing has ended").waitFor();
	await page.getByText("The sharer stopped sharing.").waitFor();
	log("stopping ends the share for the guest");
	await page.screenshot({ path: join(ARTIFACTS, "3-ended.png") });

	await page.reload();
	await waitFor("a dead link", async () => (await state())?.phase === "ended");
	await page.getByText("This share does not exist or has already ended.").waitFor();
	log("the link stops working for good");

	const landing = await context.newPage();
	await landing.goto(`${RELAY}/`);
	await landing.getByText("herdr-share relay").first().waitFor();
	await landing.getByText("protocol 1").waitFor();
	await landing.screenshot({ path: join(ARTIFACTS, "4-landing.png") });
	await landing.close();
	log("the relay's landing page loads");

	const zh = await browser.newContext({ locale: "zh-CN" });
	const zhPage = await zh.newPage();
	await zhPage.goto(`${base}#${key}`);
	await zhPage.getByText("分享已结束").waitFor();
	await zh.close();
	log("Chinese browsers get the Chinese interface");

	const relevant = consoleErrors.filter((e) => !/WebSocket|ERR_|Failed to load resource/.test(e));
	check(relevant.length === 0, `no page errors: ${relevant.join(" | ")}`);
	log("no script errors in the page");
	console.log("browser e2e: all checks passed");
} finally {
	await browser.close();
}
