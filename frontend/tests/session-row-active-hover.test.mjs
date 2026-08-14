import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const srcRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "src");

// Regression guard for the active-row hover fix: the currently open session
// must keep its active background while hovered instead of switching to the
// generic hover tint. The visual behavior is verified in the browser QA;
// these checks pin the CSS contract.
test("active session row keeps its background on hover", async () => {
  const css = await readFile(path.join(srcRoot, "styles.css"), "utf8");
  const activeRule = css.slice(css.indexOf(".session-row.active {"), css.indexOf("}", css.indexOf(".session-row.active {")));
  const activeHoverRule = css.slice(css.indexOf(".session-row.active:hover {"), css.indexOf("}", css.indexOf(".session-row.active:hover {")));
  assert.match(activeHoverRule, /background:\s*#f0f2f1;/);
  // The hover rule must match the base active background so hovering the open
  // row does not wash out the selected state.
  assert.equal(
    activeHoverRule.match(/background:\s*(#[0-9a-f]+);/)[1],
    activeRule.match(/background:\s*(#[0-9a-f]+);/)[1],
  );
});

test("generic hover tint still applies to non-active rows", async () => {
  const css = await readFile(path.join(srcRoot, "styles.css"), "utf8");
  const hoverRule = css.slice(css.indexOf(".session-row:hover {"), css.indexOf("}", css.indexOf(".session-row:hover {")));
  assert.match(hoverRule, /background:\s*#f1f3f2;/);
});
