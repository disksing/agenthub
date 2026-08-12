import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import {
  ARCHIVED_STATE,
  archiveDisabledReason,
  isArchivable,
  isArchived,
  sessionStatusLabel,
  sessionsQuery,
} from "../src/archive.js";

const srcRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "src");

const base = { id: "ses_1", state: "stopped" };

test("isArchived only matches the archived state", () => {
  assert.equal(isArchived({ ...base, state: ARCHIVED_STATE }), true);
  assert.equal(isArchived(base), false);
  assert.equal(isArchived(null), false);
});

test("inactive sessions are archivable", () => {
  assert.equal(isArchivable(base), true);
  assert.equal(archiveDisabledReason(base), "");
});

test("active or unsafe sessions are not archivable", () => {
  for (const state of ["ready", "starting", "running", "waiting_approval", "stopping", "failed"]) {
    assert.equal(isArchivable({ ...base, state }), false, state);
    assert.match(archiveDisabledReason({ ...base, state }), /Stop the session/, state);
  }
  assert.equal(isArchivable({ ...base, currentTurnId: "turn_1" }), false);
  assert.match(archiveDisabledReason({ ...base, currentTurnId: "turn_1" }), /running turn/);
  assert.equal(isArchivable({ ...base, pendingApprovalIds: ["apr_1"] }), false);
  assert.match(archiveDisabledReason({ ...base, pendingApprovalIds: ["apr_1"] }), /pending approval/);
  assert.equal(isArchivable({ ...base, state: ARCHIVED_STATE }), false);
  assert.match(archiveDisabledReason({ ...base, state: ARCHIVED_STATE }), /already archived/);
  assert.equal(isArchivable(null), false);
  assert.match(archiveDisabledReason(null), /No session/);
});

test("session status exposes stopping and stopped reason", () => {
  assert.equal(sessionStatusLabel({ state: "stopping" }), "stopping");
  assert.equal(sessionStatusLabel({ state: "waiting_approval" }), "waiting approval");
  assert.equal(sessionStatusLabel({ state: "stopped", stopReason: "daemon_recovery" }), "stopped · daemon recovery");
  assert.equal(sessionStatusLabel({ state: "stopped" }), "stopped");
  assert.equal(sessionStatusLabel(null), "No session yet");
});

test("sessionsQuery hides archived sessions by default", () => {
  assert.equal(sessionsQuery(false), "/v1/sessions");
  assert.equal(sessionsQuery(true), "/v1/sessions?archived=true");
});

// Archiving a session requires no confirmation, so browser-native
// confirm()/prompt() stay banned from the production UI sources.
test("UI sources use no browser-native confirm or prompt", async () => {
  for (const file of ["App.jsx"]) {
    const content = await readFile(path.join(srcRoot, file), "utf8");
    assert.equal(/\bwindow\.(confirm|prompt|alert)\s*\(/.test(content), false, `${file} uses a native dialog`);
    assert.equal(/[^.\w](confirm|prompt|alert)\s*\(/.test(content), false, `${file} calls a native dialog`);
  }
});
