import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { chromium } from "@playwright/test";

// Captures the running AgentHub Web UI and builds a side-by-side comparison
// against the source design image. Expects the seeded QA environment serving
// the built app at http://127.0.0.1:4173/ (English fixtures only).

const [sourcePath, outputDir] = process.argv.slice(2);

if (!sourcePath || !outputDir) {
  throw new Error("Usage: node scripts/capture-design-qa.mjs <source-image> <output-dir>");
}

await mkdir(outputDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({
  viewport: { width: 1440, height: 1024 },
  deviceScaleFactor: 1,
});

const consoleErrors = [];
page.on("console", (message) => {
  if (message.type() === "error") consoleErrors.push(message.text());
});
page.on("pageerror", (error) => consoleErrors.push(error.message));

// The New Session flow asks for the working directory via window.prompt.
page.on("dialog", (dialog) => dialog.accept("/tmp/agenthub-qa"));

await page.goto("http://127.0.0.1:4173/", { waitUntil: "networkidle" });

const agentPicker = page.getByRole("combobox", { name: "Agent" });
await agentPicker.selectOption({ label: "Codex" });

const implementationPath = path.join(outputDir, "agenthub-implementation.png");
await page.screenshot({ path: implementationPath, fullPage: false });

await agentPicker.selectOption({ label: "Kimi" });

const composer = page.getByRole("textbox", { name: "Message" });
const fixtureMessage = "Please check whether this fix covers the refresh token expiry case.";
await composer.fill(fixtureMessage);
await page.getByRole("button", { name: "Send message" }).click();
await page.getByText(fixtureMessage, { exact: true }).waitFor();

await page.getByRole("button", { name: "New Session" }).click();
await page.getByRole("heading", { name: "Start a new Session" }).waitFor();
await page.getByRole("button", { name: "Fix the login endpoint" }).click();

await page.getByRole("button", { name: "Toggle details panel" }).click();
await page.getByRole("button", { name: "Toggle details panel" }).click();
await page.getByText("Session ID", { exact: true }).waitFor();

const sourceData = (await readFile(sourcePath)).toString("base64");
const implementationData = (await readFile(implementationPath)).toString("base64");
const comparisonPage = await browser.newPage({
  viewport: { width: 1440, height: 548 },
  deviceScaleFactor: 1,
});

await comparisonPage.setContent(`
  <!doctype html>
  <html>
    <head>
      <style>
        * { box-sizing: border-box; }
        body { margin: 0; background: #e7e9e8; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
        .labels { display: grid; grid-template-columns: 1fr 1fr; height: 36px; color: #303536; font-size: 13px; font-weight: 650; }
        .labels div { display: flex; align-items: center; padding-left: 16px; border-right: 1px solid #c8cccb; }
        .compare { display: grid; grid-template-columns: 1fr 1fr; width: 1440px; height: 512px; }
        .pane { overflow: hidden; background: white; border-right: 1px solid #c8cccb; }
        img { display: block; width: 720px; height: 512px; object-fit: fill; }
      </style>
    </head>
    <body>
      <div class="labels"><div>REFERENCE</div><div>IMPLEMENTATION</div></div>
      <div class="compare">
        <div class="pane"><img src="data:image/png;base64,${sourceData}" /></div>
        <div class="pane"><img src="data:image/png;base64,${implementationData}" /></div>
      </div>
    </body>
  </html>
`);

const comparisonPath = path.join(outputDir, "agenthub-comparison.png");
await comparisonPage.screenshot({ path: comparisonPath, fullPage: false });

const report = {
  viewport: { width: 1440, height: 1024, deviceScaleFactor: 1 },
  sourcePath,
  implementationPath,
  comparisonPath,
  primaryInteractions: [
    "Selected Codex and then Kimi in the Agent picker",
    "Sent a message and observed it in the conversation",
    "Created a new Session and observed the empty state",
    "Returned to an existing Session",
    "Collapsed and reopened the details panel",
  ],
  consoleErrors,
};

await writeFile(path.join(outputDir, "qa-run.json"), `${JSON.stringify(report, null, 2)}\n`);
await browser.close();

console.log(JSON.stringify(report, null, 2));
