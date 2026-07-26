import { mkdir, writeFile } from "node:fs/promises";
import { chromium } from "@playwright/test";

// Browser QA for the English-only Web UI. Point it at a daemon that serves
// the built app with the seeded English QA fixtures (see frontend/design-qa.md
// and the task QA notes). Captures desktop and narrow screenshots and fails
// on console errors or Han characters visible in the rendered UI.
//
// Usage: node scripts/capture-ui-qa.mjs <base-url> <output-dir>

const [baseURL, outDir] = process.argv.slice(2);
if (!baseURL || !outDir) {
  throw new Error("Usage: node scripts/capture-ui-qa.mjs <base-url> <output-dir>");
}
await mkdir(outDir, { recursive: true });

const hanPattern = /[\u3400-\u4dbf\u4e00-\u9fff]/u;
const consoleErrors = [];
const dialogMessages = [];
const hanFindings = [];

const browser = await chromium.launch();

async function newPage(viewport) {
  const page = await browser.newPage({ viewport, deviceScaleFactor: 1 });
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(`[${viewport.width}px] ${message.text()}`);
  });
  page.on("pageerror", (error) => consoleErrors.push(`[${viewport.width}px] ${error.message}`));
  page.on("dialog", async (dialog) => {
    dialogMessages.push(dialog.message());
    await dialog.dismiss();
  });
  return page;
}

async function checkNoHan(page, label) {
  const text = await page.locator("body").innerText();
  const lines = text.split("\n").filter((line) => hanPattern.test(line));
  if (lines.length) hanFindings.push(`${label}: ${lines.join(" | ")}`);
}

async function shot(page, name) {
  await page.screenshot({ path: `${outDir}/${name}.png` });
  await checkNoHan(page, name);
}

const desktop = await newPage({ width: 1440, height: 1024 });
await desktop.goto(baseURL, { waitUntil: "networkidle" });

// Existing session with messages, agent output, and an approval card.
await desktop.getByRole("button", { name: "Fix the login endpoint" }).click();
await desktop.getByText("Approval required").waitFor();
await shot(desktop, "01-desktop-session-approval");

// Empty conversation state.
await desktop.getByRole("button", { name: "Docs cleanup" }).click();
await desktop.getByRole("heading", { name: "Start a new Session" }).waitFor();
await shot(desktop, "02-desktop-empty-state");

// Settings panels.
await desktop.getByRole("button", { name: "Settings" }).click();
await desktop.getByRole("dialog").waitFor();
await desktop.getByRole("button", { name: "Providers" }).waitFor();
await shot(desktop, "03-settings-providers");

await desktop.getByRole("button", { name: "Agents" }).click();
await desktop.getByRole("button", { name: /Codex Main Codex/ }).click();
await shot(desktop, "04-settings-agents");

// Validation error surfaced by Save all (empty agent name).
const nameInput = desktop.locator("#settings-agent-0-name");
const originalName = await nameInput.inputValue();
await nameInput.fill("");
await desktop.getByRole("button", { name: "Save all" }).click();
await desktop.getByText("Agent name is required").waitFor();
await shot(desktop, "05-settings-validation-error");

// Restore the name, make a real change, save, and confirm the Saved state.
await nameInput.fill(originalName);
await desktop.locator("#settings-agent-0-name").fill(`Codex Main (QA run ${Date.now() % 100000})`);
await desktop.getByRole("button", { name: "Save all" }).click();
await desktop.getByText("Saved", { exact: true }).waitFor();
await shot(desktop, "06-settings-saved");

// Unsaved-close confirmation (native dialog text is recorded, not shown).
await desktop.locator("#settings-agent-0-name").fill("Unsaved edit");
await desktop.keyboard.press("Escape");
await desktop.waitForTimeout(300);

// Back to the main UI after closing settings.
await desktop.getByRole("button", { name: "Close settings" }).click();
await shot(desktop, "07-desktop-after-settings");
await desktop.close();

// Narrow viewport: the sidebar starts hidden and overlays the workspace when
// opened; picking a session closes it again.
const narrow = await newPage({ width: 390, height: 844 });
await narrow.goto(baseURL, { waitUntil: "networkidle" });
await shot(narrow, "10-narrow-main");

await narrow.getByRole("button", { name: "Toggle session list" }).click();
await narrow.getByRole("button", { name: "Fix the login endpoint" }).click();
await narrow.getByText("Approval required").waitFor();
await shot(narrow, "11-narrow-session");

await narrow.getByRole("button", { name: "Toggle session list" }).click();
await narrow.getByRole("button", { name: "Settings" }).click();
await narrow.getByRole("dialog").waitFor();
await narrow.getByRole("button", { name: "Providers" }).waitFor();
await shot(narrow, "12-narrow-settings-providers");

await narrow.getByRole("button", { name: "Agents" }).click();
await narrow.locator(".settings-card-toggle").first().waitFor();
await shot(narrow, "13-narrow-settings-agents");
await narrow.close();

await browser.close();

const report = { consoleErrors, dialogMessages, hanFindings };
await writeFile(`${outDir}/ui-qa-report.json`, `${JSON.stringify(report, null, 2)}\n`);
console.log(JSON.stringify(report, null, 2));
if (consoleErrors.length || hanFindings.length) process.exit(1);
