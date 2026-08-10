import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { BEEPER_PATH, isBeeperPath } from "../src/routes.js";

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

test("the standalone Beeper route matches only its direct path", () => {
	assert.equal(BEEPER_PATH, "/beeper");
	assert.equal(isBeeperPath("/beeper"), true);
	assert.equal(isBeeperPath("/beeper/"), true);
	assert.equal(isBeeperPath("/"), false);
	assert.equal(isBeeperPath("/beeper/settings"), false);
});

test("the app selects a standalone page that reuses Companion", async () => {
	const main = await readFile(path.join(frontendRoot, "src", "main.jsx"), "utf8");
	const page = await readFile(path.join(frontendRoot, "src", "companion", "BeeperPage.jsx"), "utf8");
	const companion = await readFile(path.join(frontendRoot, "src", "companion", "Companion.jsx"), "utf8");
	assert.ok(main.includes("isBeeperPath(window.location.pathname) ? BeeperPage : App"));
	assert.ok(page.includes("<Companion"));
	assert.ok(page.includes("standalone"));
	assert.ok(page.includes("<SettingsModal"));
	assert.ok(companion.includes('target="_blank"'));
	assert.ok(companion.includes('aria-label="Open Beeper in new page"'));
});

test("standalone Beeper layout fills its page and keeps responsive card queries", async () => {
	const styles = await readFile(path.join(frontendRoot, "src", "styles.css"), "utf8");
	assert.ok(styles.includes(".beeper-page"));
	assert.ok(styles.includes(".companion-layer.standalone"));
	assert.ok(styles.includes("@container companion-card (min-width: 680px)"));
	assert.ok(styles.includes("@container companion-card (max-height: 390px)"));
});
