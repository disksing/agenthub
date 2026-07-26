import assert from "node:assert/strict";
import { access, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { archiveListError, pickActiveAfterArchive } from "../src/archive.js";

const srcRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "src");

test("pickActiveAfterArchive keeps the selection for other sessions", () => {
  const sessions = [{ id: "ses_1" }, { id: "ses_2" }];
  assert.equal(pickActiveAfterArchive(sessions, "ses_1", "ses_2"), "ses_2");
  assert.equal(pickActiveAfterArchive(sessions, "ses_1", ""), "");
});

test("pickActiveAfterArchive converges a selected archive to the next session", () => {
  const remaining = [{ id: "ses_2" }, { id: "ses_3" }];
  assert.equal(pickActiveAfterArchive(remaining, "ses_1", "ses_1"), "ses_2");
  assert.equal(pickActiveAfterArchive([], "ses_1", "ses_1"), "");
  assert.equal(pickActiveAfterArchive(null, "ses_1", "ses_1"), "");
});

test("archiveListError names the session and keeps the server reason", () => {
  assert.equal(
    archiveListError({ id: "ses_1", title: "Fix login" }, "session has a running turn"),
    'Failed to archive "Fix login": session has a running turn',
  );
  assert.equal(archiveListError({ id: "ses_1", title: "" }, "conflict"), 'Failed to archive "ses_1": conflict');
  assert.equal(archiveListError(null, ""), 'Failed to archive "session": unknown error');
});

// Structural guarantees for the inline list archive button. The interaction
// itself is verified in the browser QA (hover, focus, click, narrow layout);
// these checks pin the markup and CSS contracts that make it accessible.
test("list archive button uses stable IDs, blocks parent events and is list-only", async () => {
  const app = await readFile(path.join(srcRoot, "App.jsx"), "utf8");
  // The button only exists in the default (non-archived) list.
  assert.match(app, /\{!archivedView && \(/);
  // The request targets the stable session ID, never the title.
  assert.match(app, /api\(`\/v1\/sessions\/\$\{session\.id\}`, \{ method: "DELETE"/);
  // Row selection, navigation and double-click must not fire from the button.
  const buttonBlock = app.slice(app.indexOf('className="session-row-archive"'));
  assert.match(buttonBlock, /onClick=\{\(event\) => \{ event\.stopPropagation\(\); archiveFromList\(item\); \}\}/);
  assert.match(buttonBlock, /onDoubleClick=\{\(event\) => event\.stopPropagation\(\)\}/);
  assert.match(buttonBlock, /onMouseDown=\{\(event\) => event\.stopPropagation\(\)\}/);
  // Accessible name and tooltip follow the "Archive session <title>" contract.
  assert.match(buttonBlock, /aria-label=\{`Archive session \$\{item\.title \|\| item\.id\}`\}/);
  assert.match(buttonBlock, /aria-busy=\{itemArchiving \|\| undefined\}/);
  // In-progress and non-archivable rows cannot submit.
  assert.match(buttonBlock, /disabled=\{itemArchiving \|\| !itemArchivable\}/);
  // Duplicate submissions are blocked by the per-session pending set.
  assert.match(app, /if \(listArchivingIds\.has\(session\.id\)\) return;/);
  // Failure keeps the item and surfaces the error; success converges selection.
  assert.match(app, /setError\(archiveListError\(session, value\.message\)\)/);
  assert.match(app, /setActiveId\(\(current\) => pickActiveAfterArchive\(remaining, session\.id, current\)\)/);
});

test("list archive button is hover/focus revealed without layout shifts", async () => {
  const css = await readFile(path.join(srcRoot, "styles.css"), "utf8");
  // The button is always rendered (stable space) and only visually hidden.
  const buttonRule = css.slice(css.indexOf(".session-row-archive {"), css.indexOf("}", css.indexOf(".session-row-archive {")));
  assert.match(buttonRule, /opacity: 0;/);
  assert.match(buttonRule, /pointer-events: none;/);
  // Hover over the row (including moving from the title onto the button)
  // reveals it; keyboard focus anywhere in the row does too.
  assert.match(css, /\.session-row:hover \.session-row-archive/);
  assert.match(css, /\.session-row:focus-within \.session-row-archive/);
  assert.match(css, /\.session-row-archive:focus-visible/);
  // Hover-less devices keep a visible entry point on the selected row.
  assert.match(css, /@media \(hover: none\) \{\s*\.session-row\.active \.session-row-archive/);
});

// Regression guards: the details panel must not offer a second archive entry
// point. The hover/focus list button is the only archive action in the UI.
test("details panel has no archive button, dialog or dedicated handler", async () => {
  const app = await readFile(path.join(srcRoot, "App.jsx"), "utf8");
  // No details-panel archive button, confirm dialog or modal import.
  assert.equal(app.includes("ArchiveConfirmModal"), false, "App.jsx still wires the archive confirm dialog");
  assert.equal(app.includes("openArchiveConfirm"), false, "App.jsx still has the details archive handler");
  assert.equal(app.includes("archive-button"), false, "details panel still renders an archive button");
  assert.equal(app.includes("Archive Session"), false, "details panel still shows an Archive Session label");
  // The details aside keeps only the informational read-only note for
  // already-archived sessions; it is not an action.
  const details = app.slice(app.indexOf('<aside className="details">'));
  assert.equal(/aria-label="[^"]*[Aa]rchive/.test(details), false, "details panel still exposes an archive action");
  const archiveRow = details.slice(details.indexOf("archive-row"));
  assert.equal(archiveRow.includes("<button"), false, "archive row should only hold the read-only note");
  assert.match(details, /className="archived-note"/);
});

test("archive confirm dialog component is deleted", async () => {
  await assert.rejects(
    access(path.join(srcRoot, "ArchiveConfirmModal.jsx")),
    (error) => error?.code === "ENOENT",
    "ArchiveConfirmModal.jsx should be removed",
  );
});

test("details-only archive styles are removed, shared note styles stay", async () => {
  const css = await readFile(path.join(srcRoot, "styles.css"), "utf8");
  for (const removed of [".archive-button", ".archive-hint", ".confirm-dialog"]) {
    assert.equal(css.includes(removed), false, `styles.css still defines ${removed}`);
  }
  assert.match(css, /\.archived-note \{/);
  assert.match(css, /\.session-row-archive \{/);
});
