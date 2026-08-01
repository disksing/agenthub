import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const srcRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "src");

// Regression guards for the session-switch events fix: selecting another
// session used to keep showing the previous session's events until the new
// durable history arrived. The workspace must instead drop the stale events
// immediately and show a loading placeholder while the catch-up fetch runs.
test("switching sessions clears stale events and raises the loading flag", async () => {
  const app = await readFile(path.join(srcRoot, "App.jsx"), "utf8");
  // The selection-change branch must empty the list before the fetch.
  const switchBranch = app.slice(
    app.indexOf("eventsSessionRef.current !== activeId"),
    app.indexOf("catchUpEvents(activeId)"),
  );
  assert.match(switchBranch, /setEvents\(\[\]\)/);
  assert.match(switchBranch, /setEventsLoading\(true\)/);
});

test("the loading flag is released after catch-up succeeds or fails", async () => {
  const app = await readFile(path.join(srcRoot, "App.jsx"), "utf8");
  const catchUp = app.slice(app.indexOf("catchUpEvents(activeId)"));
  const success = catchUp.slice(catchUp.indexOf(".then("), catchUp.indexOf(".catch("));
  assert.match(success, /setEventsLoading\(false\)/);
  // The failure path must also stop loading and stay guarded against a
  // disposed (already switched-away) effect.
  const failure = catchUp.slice(catchUp.indexOf(".catch("));
  assert.match(failure, /if \(disposed\) return;/);
  assert.match(failure, /setEventsLoading\(false\)/);
});

test("the conversation renders a status placeholder while events load", async () => {
  const app = await readFile(path.join(srcRoot, "App.jsx"), "utf8");
  assert.match(app, /eventsLoading \? \(/);
  const placeholder = app.slice(app.indexOf('className="events-loading"'));
  assert.match(placeholder, /role="status"/);
  assert.match(placeholder, /className="spin"/);
});

test("the loading placeholder has dedicated centered styles", async () => {
  const css = await readFile(path.join(srcRoot, "styles.css"), "utf8");
  const rule = css.slice(css.indexOf(".events-loading {"), css.indexOf("}", css.indexOf(".events-loading {")));
  assert.match(rule, /display: flex;/);
  assert.match(rule, /justify-content: center;/);
  assert.match(rule, /height: 100%;/);
});
