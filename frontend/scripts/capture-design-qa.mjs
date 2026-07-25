import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { chromium } from "@playwright/test";

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

await page.goto("http://127.0.0.1:4173/", { waitUntil: "networkidle" });
await page.getByRole("button", { name: "Codex", exact: true }).click();

const implementationPath = path.join(outputDir, "agenthub-implementation.png");
await page.screenshot({ path: implementationPath, fullPage: false });

await page.getByRole("button", { name: "Kimi", exact: true }).click();
await page.getByText("Kimi", { exact: true }).first().waitFor();

const composer = page.getByRole("textbox", { name: "消息" });
await composer.fill("请检查这个修复是否覆盖了 refresh token 过期的情况。");
await page.getByRole("button", { name: "发送消息" }).click();
await page.getByText("收到。我会先检查相关文件和当前实现，再给出最小范围的修改与验证结果。").waitFor();

await page.getByRole("button", { name: "新建 Session" }).click();
await page.getByRole("heading", { name: "开始新的 Session" }).waitFor();
await page.getByRole("button", { name: "修复登录接口" }).click();

await page.getByRole("button", { name: "收起详情" }).click();
await page.getByRole("button", { name: "切换详情" }).click();
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
    "Opened the Agent picker and selected Kimi",
    "Sent a message and received a mock Agent response",
    "Created a new Session and observed the empty state",
    "Returned to an existing Session",
    "Collapsed and reopened the details panel",
  ],
  consoleErrors,
};

await writeFile(path.join(outputDir, "qa-run.json"), `${JSON.stringify(report, null, 2)}\n`);
await browser.close();

console.log(JSON.stringify(report, null, 2));
