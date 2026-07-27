import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const srcRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "src");

// Regression guards for the session list overflow fix: a long session list
// must scroll inside the sidebar instead of stretching the app-shell grid row
// past 100vh, which pushed the composer below the viewport. The visible
// behavior is verified in the browser QA; these checks pin the CSS contract.
test("sidebar is height-constrained so it cannot stretch the app-shell row", async () => {
  const css = await readFile(path.join(srcRoot, "styles.css"), "utf8");
  const sidebarRule = css.slice(css.indexOf(".sidebar {"), css.indexOf("}", css.indexOf(".sidebar {")));
  assert.match(sidebarRule, /display: flex;/);
  assert.match(sidebarRule, /flex-direction: column;/);
  // Without min-height: 0 the grid item's automatic minimum size lets a long
  // list force the row (and the whole page) taller than the viewport.
  assert.match(sidebarRule, /min-height: 0;/);
});

test("session list scrolls within the sidebar and keeps fixed entries visible", async () => {
  const css = await readFile(path.join(srcRoot, "styles.css"), "utf8");
  // The sidebar-top wrapper must give the list a bounded flex track.
  const topRule = css.slice(css.indexOf(".sidebar-top {"), css.indexOf("}", css.indexOf(".sidebar-top {")));
  assert.match(topRule, /flex: 1 1 auto;/);
  assert.match(topRule, /min-height: 0;/);
  // The list itself owns the scrollbar.
  const listRule = css.slice(css.indexOf(".session-list {"), css.indexOf("}", css.indexOf(".session-list {")));
  assert.match(listRule, /flex: 1 1 auto;/);
  assert.match(listRule, /min-height: 0;/);
  assert.match(listRule, /overflow-y: auto;/);
  // New Session and the archived-view toggle stay put instead of scrolling
  // away or being compressed by the shrinking list.
  const newSessionRule = css.slice(css.indexOf(".new-session {"), css.indexOf("}", css.indexOf(".new-session {")));
  assert.match(newSessionRule, /flex: 0 0 auto;/);
  const archiveLinkRule = css.slice(css.indexOf(".archive-view-link {"), css.indexOf("}", css.indexOf(".archive-view-link {")));
  assert.match(archiveLinkRule, /flex: 0 0 auto;/);
});

test("app shell stays viewport-locked and the composer keeps its grid track", async () => {
  const css = await readFile(path.join(srcRoot, "styles.css"), "utf8");
  const shellRule = css.slice(css.indexOf(".app-shell {"), css.indexOf("}", css.indexOf(".app-shell {")));
  assert.match(shellRule, /height: 100vh;/);
  assert.match(shellRule, /overflow: hidden;/);
  const workspaceRule = css.slice(css.indexOf(".workspace {"), css.indexOf("}", css.indexOf(".workspace {")));
  assert.match(workspaceRule, /grid-template-rows: auto minmax\(0, 1fr\) auto;/);
  assert.match(workspaceRule, /min-height: 0;/);
});
